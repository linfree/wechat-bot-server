package api

import (
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"wechat-bot-server/internal/budget"
	"wechat-bot-server/internal/config"
	"wechat-bot-server/internal/queue"
	"wechat-bot-server/internal/wechat"
)

// WechatHandler holds dependencies for management API handlers.
type WechatHandler struct {
	Client    *wechat.Client
	Budget    *budget.Manager
	Queue     *queue.IncomingQueue
	ConfigMgr *config.Manager
}

// HandleGetQRCode returns the login QR code and starts polling for confirmation.
// GET /api/v1/wechat/qrcode
func (h *WechatHandler) HandleGetQRCode(c *gin.Context) {
	qrcodeID, qrcodeImg, err := h.Client.GetQRCode()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "wechat service unavailable"})
		return
	}

	// Start a single background poller for QR confirmation.
	h.Client.StartQRPolling(qrcodeID)

	c.JSON(http.StatusOK, gin.H{
		"qrcode_id":  qrcodeID,
		"qrcode_img": qrcodeImg,
	})
}

// HandleGetStatus returns the full connection status and runtime parameters.
// GET /api/v1/wechat/status
func (h *WechatHandler) HandleGetStatus(c *gin.Context) {
	clientStatus := h.Client.Status()
	connected := clientStatus == wechat.StatusConnected

	loginTime := h.Client.LoginTime()
	var loginTimeStr string
	var remainingHours, totalHours float64
	if !loginTime.IsZero() {
		loginTimeStr = loginTime.UTC().Format(time.RFC3339)
		elapsed := time.Since(loginTime)
		remaining := loginTime.Add(24 * time.Hour).Sub(time.Now())
		remainingHours = math.Round(remaining.Hours()*10) / 10
		if remainingHours < 0 {
			remainingHours = 0
		}
		totalHours = math.Round(elapsed.Hours()*10) / 10
	}

	cfg := h.ConfigMgr.Get()
	token := h.Client.Token()
	maskedToken := "****"
	if len(token) > 8 {
		maskedToken = token[:4] + "****" + token[len(token)-4:]
	}

	budgetInfo := h.Budget.Info()

	c.JSON(http.StatusOK, gin.H{
		"connected":                    connected,
		"status":                       string(clientStatus),
		"login_time":                   loginTimeStr,
		"elapsed_hours":                totalHours,
		"remaining_hours":              remainingHours,
		"masked_token":                 maskedToken,
		"send_budget": gin.H{
			"remaining": budgetInfo.Remaining,
			"limit":     budgetInfo.Limit,
		},
		"max_buffered_messages":       cfg.Budget.MaxBufferedMessages,
		"activation_warning_hours":    cfg.Reconnect.ActivationWarningHours,
		"activation_reminder_minutes": cfg.Reconnect.ActivationReminderMinutes,
		"buffer_mode":                 budgetInfo.BufferMode,
		"buffered_count":              budgetInfo.BufferedCount,
		"incoming_queue_size":         h.Queue.Size(),
	})
}

// HandleDisconnect actively disconnects the WeChat long-polling.
// POST /api/v1/wechat/disconnect
func (h *WechatHandler) HandleDisconnect(c *gin.Context) {
	// Send goodbye notification before disconnecting.
	ct := h.Client.LastContact()
	if ct.FromID != "" {
		_ = h.Client.SendMessage(ct.FromID, ct.ContextToken, "机器人已断开连接")
	}
	h.Client.Stop()
	// Clear saved token so restart doesn't auto-reconnect.
	_ = h.ConfigMgr.UpdateWechat("", "", "", "")
	c.JSON(http.StatusOK, gin.H{"status": "disconnected"})
}

// settingsRequest is the request body for PUT /api/v1/wechat/settings.
type settingsRequest struct {
	SendBudgetLimit           int `json:"send_budget_limit" binding:"required"`
	MaxBufferedMessages       int `json:"max_buffered_messages" binding:"required"`
	ActivationWarningHours    int `json:"activation_warning_hours" binding:"required"`
	ActivationReminderMinutes int `json:"activation_reminder_minutes" binding:"required"`
}

// HandleUpdateSettings updates runtime parameters and persists them.
// PUT /api/v1/wechat/settings
func (h *WechatHandler) HandleUpdateSettings(c *gin.Context) {
	var req settingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if req.SendBudgetLimit < 1 || req.SendBudgetLimit > 99 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "消息预算上限需在 1-99 之间"})
		return
	}
	if req.MaxBufferedMessages < 10 || req.MaxBufferedMessages > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缓冲队列上限需在 10-1000 之间"})
		return
	}
	if req.ActivationWarningHours < 1 || req.ActivationWarningHours > 23 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "续期提醒时间需在 1-23 小时之间"})
		return
	}
	if req.ActivationReminderMinutes < 10 || req.ActivationReminderMinutes > 1440 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "提醒间隔需在 10-1440 分钟之间"})
		return
	}

	// Persist settings.
	if err := h.ConfigMgr.UpdateSettings(req.SendBudgetLimit, req.MaxBufferedMessages, req.ActivationWarningHours, req.ActivationReminderMinutes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save settings"})
		return
	}

	// Update budget manager limits for immediate effect.
	h.Budget.SetLimits(req.SendBudgetLimit, req.MaxBufferedMessages)

	c.JSON(http.StatusOK, gin.H{"status": "saved"})
}
