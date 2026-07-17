package wechat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const DefaultBaseURL = "https://ilinkai.weixin.qq.com"
const DefaultCDNBaseURL = "https://novac2c.cdn.weixin.qq.com/c2c"

const (
	MediaTypeImage = 1
	MediaTypeVideo = 2
	MediaTypeFile  = 3
)

type Status string

const (
	StatusDisconnected Status = "disconnected"
	StatusConnecting   Status = "connecting"
	StatusConnected    Status = "connected"
	StatusExpired      Status = "expired"
)

type Client struct {
	baseURL      string
	botToken     string
	loginTime    time.Time
	cdnBaseURL   string
	status       Status
	mu           sync.RWMutex
	httpClient   *http.Client
	cdnHttpClient *http.Client
	msgCh        chan Message
	getUpdatesBuf string
	lastContact  ContactInfo
	stopCh       chan struct{}
	done         chan struct{}
	reconnectStopCh chan struct{}
	pollCtx      context.Context
	pollCancel   context.CancelFunc
	qrPollMu       sync.Mutex
	qrPollCancel   context.CancelFunc
	qrPolling      bool
	pollRunning    bool
	reqCtx       context.Context
	reqCancel    context.CancelFunc

	// OnTokenSaved is called when token is updated (login or reconnect).
	// The caller should persist token + baseURL + loginTime to config.
	OnTokenSaved func(token, baseURL string, loginTime time.Time)
}

type ContactInfo struct {
	FromID       string
	ContextToken string
}

func NewClient(baseURL, botToken string, loginTime time.Time, cdnBaseURL string) *Client {
	if cdnBaseURL == "" {
		cdnBaseURL = DefaultCDNBaseURL
	}
	ctx, cancel := context.WithCancel(context.Background())
	reqCtx, reqCancel := context.WithCancel(context.Background())
	return &Client{
		baseURL:       baseURL,
		botToken:      botToken,
		loginTime:     loginTime,
		cdnBaseURL:    cdnBaseURL,
		status:        StatusDisconnected,
		httpClient:    &http.Client{Timeout: 60 * time.Second},
		cdnHttpClient: &http.Client{Timeout: 5 * time.Minute},
		msgCh:         make(chan Message, 100),
		stopCh:        make(chan struct{}),
		done:          make(chan struct{}),
		pollCtx:       ctx,
		pollCancel:    cancel,
		reqCtx:        reqCtx,
		reqCancel:     reqCancel,
	}
}

func (c *Client) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

func (c *Client) SetStatus(s Status) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status = s
}

func (c *Client) Token() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.botToken
}

func (c *Client) SetToken(token, baseURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.botToken = token
	if baseURL != "" {
		c.baseURL = baseURL
	}
	c.loginTime = time.Now()
}

// NotifyTokenSaved triggers the OnTokenSaved callback. Called externally
// after SetToken to avoid nested callback issues during reconnection.
func (c *Client) NotifyTokenSaved() {
	c.mu.RLock()
	token := c.botToken
	baseURL := c.baseURL
	loginTime := c.loginTime
	cb := c.OnTokenSaved
	c.mu.RUnlock()
	if cb != nil {
		cb(token, baseURL, loginTime)
	}
}

func (c *Client) LoginTime() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.loginTime
}

func (c *Client) Messages() <-chan Message { return c.msgCh }

func (c *Client) SetLastContact(ci ContactInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastContact = ci
}

func (c *Client) LastContact() ContactInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastContact
}

func (c *Client) makeHeaders() map[string]string {
	h := map[string]string{
		"Content-Type":      "application/json",
		"AuthorizationType": "ilink_bot_token",
	}
	uin := fmt.Sprintf("%d", rand.Uint32())
	h["X-WECHAT-UIN"] = base64.StdEncoding.EncodeToString([]byte(uin))
	if c.Token() != "" {
		h["Authorization"] = "Bearer " + c.Token()
	}
	return h
}

