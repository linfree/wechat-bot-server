package queue

import (
	"sync"
	"time"
)

// IncomingMessage represents a message from a WeChat user stored in the incoming queue.
type IncomingMessage struct {
	FromUserID  string `json:"from_user_id"`
	Text        string `json:"text"`
	MessageType int    `json:"message_type"`
	Timestamp   string `json:"timestamp"`
}

// IncomingQueue is a thread-safe in-memory message queue for incoming WeChat messages.
type IncomingQueue struct {
	mu       sync.Mutex
	messages []IncomingMessage
	maxSize  int
}

// NewIncomingQueue creates a new incoming message queue.
func NewIncomingQueue(maxSize int) *IncomingQueue {
	if maxSize <= 0 {
		maxSize = 50
	}
	return &IncomingQueue{
		maxSize: maxSize,
	}
}

// Push adds a message to the queue. If the queue is full, the oldest message is dropped.
func (q *IncomingQueue) Push(msg IncomingMessage) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.messages) >= q.maxSize {
		q.messages = q.messages[1:]
	}
	q.messages = append(q.messages, msg)
}

// List returns all messages, optionally filtered by a "since" timestamp.
// If since is empty, all messages are returned.
func (q *IncomingQueue) List(since string) []IncomingMessage {
	q.mu.Lock()
	defer q.mu.Unlock()

	if since == "" {
		result := make([]IncomingMessage, len(q.messages))
		copy(result, q.messages)
		return result
	}

	sinceTime, err := time.Parse(time.RFC3339, since)
	if err != nil {
		// If parsing fails, return all messages.
		result := make([]IncomingMessage, len(q.messages))
		copy(result, q.messages)
		return result
	}

	var filtered []IncomingMessage
	for _, msg := range q.messages {
		msgTime, err := time.Parse(time.RFC3339, msg.Timestamp)
		if err != nil || msgTime.After(sinceTime) {
			filtered = append(filtered, msg)
		}
	}
	return filtered
}

// Clear removes all messages from the queue.
func (q *IncomingQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.messages = nil
}

// Size returns the current number of messages in the queue.
func (q *IncomingQueue) Size() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.messages)
}
