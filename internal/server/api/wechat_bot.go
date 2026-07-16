package api

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"wechat-bot-server/internal/budget"
	"wechat-bot-server/internal/queue"
	"wechat-bot-server/internal/wechat"
)

// BotHandler holds dependencies for message send/receive API handlers.
type BotHandler struct {
	Client *wechat.Client
	Budget *budget.Manager
	Queue  *queue.IncomingQueue
}

// sendTextRequest is the request body for POST /api/v1/wechat-bot/send/text.
type sendTextRequest struct {
	Text     string `json:"text" binding:"required"`
	ToUserID string `json:"to_user_id"`
}

// HandleSendText sends a text message.
// POST /api/v1/wechat-bot/send/text
func (h *BotHandler) HandleSendText(c *gin.Context) {
	var req sendTextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "text is required"})
		return
	}
	if len(req.Text) > 3500 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "text exceeds 3500 characters"})
		return
	}

	toID, ctxToken := h.resolveTarget(req.ToUserID)
	if toID == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no recipient available, please send a message first"})
		return
	}

	result := h.Budget.BudgetedSend(toID, ctxToken, req.Text)
	if result.Status == "error" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error})
		return
	}
	c.JSON(http.StatusOK, result)
}

// sendMediaRequest is the request body for media send endpoints.
type sendMediaRequest struct {
	FilePath string `json:"file_path" binding:"required"`
	ToUserID string `json:"to_user_id"`
}

// HandleSendImage sends an image.
// POST /api/v1/wechat-bot/send/image
func (h *BotHandler) HandleSendImage(c *gin.Context) {
	h.handleSendMedia(c, budget.MediaImage)
}

// HandleSendFile sends a file.
// POST /api/v1/wechat-bot/send/file
func (h *BotHandler) HandleSendFile(c *gin.Context) {
	h.handleSendMedia(c, budget.MediaFile)
}

// HandleSendVideo sends a video.
// POST /api/v1/wechat-bot/send/video
func (h *BotHandler) HandleSendVideo(c *gin.Context) {
	h.handleSendMedia(c, budget.MediaVideo)
}

func (h *BotHandler) handleSendMedia(c *gin.Context, mediaType budget.MediaType) {
	var req sendMediaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_path is required"})
		return
	}

	// Verify file exists and is readable.
	info, err := os.Stat(req.FilePath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file not found: " + req.FilePath})
		return
	}
	if info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_path is a directory: " + req.FilePath})
		return
	}
	// Optional: check file size (< 50 MB).
	if info.Size() > 50*1024*1024 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file too large (max 50MB)"})
		return
	}

	toID, ctxToken := h.resolveTarget(req.ToUserID)
	if toID == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no recipient available, please send a message first"})
		return
	}

	result := h.Budget.BudgetedSendMedia(toID, ctxToken, req.FilePath, mediaType)
	if result.Status == "error" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error})
		return
	}
	c.JSON(http.StatusOK, result)
}

// HandleGetMessages returns messages from the incoming queue.
// GET /api/v1/wechat-bot/messages?since=ISO8601
func (h *BotHandler) HandleGetMessages(c *gin.Context) {
	since := c.Query("since")
	messages := h.Queue.List(since)
	if messages == nil {
		messages = []queue.IncomingMessage{}
	}
	c.JSON(http.StatusOK, messages)
}

// HandleClearMessages clears the incoming message queue.
// DELETE /api/v1/wechat-bot/messages
func (h *BotHandler) HandleClearMessages(c *gin.Context) {
	h.Queue.Clear()
	c.JSON(http.StatusOK, gin.H{"status": "cleared"})
}

// resolveTarget resolves the target user ID. If toID is empty, uses the last contact.
func (h *BotHandler) resolveTarget(toID string) (string, string) {
	if toID != "" {
		return toID, ""
	}
	ct := h.Client.LastContact()
	return ct.FromID, ct.ContextToken
}
