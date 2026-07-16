package budget

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"wechat-bot-server/internal/wechat"
)

// MediaType represents the type of media in a buffered message.
type MediaType int

const (
	MediaText  MediaType = iota
	MediaImage
	MediaFile
	MediaVideo
)

// bufferedMessage holds a message waiting to be sent when budget recovers.
type bufferedMessage struct {
	Text      string
	FilePath  string
	MediaType MediaType
}

// SendResult is returned by BudgetedSend/BudgetedSendMedia.
type SendResult struct {
	Status        string `json:"status"`         // "sent", "buffered", "error"
	Remaining     int    `json:"remaining,omitempty"`
	BufferedCount int    `json:"buffered_count,omitempty"`
	Error         string `json:"error,omitempty"`
}

// InfoResult is returned by Info.
type InfoResult struct {
	Remaining     int  `json:"remaining"`
	Limit         int  `json:"limit"`
	BufferMode    bool `json:"buffer_mode"`
	BufferedCount int  `json:"buffered_count"`
}

// Manager implements message budget counting and buffer queue management.
type Manager struct {
	mu           sync.Mutex
	sendBudget   int
	bufferMode   bool
	msgBuffer    []bufferedMessage
	wc           *wechat.Client
	budgetLimit  int
	maxBuffer    int
	activateCmd  string
}

// NewManager creates a new budget Manager.
func NewManager(wc *wechat.Client, budgetLimit, maxBuffer int) *Manager {
	return &Manager{
		wc:          wc,
		budgetLimit: budgetLimit,
		maxBuffer:   maxBuffer,
		sendBudget:  budgetLimit,
		activateCmd: "/",
	}
}

// Info returns the current budget state.
func (m *Manager) Info() InfoResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	return InfoResult{
		Remaining:     m.sendBudget,
		Limit:         m.budgetLimit,
		BufferMode:    m.bufferMode,
		BufferedCount: len(m.msgBuffer),
	}
}

// SetLimits updates budget limit and max buffer size.
func (m *Manager) SetLimits(budgetLimit, maxBuffer int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if budgetLimit > 0 {
		m.budgetLimit = budgetLimit
	}
	if maxBuffer > 0 {
		m.maxBuffer = maxBuffer
	}
}

// BudgetedSend sends a text message via budget control.
// Returns SendResult with status "sent" or "buffered".
func (m *Manager) BudgetedSend(toID, contextToken, text string) SendResult {
	m.mu.Lock()
	if m.sendBudget > 0 {
		m.sendBudget--
		m.mu.Unlock()
		if err := m.wc.SendMessage(toID, contextToken, text); err != nil {
			return SendResult{Status: "error", Error: err.Error()}
		}
		return SendResult{Status: "sent", Remaining: m.sendBudget}
	}

	// Budget exhausted — enter buffer mode.
	firstEntry := m.enterBufferModeLocked()
	m.bufferMessageLocked(bufferedMessage{Text: text, MediaType: MediaText})
	count := len(m.msgBuffer)
	m.mu.Unlock()
	if firstEntry {
		m.sendBufferActivationReminder()
	}
	return SendResult{Status: "buffered", BufferedCount: count}
}

// BudgetedSendMedia sends a media message via budget control.
func (m *Manager) BudgetedSendMedia(toID, contextToken, filePath string, mediaType MediaType) SendResult {
	m.mu.Lock()
	if m.sendBudget > 0 {
		m.sendBudget--
		m.mu.Unlock()
		if err := m.sendMedia(toID, contextToken, filePath, mediaType); err != nil {
			return SendResult{Status: "error", Error: err.Error()}
		}
		return SendResult{Status: "sent", Remaining: m.sendBudget}
	}

	// Budget exhausted — enter buffer mode.
	firstEntry := m.enterBufferModeLocked()
	m.bufferMessageLocked(bufferedMessage{FilePath: filePath, MediaType: mediaType})
	count := len(m.msgBuffer)
	m.mu.Unlock()
	if firstEntry {
		m.sendBufferActivationReminder()
	}
	return SendResult{Status: "buffered", BufferedCount: count}
}