func (c *Client) doRequest(method, path string, bodyData []byte, ctx ...context.Context) (map[string]interface{}, error) {
	c.mu.RLock()
	base := c.baseURL
	c.mu.RUnlock()
	if base == "" {
		base = DefaultBaseURL
	}
	reqURL := base + "/" + path
	var body io.Reader
	if bodyData != nil {
		body = bytes.NewReader(bodyData)
	}
	// Use the provided context if any (e.g. pollCtx for long-polling),
	// otherwise use the long-lived request context that survives Stop().
	reqCtx := c.reqCtx
	if len(ctx) > 0 && ctx[0] != nil {
		reqCtx = ctx[0]
	}
	req, err := http.NewRequestWithContext(reqCtx, method, reqURL, body)
	if err != nil {
		return nil, err
	}
	for k, v := range c.makeHeaders() {
		req.Header.Set(k, v)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respData, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	if err := json.Unmarshal(respData, &result); err != nil {
		return nil, fmt.Errorf("invalid json response: %w", err)
	}
	return result, nil
}

func (c *Client) GetQRCode() (string, string, error) {
	result, err := c.doRequest("GET", "ilink/bot/get_bot_qrcode?bot_type=3", nil)
	if err != nil {
		return "", "", err
	}
	qrcode, _ := result["qrcode"].(string)
	qrcodeImg, _ := result["qrcode_img_content"].(string)
	return qrcode, qrcodeImg, nil
}

// StartQRPolling begins background polling for QR confirmation.
// If a poller is already running, it is cancelled and replaced by a new one
// for the given qrcodeID.
func (c *Client) StartQRPolling(qrcodeID string) {
	c.qrPollMu.Lock()
	// Cancel any in-flight poller (e.g. user refreshed the QR code).
	if c.qrPollCancel != nil {
		c.qrPollCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	c.qrPollCancel = cancel
	c.qrPolling = true
	c.qrPollMu.Unlock()

	go func() {
		defer func() {
			c.qrPollMu.Lock()
			c.qrPolling = false
			c.qrPollCancel = nil
			c.qrPollMu.Unlock()
			cancel()
		}()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			confirmed, token, baseURL, err := c.CheckQRCodeStatus(qrcodeID)
			if err != nil {
				continue
			}
			if confirmed {
				c.SetToken(token, baseURL)
				c.Start()
				c.NotifyTokenSaved()
				return
			}
		}
	}()
}

func (c *Client) CheckQRCodeStatus(qrcode string) (bool, string, string, error) {
	result, err := c.doRequest("GET", "ilink/bot/get_qrcode_status?qrcode="+qrcode, nil)
	if err != nil {
		return false, "", "", err
	}
	status, _ := result["status"].(string)
	if status == "confirmed" {
		token, _ := result["bot_token"].(string)
		baseURL, _ := result["baseurl"].(string)
		return true, token, baseURL, nil
	}
	return false, "", "", nil
}

// SendMessageItemList sends a message with a custom item_list.
func (c *Client) SendMessageItemList(toID, contextToken string, itemList []map[string]interface{}) error {
	clientID := fmt.Sprintf("wechat-bot-%08x", rand.Uint32())
	body, _ := json.Marshal(map[string]interface{}{
		"msg": map[string]interface{}{
			"from_user_id":  "",
			"to_user_id":    toID,
			"client_id":     clientID,
			"message_type":  2,
			"message_state": 2,
			"context_token": contextToken,
			"item_list":     itemList,
		},
		"base_info": map[string]string{"channel_version": "1.0.2"},
	})
	_, err := c.doRequest("POST", "ilink/bot/sendmessage", body)
	return err
}

func (c *Client) SendMessage(toID, contextToken, text string) error {
	return c.SendMessageItemList(toID, contextToken, []map[string]interface{}{
		{"type": 1, "text_item": map[string]interface{}{"text": text}},
	})
}

// --- Media upload methods ---

func (c *Client) GetUploadURL(fileKey string, mediaType int, toUserID string, rawSize int, rawFileMD5 string, fileSize int, aesKeyHex string) (string, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"filekey":       fileKey,
		"media_type":    mediaType,
		"to_user_id":    toUserID,
		"rawsize":       rawSize,
		"rawfilemd5":    rawFileMD5,
		"filesize":      fileSize,
		"no_need_thumb": true,
		"aeskey":        aesKeyHex,
		"base_info":     map[string]string{"channel_version": "1.0.2"},
	})
	result, err := c.doRequest("POST", "ilink/bot/getuploadurl", body)
	if err != nil {
		return "", fmt.Errorf("getuploadurl: %w", err)
	}
	uploadParam, _ := result["upload_param"].(string)
	if uploadParam == "" {
		return "", fmt.Errorf("getuploadurl returned no upload_param")
	}
	return uploadParam, nil
}

