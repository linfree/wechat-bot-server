//go:build !windows

package tray

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"wechat-bot-server/internal/wechat"
)

// Run blocks on SIGINT/SIGTERM (no tray on non-Windows platforms).
func Run(port int, client *wechat.Client, iconBytes []byte) {
	wc = client
	quitCh = make(chan struct{})

	log.Printf("[tray] service running at http://localhost:%d", port)
	fmt.Printf("微信机器人服务已启动: http://localhost:%d\n按 Ctrl+C 退出\n", port)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	close(quitCh)
}
