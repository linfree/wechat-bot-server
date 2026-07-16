package tray

import "wechat-bot-server/internal/wechat"

var (
	quitCh chan struct{}
	wc     *wechat.Client
)

// QuitCh returns a channel that is closed when the user requests to quit.
func QuitCh() <-chan struct{} {
	return quitCh
}