func (c *Client) UploadToCDN(ciphertext []byte, uploadParam, fileKey string) (string, error) {
	cdnURL := c.cdnBaseURL + "/upload?encrypted_query_param=" + url.QueryEscape(uploadParam) + "&filekey=" + url.QueryEscape(fileKey)
	req, err := http.NewRequest("POST", cdnURL, bytes.NewReader(ciphertext))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.cdnHttpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cdn upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		errMsg := resp.Header.Get("x-error-message")
		if errMsg == "" {
			errMsg = fmt.Sprintf("status %d", resp.StatusCode)
		}
		return "", fmt.Errorf("cdn upload failed: %s", errMsg)
	}
	downloadParam := resp.Header.Get("x-encrypted-param")
	if downloadParam == "" {
		return "", fmt.Errorf("cdn upload response missing x-encrypted-param header")
	}
	return downloadParam, nil
}

func (c *Client) UploadFile(filePath string, mediaType int, toUserID string) (*UploadResult, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	rawSize := len(data)
	rawFileMD5 := fileMD5(data)
	fileSize := computeCiphertextSize(rawSize)

	rawKey, aesKeyHex := generateAESKey()
	fileKey := generateFileKey()

	uploadParam, err := c.GetUploadURL(fileKey, mediaType, toUserID, rawSize, rawFileMD5, fileSize, aesKeyHex)
	if err != nil {
		return nil, err
	}

	ciphertext, err := aesEncryptECB(rawKey, data)
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}

	downloadParam, err := c.UploadToCDN(ciphertext, uploadParam, fileKey)
	if err != nil {
		return nil, err
	}

	return &UploadResult{
		DownloadParam:      downloadParam,
		AESKeyBase64:       aesKeyToBase64(aesKeyHex),
		AESKeyHex:          aesKeyHex,
		FileSize:           rawSize,
		FileSizeCiphertext: fileSize,
	}, nil
}

func makeCDNMedia(result *UploadResult) CDNMedia {
	return CDNMedia{
		EncryptQueryParam: result.DownloadParam,
		AESKey:            result.AESKeyBase64,
		EncryptType:       1,
	}
}

func (c *Client) SendImage(toID, contextToken, filePath string) error {
	result, err := c.UploadFile(filePath, MediaTypeImage, toID)
	if err != nil {
		return err
	}
	media := makeCDNMedia(result)
	return c.SendMessageItemList(toID, contextToken, []map[string]interface{}{
		{"type": 2, "image_item": map[string]interface{}{
			"media":    media,
			"mid_size": result.FileSizeCiphertext,
		}},
	})
}

func (c *Client) SendFile(toID, contextToken, filePath string) error {
	result, err := c.UploadFile(filePath, MediaTypeFile, toID)
	if err != nil {
		return err
	}
	media := makeCDNMedia(result)
	fileName := filepath.Base(filePath)
	return c.SendMessageItemList(toID, contextToken, []map[string]interface{}{
		{"type": 4, "file_item": map[string]interface{}{
			"media":     media,
			"file_name": fileName,
			"len":       fmt.Sprintf("%d", result.FileSize),
		}},
	})
}

func (c *Client) SendVideo(toID, contextToken, filePath string) error {
	result, err := c.UploadFile(filePath, MediaTypeVideo, toID)
	if err != nil {
		return err
	}
	media := makeCDNMedia(result)
	return c.SendMessageItemList(toID, contextToken, []map[string]interface{}{
		{"type": 5, "video_item": map[string]interface{}{
			"media":      media,
			"video_size": result.FileSizeCiphertext,
		}},
	})
}

