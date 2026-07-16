package server

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pkg/browser"

	"wechat-bot-server/internal/budget"
	"wechat-bot-server/internal/config"
	"wechat-bot-server/internal/queue"
	"wechat-bot-server/internal/server/api"
	"wechat-bot-server/internal/wechat"
)

// ServerDeps holds all dependencies needed by the HTTP server.
type ServerDeps struct {
	WechatClient  *wechat.Client
	BudgetManager *budget.Manager
	IncomingQueue *queue.IncomingQueue
	ConfigMgr     *config.Manager
}

// NewRouter creates and configures a Gin router with all routes registered.
func NewRouter(deps ServerDeps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// Handler dependencies.
	wechatH := &api.WechatHandler{
		Client:    deps.WechatClient,
		Budget:    deps.BudgetManager,
		Queue:     deps.IncomingQueue,
		ConfigMgr: deps.ConfigMgr,
	}
	botH := &api.BotHandler{
		Client: deps.WechatClient,
		Budget: deps.BudgetManager,
		Queue:  deps.IncomingQueue,
	}

	// Static: serve the management web page and logo (embedded at compile time).
	r.GET("/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	})
	r.GET("/logo.png", func(c *gin.Context) {
		c.Data(http.StatusOK, "image/png", logoPNG)
	})

	// Management API.
	v1 := r.Group("/api/v1")
	wechatGroup := v1.Group("/wechat")
	{
		wechatGroup.GET("/qrcode", wechatH.HandleGetQRCode)
		wechatGroup.GET("/status", wechatH.HandleGetStatus)
		wechatGroup.POST("/disconnect", wechatH.HandleDisconnect)
		wechatGroup.PUT("/settings", wechatH.HandleUpdateSettings)
	}

	// Message send/receive API.
	botGroup := v1.Group("/wechat-bot")
	{
		sendGroup := botGroup.Group("/send")
		{
			sendGroup.POST("/text", botH.HandleSendText)
			sendGroup.POST("/image", botH.HandleSendImage)
			sendGroup.POST("/file", botH.HandleSendFile)
			sendGroup.POST("/video", botH.HandleSendVideo)
		}
		botGroup.GET("/messages", botH.HandleGetMessages)
		botGroup.DELETE("/messages", botH.HandleClearMessages)
	}

	return r
}

// Serve starts the HTTP server and opens the browser.
func Serve(router *gin.Engine, port int) error {
	addr := fmt.Sprintf(":%d", port)
	webURL := fmt.Sprintf("http://localhost:%d", port)

	log.Printf("[server] starting on %s", addr)

	// Open browser after server is ready (retry with backoff).
	go func() {
		var client http.Client
		for i := 0; i < 30; i++ {
			time.Sleep(200 * time.Millisecond)
			resp, err := client.Get(webURL)
			if err == nil {
				resp.Body.Close()
				break
			}
		}
		browser.OpenURL(webURL)
	}()

	return router.Run(addr)
}