// OnUserMessage processes an incoming user message.
// Returns true if the message should be added to the incoming queue.
func (m *Manager) OnUserMessage(msg wechat.Message) bool {
	m.mu.Lock()

	// Reset budget on every user message.
	m.sendBudget = m.budgetLimit

	// Check for pure activation command "/".
	if strings.TrimSpace(msg.Text) == m.activateCmd {
		log.Printf("[budget] activation command received, budget reset, not enqueuing")
		flushData := m.collectFlushLocked()
		m.mu.Unlock()
		m.doFlush(flushData)
		return false
	}

	// Flush buffer if in buffer mode.
	shouldFlush := m.bufferMode && len(m.msgBuffer) > 0
	var flushData []bufferedMessage
	if shouldFlush {
		flushData = m.collectFlushLocked()
	}

	m.bufferMode = false
	m.mu.Unlock()

	if shouldFlush {
		m.doFlush(flushData)
	}
	return true
}

// --- internal helpers ---

// collectFlushLocked collects all buffered messages and clears the buffer.
// Caller must hold m.mu. Returns data to be sent OUTSIDE the lock.
func (m *Manager) collectFlushLocked() []bufferedMessage {
	data := make([]bufferedMessage, len(m.msgBuffer))
	copy(data, m.msgBuffer)
	m.msgBuffer = nil
	m.bufferMode = false
	return data
}

// doFlush sends buffered messages without holding the budget lock.
func (m *Manager) doFlush(data []bufferedMessage) {
	if len(data) == 0 {
		return
	}

	ct := m.wc.LastContact()
	if ct.FromID == "" {
		log.Printf("[budget] no last contact, cannot flush buffer")
		return
	}

	// Group consecutive text messages into one combined text.
	var textParts []string
	for i := 0; i < len(data); {
		bm := data[i]
		if bm.MediaType == MediaText {
			textParts = nil
			j := i
			for j < len(data) && data[j].MediaType == MediaText {
				textParts = append(textParts, data[j].Text)
				j++
			}
			combined := strings.Join(textParts, "\n---\n")
			if err := m.wc.SendMessage(ct.FromID, ct.ContextToken, combined); err != nil {
				log.Printf("[budget] flush text error: %v", err)
			}
			i = j
		} else {
			if err := m.wc.SendFile(ct.FromID, ct.ContextToken, bm.FilePath); err != nil {
				// Try generic sendMedia for other types
				m.sendMedia(ct.FromID, ct.ContextToken, bm.FilePath, bm.MediaType)
			}
			i++
		}
	}
}

// enterBufferModeLocked marks the budget as exhausted and enters buffer mode.
// Returns true if this is the first entry (caller should send activation reminder).
// Caller must hold m.mu.
func (m *Manager) enterBufferModeLocked() bool {
	if !m.bufferMode {
		m.bufferMode = true
		return true
	}
	return false
}

// sendBufferActivationReminder sends the activation reminder without holding budget lock.
func (m *Manager) sendBufferActivationReminder() {
	ct := m.wc.LastContact()
	if ct.FromID != "" {
		msg := "本轮消息额度已用完，回复任意消息激活"
		if err := m.wc.SendMessage(ct.FromID, ct.ContextToken, msg); err != nil {
			log.Printf("[budget] failed to send buffer activation reminder: %v", err)
		}
	}
}

func (m *Manager) bufferMessageLocked(bm bufferedMessage) {
	if len(m.msgBuffer) >= m.maxBuffer {
		// Eviction: drop oldest text message.
		evicted := false
		for i, buf := range m.msgBuffer {
			if buf.MediaType == MediaText {
				m.msgBuffer = append(m.msgBuffer[:i], m.msgBuffer[i+1:]...)
				evicted = true
				break
			}
		}
		if !evicted {
			log.Printf("[budget] WARNING: buffer full and no text message to evict, dropping new message")
			return
		}
	}
	m.msgBuffer = append(m.msgBuffer, bm)
}

func (m *Manager) sendMedia(toID, contextToken, filePath string, mediaType MediaType) error {
	switch mediaType {
	case MediaImage:
		return m.wc.SendImage(toID, contextToken, filePath)
	case MediaFile:
		return m.wc.SendFile(toID, contextToken, filePath)
	case MediaVideo:
		return m.wc.SendVideo(toID, contextToken, filePath)
	default:
		return fmt.Errorf("unknown media type: %d", mediaType)
	}
}
