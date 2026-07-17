//go:build darwin

package tray

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/getlantern/systray"

	"wechat-bot-server/internal/wechat"
)

var (
	icon       []byte
	statusItem *systray.MenuItem
	openItem   *systray.MenuItem
	quitItem   *systray.MenuItem
	webURL     string
)

// Run starts the macOS menu bar tray (blocking).
func Run(port int, client *wechat.Client, iconBytes []byte) {
	wc = client
	icon = iconBytes
	webURL = fmt.Sprintf("http://localhost:%d", port)
	quitCh = make(chan struct{})
	systray.Run(onReady, onExit)
}

func onReady() {
	systray.SetIcon(icon)
	systray.SetTitle("微信机器人")
	systray.SetTooltip("微信机器人消息服务")

	statusItem = systray.AddMenuItem("状态: 未连接", "")
	statusItem.Disable()
	systray.AddSeparator()

	openItem = systray.AddMenuItem("打开管理页面", "")
	quitItem = systray.AddMenuItem("退出服务", "")

	// Periodically update connection status.
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if wc != nil && wc.Status() == wechat.StatusConnected {
					statusItem.SetTitle("状态: 已连接")
					systray.SetTooltip("微信机器人消息服务 - 已连接")
				} else {
					statusItem.SetTitle("状态: 未连接")
					systray.SetTooltip("微信机器人消息服务 - 未连接")
				}
			case <-quitCh:
				return
			}
		}
	}()

	// Handle menu clicks.
	go func() {
		for {
			select {
			case <-openItem.ClickedCh:
				exec.Command("open", webURL).Start()
			case <-quitItem.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func onExit() {
	close(quitCh)
}
