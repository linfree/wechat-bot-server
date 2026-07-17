---
name: wechat-bot
description: 通过微信机器人发送和接收消息。自动检测服务状态，未安装时自动下载。
data_flow:
  classification: LOCAL_ONLY
priority: medium
token_estimate: 1200
applicable_conditions:
  - 需要通过微信发送消息通知
  - 需要查看微信机器人连接状态
  - 需要查看收到的微信消息
negative_triggers:
  - 与微信消息收发无关的操作
  - 不需要通知用户的纯后台任务
failure_modes:
  - F1: 服务未安装 — 自动通过 gh-proxy.com 代理下载二进制到 ~/.wechat-bot-server/
  - F2: 服务未运行 — 自动启动已下载的二进制
  - F3: 服务未登录 — 告知用户在浏览器打开管理页面扫码
  - F4: 消息预算耗尽 — 告知用户消息已排队等待激活
output_budget: 1200
allowed-tools: Bash(curl *)
---

# wechat-bot

通过本地微信机器人服务收发微信消息。AI 自动管理服务的下载、启动和状态检测。

## Problem

用户需要通过 Claude Code 在微信上发送通知消息、查看机器人连接状态、以及接收微信消息。服务是独立 Go 程序，可能未安装、未运行、或未登录。AI 需要自动检测并处理这些状态，减少用户手动操作。

## Approach

### 1. 准备二进制文件（每次调用必须先执行）

服务统一目录 `~/.wechat-bot-server/`，按当前平台确定二进制文件名：

| 平台 | 二进制文件名 | 下载路径 |
|------|-------------|----------|
| Windows | `wechat-bot-server.exe` | `https://gh-proxy.com/https://github.com/linfree/wechat-bot-server/releases/latest/download/wechat-bot-server-windows-amd64.exe` |
| macOS Intel | `wechat-bot-server` | `https://gh-proxy.com/https://github.com/linfree/wechat-bot-server/releases/latest/download/wechat-bot-server-darwin-amd64` |
| macOS Apple Silicon | `wechat-bot-server` | `https://gh-proxy.com/https://github.com/linfree/wechat-bot-server/releases/latest/download/wechat-bot-server-darwin-arm64` |
| Linux | `wechat-bot-server` | `https://gh-proxy.com/https://github.com/linfree/wechat-bot-server/releases/latest/download/wechat-bot-server-linux-amd64` |

```bash
# 确定平台和路径
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*)  OS=windows; EXT=.exe ;;
  Darwin)                 OS=darwin; EXT="" ;;
  Linux)                  OS=linux; EXT="" ;;
  *)                      OS=unknown; EXT="" ;;
esac
BIN_DIR="$HOME/.wechat-bot-server"
BIN="$BIN_DIR/wechat-bot-server$EXT"
mkdir -p "$BIN_DIR"

# 检查二进制是否存在，不存在则自动下载
if [ ! -f "$BIN" ]; then
  ARCH="amd64"
  [ "$(uname -m)" = "arm64" ] && ARCH="arm64"
  GITHUB="https://github.com/linfree/wechat-bot-server/releases/latest/download/wechat-bot-server"
  case "$OS" in
    windows) URL="https://gh-proxy.com/${GITHUB}-windows-amd64.exe" ;;
    darwin)  URL="https://gh-proxy.com/${GITHUB}-darwin-${ARCH}" ;;
    linux)   URL="https://gh-proxy.com/${GITHUB}-linux-amd64" ;;
    *)       echo "unsupported OS"; exit 1 ;;
  esac
  curl -L --connect-timeout 30 --max-time 120 -o "$BIN" "$URL"
  if [ "$OS" != "windows" ]; then
    chmod +x "$BIN"
    xattr -cr "$BIN" 2>/dev/null || true
  fi
fi
```

### 2. 检测并启动服务

```bash
# 从配置文件读取实际端口
PORT=$(grep -o '"web_port":[[:space:]]*[0-9]*' "$BIN_DIR/config.json" 2>/dev/null | grep -o '[0-9]*')
if [ -z "$PORT" ]; then PORT=18081; fi

# 检查服务是否在运行
STATUS=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 2 http://localhost:$PORT/api/v1/wechat/status 2>/dev/null)

# 服务未运行则启动（后台运行）
if [ "$STATUS" != "200" ]; then
  # 先杀掉可能残留的旧进程
  pkill -f "wechat-bot-server" 2>/dev/null || true
  sleep 1
  "$BIN" &
  sleep 2
  # 重新检测端口（服务启动后可能切换了端口）
  PORT=$(grep -o '"web_port":[[:space:]]*[0-9]*' "$BIN_DIR/config.json" 2>/dev/null | grep -o '[0-9]*')
  if [ -z "$PORT" ]; then PORT=18081; fi
  STATUS=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 2 http://localhost:$PORT/api/v1/wechat/status 2>/dev/null)
fi
```

