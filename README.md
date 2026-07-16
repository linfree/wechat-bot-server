# wechat-bot-server

独立微信机器人消息服务。基于微信 iLink Bot 协议实现长轮询 + 消息预算机制，封装为独立 Go 服务，配合 Claude Code Skill 实现微信消息收发。

## 架构总览

```
┌─────────────────────────────────────────────────────┐
│                    main.go                           │
│  ┌──────────┐  ┌──────────┐  ┌───────────────────┐  │
│  │  Config   │  │  Wechat  │  │  Message Pipeline  │  │
│  │  Manager  │  │  Client  │  │  (budget + queue)  │  │
│  └──────────┘  └──────────┘  └───────────────────┘  │
│                                                      │
│  ┌──────────────────────────────────────────────┐    │
│  │              Gin HTTP Server                  │    │
│  │  ┌─────────────┐  ┌──────────────────────┐   │    │
│  │  │ 管理 API     │  │ 消息 API              │   │    │
│  │  │ /wechat/*    │  │ /wechat-bot/*         │   │    │
│  │  └─────────────┘  └──────────────────────┘   │    │
│  │  ┌──────────────────────────────────────┐    │    │
│  │  │  管理页面 (go:embed HTML/CSS/JS)      │    │    │
│  │  └──────────────────────────────────────┘    │    │
│  └──────────────────────────────────────────────┘    │
│                                                      │
│  ┌──────────────────────────────────────────────┐    │
│  │              Tray (平台特定)                   │    │
│  │  Windows: systray 托盘图标                    │    │
│  │  Linux/macOS: 信号阻塞 stub                   │    │
│  └──────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────┘
```

## 核心流程

### 1. 登录流程

```
浏览器点击"刷新二维码"
  → GET /api/v1/wechat/qrcode
  → wechat.GetQRCode() → 微信 API 返回二维码
  → StartQRPolling(qrcodeID) → 后台单例轮询（每2秒，最长10分钟）
  → 返回二维码给前端展示

用户微信扫码
  → 后台轮询 CheckQRCodeStatus 检测到 confirmed
  → SetToken(token, baseURL)      ← 更新 token（锁外回调 OnTokenSaved）
  → Start()                        ← 启动消息轮询 pollLoop
  → OnTokenSaved 回调:
     ├─ cfgMgr.UpdateWechat()     ← 持久化到 config.json
     ├─ SetStatus(Connected)       ← 状态 → connected
     ├─ StartReconnectTimer()      ← 启动 24h 续期监控
     └─ SendMessage("机器人已连接") ← 微信通知用户
  → 前端 loadStatus 每5秒轮询检测到 connected=true，切换显示
```

### 2. 断开流程

```
浏览器点击"断开连接"
  → POST /api/v1/wechat/disconnect
  → SendMessage("机器人已断开连接")  ← 通知微信用户
  → wechat.Stop()
     ├─ close(stopCh)              ← 信号 pollLoop 退出
     ├─ pollCancel()               ← 中断长轮询 HTTP 请求
     ├─ 等待 pollLoop 的 done channel（最多5秒）
     └─ SetStatus(Disconnected)
  → 前端检测到 connected=false，切换回二维码面板
```

### 3. 消息预算 + 缓冲机制

```
                用户发消息
                    │
                    ▼
            OnUserMessage(msg)
         ┌──────┴──────┐
         │ 重置预算 = 7  │
         └──────┬──────┘
                │
        msg == "/" (激活命令)?
        ┌───────┴───────┐
        │ YES           │ NO
        ▼               ▼
   清空缓冲队列    有缓冲消息待发?
   (不进入收件箱)   ┌─────┴─────┐
                   │ YES       │ NO
                   ▼           ▼
              doFlush()    bufferMode
              (锁外发送)    = false
                               │
                               ▼
                          返回 true
                          (进入收件箱)

主动发消息:
  BudgetedSend(text)
    ├─ 预算 > 0 → 扣减预算，发送 → {"status":"sent"}
    └─ 预算 = 0 → 进入缓冲队列
                   ├─ 首次进入 → 发提醒"回复任意消息激活"
                   └─ 返回 {"status":"buffered"}
```

### 4. Token 续期流程

```
StartReconnectTimer(cfg)
  │
  ├─ 每 1 分钟检查一次
  │
  ├─ remaining ≤ 30min → SetStatus(Expired)，停止轮询
  │
  └─ elapsed ≥ 20h (可配置)
       └─ 获取新二维码
          └─ 发送激活提醒 → "### 登录提醒\n[重新点击激活机器人](url)"
             └─ 轮询二维码确认（每5秒）
                └─ 用户扫码 → SetToken → OnTokenSaved
                   ├─ 持久化新 token
                   ├─ SetStatus(Connected)
                   └─ 重新 StartReconnectTimer
          └─ 每 60 分钟重复提醒（可配置）
```

### 5. 连续扫码（刷新二维码）

```
StartQRPolling(newQR)
  │
  ├─ qrPollCancel() ← 取消旧轮询 goroutine
  ├─ 创建新 context (10min timeout)
  └─ 启动新轮询 goroutine
       └─ 每2秒 CheckQRCodeStatus(newQR)
```

## 死锁问题排查与修复

### 问题现象

扫码登录后服务卡死，所有 HTTP API 无响应。

### 根因

`SetToken()` 持有 `c.mu.Lock()` 时同步调用 `OnTokenSaved` 回调：

