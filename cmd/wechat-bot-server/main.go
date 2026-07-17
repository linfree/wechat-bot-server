package main

import (
	_ "embed"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"wechat-bot-server/internal/budget"
	"wechat-bot-server/internal/config"
	"wechat-bot-server/internal/queue"
	"wechat-bot-server/internal/server"
	"wechat-bot-server/internal/tray"
	"wechat-bot-server/internal/wechat"
)

//go:embed logo.ico
var appIcon []byte

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfgMgr, err := config.NewManager()
	if err != nil {
		log.Fatalf("[main] config init failed: %v", err)
	}
	cfg := cfgMgr.Get()

	wc := wechat.NewClient(cfg.Wechat.BaseURL, cfg.Wechat.BotToken, wechat.ParseLoginTime(cfg.Wechat.LoginTime), cfg.Wechat.CDNBaseURL)
	if cfg.Wechat.LastFromID != "" {
		wc.SetLastContact(wechat.ContactInfo{FromID: cfg.Wechat.LastFromID, ContextToken: cfg.Wechat.LastContextToken})
	}

	reconnectCfg := wechat.ReconnectConfig{
		SessionDuration:           24 * time.Hour,
		ActivationWarningHours:    cfg.Reconnect.ActivationWarningHours,
		ActivationReminderMinutes: cfg.Reconnect.ActivationReminderMinutes,
		ForceBefore:               30 * time.Minute,
	}

	// Align with cc-go: OnTokenSaved called from pollQRCodeConfirmation / StartQRPolling.
	// Persists new token + baseURL + loginTime, restarts reconnect timer.
	wc.OnTokenSaved = func(token, baseURL string, loginTime time.Time) {
		cfgMgr.UpdateWechat(token, baseURL, cfg.Wechat.CDNBaseURL, loginTime.Format(time.RFC3339))
		wc.SetStatus(wechat.StatusConnected)
		wc.StartReconnectTimer(reconnectCfg)
		// Notify user that bot is online.
		ct := wc.LastContact()
		if ct.FromID != "" {
			_ = wc.SendMessage(ct.FromID, ct.ContextToken, "机器人已连接")
		}
	}

	// Align with cc-go: Start pollLoop + reconnect timer at startup.
	// The reconnect timer checks elapsed on first tick (1 min) and
	// triggers activation if needed.
	if cfg.Wechat.BotToken != "" {
		wc.Start()
		wc.StartReconnectTimer(reconnectCfg)
	}

	budgetMgr := budget.NewManager(wc, cfg.Budget.SendBudgetLimit, cfg.Budget.MaxBufferedMessages)

	queueMax := cfg.IncomingQueueMax
	if queueMax <= 0 {
		queueMax = 50
	}
	incomingQ := queue.NewIncomingQueue(queueMax)

	// Message pipeline: save LastContact → budget → incoming queue.
	go func() {
		for msg := range wc.Messages() {
			cfgMgr.UpdateLastContact(msg.FromUserID, msg.ContextToken)
			budgetMgr.OnUserMessage(msg)
			if strings.TrimSpace(msg.Text) != "/" {
				incomingQ.Push(queue.IncomingMessage{
					FromUserID:  msg.FromUserID,
					Text:        msg.Text,
					MessageType: msg.MessageType,
					Timestamp:   time.Now().Format(time.RFC3339),
				})
			}
		}
	}()

	deps := server.ServerDeps{
		WechatClient:  wc,
		BudgetManager: budgetMgr,
		IncomingQueue: incomingQ,
		ConfigMgr:     cfgMgr,
	}

	router := server.NewRouter(deps)

	// Always prefer the default port (18081); only fall back to the
	// previously-saved port if it differs and 18081 is taken.
	defaultPort := 18081
	savedPort := cfg.WebPort
	if savedPort <= 0 {
		savedPort = defaultPort
	}

	// Find an available port, trying the default first, then saved, then incrementing.
	actualPort := defaultPort
	for {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", actualPort))
		if err == nil {
			ln.Close()
			break
		}
		if actualPort == defaultPort && savedPort != defaultPort {
			actualPort = savedPort
			continue
		}
		log.Printf("[main] port %d in use, trying %d", actualPort, actualPort+1)
		actualPort++
	}

	// Persist the resolved port so external tools (skills) can read it.
	if actualPort != cfg.WebPort {
		if err := cfgMgr.UpdateWebPort(actualPort); err != nil {
			log.Printf("[main] failed to save port: %v", err)
		}
	}

	// Start HTTP server in background.
	go func() {
		if err := server.Serve(router, actualPort); err != nil {
			log.Printf("[main] server error: %v", err)
		}
	}()

	// Block until signal or tray quit.
	tray.Run(actualPort, wc, appIcon)

	// Shutdown with timeout to prevent hang on tray exit.
	done := make(chan struct{})
	go func() {
		wc.Stop()
		close(done)
	}()
	select {
	case <-done:
		log.Println("[main] exited cleanly")
	case <-time.After(5 * time.Second):
		log.Println("[main] shutdown timeout, forcing exit")
	}
}