func (c *Client) PollMessages() ([]Message, string, error) {
	c.mu.RLock()
	buf := c.getUpdatesBuf
	c.mu.RUnlock()
	body, _ := json.Marshal(map[string]interface{}{
		"get_updates_buf": buf,
		"base_info":       map[string]string{"channel_version": "1.0.2"},
	})
	result, err := c.doRequest("POST", "ilink/bot/getupdates", body, c.pollCtx)
	if err != nil {
		return nil, "", err
	}
	newBuf, _ := result["get_updates_buf"].(string)
	rawMsgs, _ := result["msgs"].([]interface{})
	var msgs []Message
	for _, raw := range rawMsgs {
		rm, _ := raw.(map[string]interface{})
		msg := parseMessage(rm)
		msgs = append(msgs, msg)
	}
	return msgs, newBuf, nil
}

func (c *Client) Start() {
	c.mu.Lock()
	// Signal the currently running pollLoop (if any) to stop.
	if c.pollRunning && c.stopCh != nil {
		select {
		case <-c.stopCh:
		default:
			close(c.stopCh)
		}
	}
	c.pollCancel()
	prevDone := c.done
	wasRunning := c.pollRunning
	c.mu.Unlock()

	// Wait for previous pollLoop to exit (best-effort).
	if wasRunning && prevDone != nil {
		select {
		case <-prevDone:
		case <-time.After(5 * time.Second):
			log.Println("[wechat] WARNING: previous poll loop did not exit within 5s")
		}
	}
	c.httpClient.CloseIdleConnections()

	// Fresh state for the new pollLoop.
	c.mu.Lock()
	c.stopCh = make(chan struct{})
	c.done = make(chan struct{})
	c.pollCtx, c.pollCancel = context.WithCancel(context.Background())
	c.pollRunning = true
	c.mu.Unlock()

	c.SetStatus(StatusConnected)
	go c.pollLoop()
}

func (c *Client) Stop() {
	c.mu.Lock()
	if c.stopCh != nil {
		select {
		case <-c.stopCh:
		default:
			close(c.stopCh)
		}
	}
	c.pollCancel()
	prevDone := c.done
	c.mu.Unlock()

	// Cancel in-flight HTTP requests (unblocks poll loop).
	c.httpClient.CloseIdleConnections()
	if prevDone != nil {
		select {
		case <-prevDone:
			log.Println("[wechat] poll loop exited cleanly")
		case <-time.After(5 * time.Second):
			log.Println("[wechat] WARNING: poll loop did not exit within 5s, forcing shutdown")
		}
	}
	c.SetStatus(StatusDisconnected)
}

func (c *Client) pollLoop() {
	defer func() {
		close(c.done)
		c.mu.Lock()
		c.pollRunning = false
		c.mu.Unlock()
	}()
	for {
		select {
		case <-c.stopCh:
			return
		default:
		}
		if c.Status() != StatusConnected {
			select {
			case <-c.stopCh:
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		msgs, newBuf, err := c.PollMessages()
		if err != nil {
			// Exit silently if we're stopping (context cancelled).
			select {
			case <-c.stopCh:
				return
			default:
			}
			log.Printf("[wechat] poll error: %v", err)
			// Don't change status here — let reconnect timer handle token
			// expiry. Transient network errors will self-heal.
			select {
			case <-c.stopCh:
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		if newBuf != "" {
			c.mu.Lock()
			c.getUpdatesBuf = newBuf
			c.mu.Unlock()
		}
		for _, msg := range msgs {
			if msg.MessageType != 1 {
				continue
			}
			contact := ContactInfo{FromID: msg.FromUserID, ContextToken: msg.ContextToken}
			c.mu.Lock()
			c.lastContact = contact
			c.mu.Unlock()
			select {
			case c.msgCh <- msg:
			case <-c.stopCh:
				return
			}
		}
	}
}

func ParseLoginTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t
}
