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
	// Stop any existing reconnect goroutine (align with cc-go).
	c.mu.Lock()
	if c.reconnectStopCh != nil {
		select {
		case <-c.reconnectStopCh:
		default:
			close(c.reconnectStopCh)
		}
	}
	c.reconnectStopCh = make(chan struct{})
	stopCh := c.reconnectStopCh
	c.mu.Unlock()

	go func() {
		log.Printf("[reconnect] timer started, warning=%dh reminder=%dm", cfg.ActivationWarningHours, cfg.ActivationReminderMinutes)
		var qrcode string
		var lastReminder time.Time
		var qrPollStop chan struct{}
		var qrResultCh chan bool
		activationStarted := false

		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-c.stopCh:
				// Align with cc-go: read c.stopCh directly each iteration
				log.Println("[reconnect] timer stopped via stopCh")
				stopQRPoll(qrPollStop)
				return
			case <-stopCh:
				log.Println("[reconnect] timer stopped via reconStopCh")
				stopQRPoll(qrPollStop)
				return
			case confirmed := <-qrResultCh:
				if confirmed {
					log.Println("[reconnect] activation confirmed, session renewed")
				} else {
					log.Println("[reconnect] activation QR expired, regenerating...")
				}
				stopQRPoll(qrPollStop)
				qrPollStop = nil
				qrResultCh = nil
				activationStarted = false
				continue
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
				qrResultCh = nil
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
					log.Printf("[reconnect] sending activation reminder, qrcode=%.16s...", qrcode)
					c.sendActivationReminder(qrcode)
					qrPollStop = make(chan struct{})
					qrResultCh = make(chan bool, 1)
					go c.pollQRCodeConfirmation(&qrcode, qrPollStop, stopCh, qrResultCh)
				} else if time.Since(lastReminder) >= time.Duration(cfg.ActivationReminderMinutes)*time.Minute {
					lastReminder = time.Now()
					log.Println("[reconnect] resending activation reminder")
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

// pollQRCodeConfirmation polls the QR code status. Aligns with cc-go: reads
// c.stopCh directly each iteration, calls NotifyTokenSaved after SetToken+Start.
func (c *Client) pollQRCodeConfirmation(qrcode *string, pollStop, reconnectStop chan struct{}, resultCh chan<- bool) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	sendResult := func(confirmed bool) {
		select {
		case resultCh <- confirmed:
		default:
		}
	}

	for {
		select {
		case <-pollStop:
			sendResult(false)
			return
		case <-reconnectStop:
			sendResult(false)
			return
		case <-c.stopCh:
			// Align with cc-go: read c.stopCh directly
			sendResult(false)
			return
		case <-ticker.C:
		}

		confirmed, expired, token, baseURL, err := c.CheckQRCodeStatus(*qrcode)
		if err != nil {
			continue
		}
		if expired {
			log.Printf("[reconnect] qrcode expired: %.16s...", *qrcode)
			sendResult(false)
			return
		}
		if confirmed {
			c.SetToken(token, baseURL)
			c.Start()
			c.NotifyTokenSaved()
			log.Println("[reconnect] qrcode confirmed, token updated")
			sendResult(true)
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
	resultCh := make(chan bool, 1)
	go c.pollQRCodeConfirmation(&qrcode, pollStop, nil, resultCh)
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