```go
// 修复前 — 死锁
func (c *Client) SetToken(token, baseURL string) {
    c.mu.Lock()
    defer c.mu.Unlock()    // ← 锁全程持有
    // ...
    if c.OnTokenSaved != nil {
        c.OnTokenSaved(...) // ← 回调里调 SetStatus → c.mu.Lock() → 死锁!
    }
}
```

调用链：`SetToken(Lock) → OnTokenSaved → SetStatus(Lock) → 死锁`

Go 的 `sync.Mutex` 不可重入，同一 goroutine 对同一锁连续 `Lock()` 两次直接永久阻塞，**且不会 panic**（难以排查）。

### 修复

回调移到锁外执行：

```go
// 修复后
func (c *Client) SetToken(token, baseURL string) {
    c.mu.Lock()
    c.botToken = token
    // ... 修改字段 ...
    cb := c.OnTokenSaved    // ← 捕获回调
    c.mu.Unlock()           // ← 立即释放锁

    if cb != nil {
        cb(token, baseURL, c.loginTime) // ← 锁外调用，安全
    }
}
```

### 其他并发修复

| 问题 | 修复 |
|------|------|
| `flushBufferLocked` 持 budget 锁发 HTTP → status API 阻塞 | `collectFlushLocked` 锁内收集数据 + `doFlush` 锁外发送 |
| `doRequest` 共用一个 `pollCtx`，Stop 后所有 API 请求 `context canceled` | 拆为 `pollCtx`（仅 pollLoop）+ `reqCtx`（普通请求，始终存活） |
| `time.Sleep` 在 pollLoop 中使 Stop 退出慢 | 改用 `select { case stopCh; case time.After }` |
| 首次启动 `Start()` 白等 5s（无旧 pollLoop） | 加 `pollRunning` 标记判断是否有在跑轮询 |

## 项目结构

```
cmd/wechat-bot-server/
  main.go                     — 服务入口，组装依赖
  logo.ico                    — EXE 图标（go:embed）
  rsrc_windows_amd64.syso     — Windows 资源文件
internal/
  budget/manager.go           — 消息预算计数 + 缓冲队列
  config/config.go            — JSON 配置持久化
  queue/queue.go              — 接收消息队列（内存）
  server/
    api/wechat.go             — 管理 API
    api/wechat_bot.go         — 消息 API
    embed.go                  — go:embed 前端资源
    router.go                 — Gin 路由
    web/index.html            — 管理页面
    web/logo.png              — 页面 Logo
  tray/
    tray.go                   — 共享类型
    tray_windows.go           — Windows systray 托盘
    tray_nix.go               — Linux/macOS stub
  wechat/
    client.go                 — 微信客户端（轮询/登录/发送）
    cdn.go                    — CDN 上传（AES 加密）
    message.go                — 消息解析
    reconnect.go              — Token 续期 + 登录提醒
skills/wechat-bot.md          — Claude Code Skill
```

## 配置

配置文件：`~/.wechat-bot-server/config.json`

```json
{
  "web_port": 18081,
  "wechat": {
    "bot_token": "",
    "base_url": "https://ilinkai.weixin.qq.com",
    "cdn_base_url": "https://novac2c.cdn.weixin.qq.com/c2c",
    "login_time": "",
    "last_from_id": "",
    "last_context_token": ""
  },
  "budget": {
    "send_budget_limit": 7,
    "max_buffered_messages": 100
  },
  "reconnect": {
    "activation_warning_hours": 20,
    "activation_reminder_minutes": 60
  },
  "incoming_queue_max": 50
}
```

## 构建

```bash
# 本地构建 (Windows GUI，无控制台)
go build -ldflags="-H windowsgui -s -w" -o wechat-bot-server.exe ./cmd/wechat-bot-server/

# UPX 压缩 (10MB → 3.7MB)
upx --best wechat-bot-server.exe

# 多平台交叉编译
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o wechat-bot-server-linux ./cmd/wechat-bot-server/
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o wechat-bot-server-darwin ./cmd/wechat-bot-server/
```

## API 文档

详见管理页面内置文档（浏览器打开 `http://localhost:18081`），或参考 `skills/wechat-bot.md`。

### 管理 API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/wechat/qrcode` | 获取登录二维码，启动后台轮询 |
| GET | `/api/v1/wechat/status` | 连接状态 + 预算 + Token 剩余时间 |
| POST | `/api/v1/wechat/disconnect` | 断开连接（发告别消息后停轮询） |
| PUT | `/api/v1/wechat/settings` | 更新运行参数 |

### 消息 API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/wechat-bot/send/text` | 发送文本 `{"text":"..."}` |
| POST | `/api/v1/wechat-bot/send/image` | 发送图片 `{"file_path":"..."}` |
| POST | `/api/v1/wechat-bot/send/file` | 发送文件 |
| POST | `/api/v1/wechat-bot/send/video` | 发送视频 |
| GET | `/api/v1/wechat-bot/messages` | 拉取收到的消息 |
| DELETE | `/api/v1/wechat-bot/messages` | 清空消息队列 |

### 返回值

- `{"status":"sent"}` — 消息已发送
- `{"status":"buffered"}` — 预算耗尽，消息已排队
- `{"status":"disconnected"}` — 已断开
- `{"error":"..."}` — 错误

## 端口选择策略

1. 优先使用 `18081`（默认端口）
2. 若被占用，尝试 `config.json` 中记录的端口
3. 仍被占用则递增尝试（18082, 18083...）
4. 实际使用端口写入 `config.json`