### 3. 分支处理

**服务检测连接状态**（STATUS=200 时获取详情）：

```bash
if [ "$STATUS" = "200" ]; then
  CONNECTED=$(curl -s http://localhost:$PORT/api/v1/wechat/status | grep -o '"connected":[[:space:]]*\(true\|false\)' | grep -o 'true\|false')
fi
```

- **`$CONNECTED` = "true"** → 已连接，继续执行消息收发
- **`$CONNECTED` = "false"** → 告知用户：
  > 微信机器人服务已启动但未登录。请在浏览器打开 http://localhost:$PORT 扫描二维码登录。登录成功后告诉我。
- **STATUS 仍然非 200** → 告知用户：
  > 微信机器人服务启动失败。请手动检查：`$BIN` 是否可执行，端口 `$PORT` 是否被占用。

### 4. 发送文本

中文内容需用临时文件传递，避免 shell 编码损坏：

```bash
TMPFILE="${TMPDIR:-${TEMP:-${TMP:-/tmp}}}/wb_msg.json"
echo '{"text":"消息内容"}' > "$TMPFILE"
curl -s -X POST http://localhost:$PORT/api/v1/wechat-bot/send/text \
  -H "Content-Type: application/json" --data-binary @"$TMPFILE"
```

### 5. 发送媒体

```bash
# 图片
echo '{"file_path":"图片路径"}' > "$TMPFILE"
curl -s -X POST http://localhost:$PORT/api/v1/wechat-bot/send/image \
  -H "Content-Type: application/json" --data-binary @"$TMPFILE"

# 文件
echo '{"file_path":"文件路径"}' > "$TMPFILE"
curl -s -X POST http://localhost:$PORT/api/v1/wechat-bot/send/file \
  -H "Content-Type: application/json" --data-binary @"$TMPFILE"

# 视频
echo '{"file_path":"视频路径"}' > "$TMPFILE"
curl -s -X POST http://localhost:$PORT/api/v1/wechat-bot/send/video \
  -H "Content-Type: application/json" --data-binary @"$TMPFILE"
```

### 6. 查询接口

```bash
# 服务状态（连接状态 + 预算剩余 + Token 有效期）
curl -s http://localhost:$PORT/api/v1/wechat/status

# 收到的消息
curl -s http://localhost:$PORT/api/v1/wechat-bot/messages
```

## Rules

1. **每次调用必须先执行步骤 1+2**（准备二进制 → 检测并启动服务），然后根据连接状态走分支
2. **二进制和配置统一在 `~/.wechat-bot-server/`**：下载到此处、检查此处、从此处启动
3. **下载必须通过 `https://gh-proxy.com/` 代理**：GitHub raw URL 拼在代理 URL 后面
4. **端口以 config.json 为准**：服务端口非固定（默认 18081，被占用自动递增），必须以 `~/.wechat-bot-server/config.json` 为准
5. **发送中文必须用文件方式**：`--data-binary @文件`，禁止直接在命令行拼接中文字符串
6. **返回 buffered 时告知用户**：消息已排队，本轮额度用完，用户回复任意消息后会自动补发
7. **文件路径使用操作系统原生格式**：Windows 用 `C:\path\to\file`，macOS/Linux 用 `/path/to/file`
8. **发送失败时展示错误信息**：从返回的 `error` 字段提取，不要静默失败

## Exit Criteria

- 二进制不存在 → 已通过 gh-proxy.com 自动下载到 `~/.wechat-bot-server/`，继续启动
- 服务未运行 → 已自动后台启动，继续检测连接状态
- 服务未登录 → 已告知用户管理页面地址和扫码操作，等待用户确认
- 发送成功 → 返回 `{"status":"sent"}`，告知用户消息已发送
- 发送排队 → 返回 `{"status":"buffered"}`，告知用户消息已排队等待激活
- 发送失败 → 返回 `{"error":"..."}` 错误信息，告知用户具体原因
