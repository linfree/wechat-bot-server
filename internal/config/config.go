package config

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// AppConfig is the top-level configuration structure.
type AppConfig struct {
	WebPort          int              `json:"web_port"`
	Wechat           WechatConfig     `json:"wechat"`
	Budget           BudgetConfig     `json:"budget"`
	Reconnect        ReconnectConfig  `json:"reconnect"`
	IncomingQueueMax int              `json:"incoming_queue_max"`
}

// WechatConfig holds the WeChat iLink Bot connection configuration.
type WechatConfig struct {
	BotToken         string `json:"bot_token"`
	BaseURL          string `json:"base_url"`
	CDNBaseURL       string `json:"cdn_base_url"`
	LoginTime        string `json:"login_time"`
	LastFromID       string `json:"last_from_id"`
	LastContextToken string `json:"last_context_token"`
}

// BudgetConfig holds the message budget and buffer configuration.
type BudgetConfig struct {
	SendBudgetLimit     int `json:"send_budget_limit"`
	MaxBufferedMessages int `json:"max_buffered_messages"`
}

// ReconnectConfig holds the reconnect timer configuration.
type ReconnectConfig struct {
	ActivationWarningHours   int `json:"activation_warning_hours"`
	ActivationReminderMinutes int `json:"activation_reminder_minutes"`
}

// Default values for configuration.
const (
	DefaultWebPort          = 18081
	DefaultBaseURL          = "https://ilinkai.weixin.qq.com"
	DefaultCDNBaseURL       = "https://novac2c.cdn.weixin.qq.com/c2c"
	DefaultSendBudgetLimit  = 7
	DefaultMaxBuffered      = 100
	DefaultActivationWarningHours = 20
	DefaultActivationReminderMin  = 60
	DefaultIncomingQueueMax = 50

	configDirName  = ".wechat-bot-server"
	configFileName = "config.json"
)

// Manager holds the in-memory configuration and provides thread-safe access.
type Manager struct {
	mu     sync.Mutex
	config *AppConfig
	path   string
}

// NewManager creates a new Manager and loads the configuration.
func NewManager() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	dir := filepath.Join(home, configDirName)
	path := filepath.Join(dir, configFileName)
	m := &Manager{
		config: defaultConfig(),
		path:   path,
	}
	if err := m.load(); err != nil {
		log.Printf("[config] load failed, using defaults: %v", err)
	}
	return m, nil
}

// Get returns a copy of the current configuration.
func (m *Manager) Get() AppConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	return *m.config
}

// ConfigPath returns the full path to the configuration file.
func (m *Manager) ConfigPath() string {
	return m.path
}

// UpdateWechat updates the WeChat connection configuration and persists.
func (m *Manager) UpdateWechat(botToken, baseURL, cdnBaseURL, loginTime string) error {
	m.mu.Lock()
	m.config.Wechat.BotToken = botToken
	if baseURL != "" {
		m.config.Wechat.BaseURL = baseURL
	}
	if cdnBaseURL != "" {
		m.config.Wechat.CDNBaseURL = cdnBaseURL
	}
	m.config.Wechat.LoginTime = loginTime
	m.mu.Unlock()
	return m.Save()
}

// UpdateLastContact updates the last contact info and persists.
func (m *Manager) UpdateLastContact(fromID, contextToken string) error {
	m.mu.Lock()
	m.config.Wechat.LastFromID = fromID
	m.config.Wechat.LastContextToken = contextToken
	m.mu.Unlock()
	return m.Save()
}

// UpdateWebPort updates the web port and persists.
func (m *Manager) UpdateWebPort(port int) error {
	m.mu.Lock()
	m.config.WebPort = port
	m.mu.Unlock()
	return m.Save()
}

// UpdateSettings updates the runtime parameters and persists.
func (m *Manager) UpdateSettings(budgetLimit, maxBuffer, warningHours, reminderMin int) error {
	m.mu.Lock()
	m.config.Budget.SendBudgetLimit = budgetLimit
	m.config.Budget.MaxBufferedMessages = maxBuffer
	m.config.Reconnect.ActivationWarningHours = warningHours
	m.config.Reconnect.ActivationReminderMinutes = reminderMin
	m.mu.Unlock()
	return m.Save()
}

// Save atomically writes the current configuration to disk.
func (m *Manager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return atomicWriteJSON(m.path, m.config)
}

// load reads the configuration file, or creates one with defaults if missing.
func (m *Manager) load() error {
	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := os.ReadFile(m.path)
	if os.IsNotExist(err) {
		return atomicWriteJSON(m.path, m.config)
	}
	if err != nil {
		return err
	}

	cfg := defaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		log.Printf("[config] WARNING: corrupted config file, repairing with defaults: %v", err)
		m.config = cfg
		_ = atomicWriteJSON(m.path, m.config)
		return nil
	}

	m.config = cfg
	return nil
}

// defaultConfig returns a fresh AppConfig with all defaults filled.
func defaultConfig() *AppConfig {
	return &AppConfig{
		WebPort: DefaultWebPort,
		Wechat: WechatConfig{
			BotToken:   "",
			BaseURL:    DefaultBaseURL,
			CDNBaseURL: DefaultCDNBaseURL,
			LoginTime:  "",
		},
		Budget: BudgetConfig{
			SendBudgetLimit:     DefaultSendBudgetLimit,
			MaxBufferedMessages: DefaultMaxBuffered,
		},
		Reconnect: ReconnectConfig{
			ActivationWarningHours:   DefaultActivationWarningHours,
			ActivationReminderMinutes: DefaultActivationReminderMin,
		},
		IncomingQueueMax: DefaultIncomingQueueMax,
	}
}

// atomicWriteJSON writes the config to a temporary file first, then atomically renames.
func atomicWriteJSON(path string, cfg *AppConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		log.Printf("[config] ERROR: marshal failed: %v", err)
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		log.Printf("[config] ERROR: write failed (disk full?): %v", err)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		log.Printf("[config] ERROR: rename failed: %v", err)
		return err
	}
	return nil
}
