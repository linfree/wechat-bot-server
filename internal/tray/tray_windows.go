//go:build windows

package tray

import (
	"fmt"
	"time"

	"github.com/getlantern/systray"
	"github.com/pkg/browser"

	"wechat-bot-server/internal/wechat"
)

var (
	icon       []byte
	statusItem *systray.MenuItem
	openItem   *systray.MenuItem
	quitItem   *systray.MenuItem
	webURL     string
)

// Run starts the system tray (blocking). Windows only.
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

	go func() {
		for {
			select {
			case <-openItem.ClickedCh:
				browser.OpenURL(webURL)
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
