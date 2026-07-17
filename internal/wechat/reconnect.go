package wechat

import (
	"fmt"
	"log"
	"time"
)

type ReconnectConfig struct {
	SessionDuration           time.Duration
	ActivationWarningHours    int
	ActivationReminderMinutes int
	ForceBefore               time.Duration
}

var DefaultReconnectConfig = ReconnectConfig{
	SessionDuration:           24 * time.Hour,
	ActivationWarningHours:    20,
	ActivationReminderMinutes: 60,
	ForceBefore:               30 * time.Minute,
}

func (c *Client) StartReconnectTimer(cfg ReconnectConfig) {
	// Stop any existing reconnect goroutine
	c.mu.Lock()
	if c.reconnectStopCh != nil {
		select {
		case <-c.reconnectStopCh:
		default:
			close(c.reconnectStopCh)
		}
	}
	c.reconnectStopCh = make(chan struct{})
	reconStopCh := c.reconnectStopCh
	// Capture stopCh under lock for the goroutine — Start() may replace c.stopCh.
	mainStopCh := c.stopCh
	c.mu.Unlock()

	go func() {
		var qrcode string
		var lastReminder time.Time
		var qrPollStop chan struct{}
		activationStarted := false

		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-mainStopCh:
				stopQRPoll(qrPollStop)
				return
			case <-reconStopCh:
				stopQRPoll(qrPollStop)
				return
			case <-ticker.C:
			}

			if c.Status() != StatusConnected {
				continue
			}

			loginTime := c.LoginTime()
			elapsed := time.Since(loginTime)
			remaining := loginTime.Add(cfg.SessionDuration).Sub(time.Now())

			if remaining <= cfg.ForceBefore {
				log.Println("[reconnect] forcing reconnect, session expired")
				c.SetStatus(StatusExpired)
				stopQRPoll(qrPollStop)
				qrPollStop = nil
				activationStarted = false
				continue
			}

			if elapsed >= time.Duration(cfg.ActivationWarningHours)*time.Hour {
				if !activationStarted {
					activationStarted = true
					var err error
					qrcode, _, err = c.GetQRCode()
					if err != nil {
						log.Printf("[reconnect] failed to get qrcode: %v", err)
						activationStarted = false
						continue
					}
					lastReminder = time.Now()
					c.sendActivationReminder(qrcode)
					qrPollStop = make(chan struct{})
					go c.pollQRCodeConfirmation(&qrcode, qrPollStop, reconStopCh, mainStopCh)
				} else if time.Since(lastReminder) >= time.Duration(cfg.ActivationReminderMinutes)*time.Minute {
					lastReminder = time.Now()
					c.sendActivationReminder(qrcode)
				}
			}
		}
	}()
}

func (c *Client) sendActivationReminder(qrcode string) {
	ct := c.LastContact()
	if ct.FromID == "" {
		log.Println("[reconnect] no last contact, cannot send reminder")
		return
	}
	text := fmt.Sprintf(
		"### 登录提醒\n\n[重新点击激活机器人](https://liteapp.weixin.qq.com/q/7GiQu1?qrcode=%s&bot_type=3)",
		qrcode,
	)
	if err := c.SendMessage(ct.FromID, ct.ContextToken, text); err != nil {
		log.Printf("[reconnect] failed to send activation reminder: %v", err)
	}
}

func (c *Client) pollQRCodeConfirmation(qrcode *string, pollStop, reconnectStop, mainStop chan struct{}) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-pollStop:
			return
		case <-reconnectStop:
			return
		case <-mainStop:
			return
		case <-ticker.C:
		}

		confirmed, token, baseURL, err := c.CheckQRCodeStatus(*qrcode)
		if err != nil {
			continue
		}
		if confirmed {
			c.SetToken(token, baseURL)
			c.Start() // clean restart: reset poll state, new pollLoop
			c.NotifyTokenSaved()
			log.Println("[reconnect] qrcode confirmed, token updated")
			return
		}
	}
}

func (c *Client) TriggerRelogin() error {
	qrcode, _, err := c.GetQRCode()
	if err != nil {
		return err
	}
	c.sendActivationReminder(qrcode)
	pollStop := make(chan struct{})
	time.AfterFunc(10*time.Minute, func() { close(pollStop) })
	c.mu.RLock()
	mainStopCh := c.stopCh
	c.mu.RUnlock()
	go c.pollQRCodeConfirmation(&qrcode, pollStop, nil, mainStopCh)
	return nil
}

func stopQRPoll(ch chan struct{}) {
	if ch != nil {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}
