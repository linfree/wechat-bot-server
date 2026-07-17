---
name: wechat-bot
description: 通过微信机器人发送和接收消息。自动检测服务状态，未安装时引导下载。
data_flow:
  classification: LOCAL_ONLY
priority: medium
token_estimate: 800
applicable_conditions:
  - 需要通过微信发送消息通知
  - 需要查看微信机器人连接状态
  - 需要查看收到的微信消息
negative_triggers:
  - 与微信消息收发无关的操作
  - 不需要通知用户的纯后台任务
failure_modes:
  - F1: 服务未运行 — 引导用户按平台下载对应包并启动
  - F2: 服务未登录 — 引导用户在浏览器打开管理页面扫码
  - F3: 消息预算耗尽 — 告知用户消息已排队等待激活
  - F4: 端口变化 — 必须从 config.json 读取实际端口
output_budget: 800
allowed-tools: Bash(curl *)
---

# wechat-bot

通过本地微信机器人服务收发微信消息。

## Problem

用户需要通过 Claude Code 在微信上发送通知消息、查看机器人连接状态、以及接收微信消息。服务是本地部署的独立 Go 程序，可能未安装、未运行、或未登录，Skill 需要自动处理这些状态。

## Approach

### 1. 检测服务状态（每次调用必须先执行）

```bash
# 从配置文件读取实际端口（端口非固定，默认 18081，被占用会自动递增）
PORT=$(grep -o '"web_port":[[:space:]]*[0-9]*' ~/.wechat-bot-server/config.json 2>/dev/null | grep -o '[0-9]*')
if [ -z "$PORT" ]; then PORT=18081; fi

# 检查服务是否在运行
STATUS=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 2 http://localhost:$PORT/api/v1/wechat/status 2>/dev/null)
```

### 2. 分支处理

**服务未运行**（curl 失败或 STATUS 非 200），告知用户下载安装：

```
微信机器人服务未运行。请按以下步骤操作：

1. 下载对应平台的程序：
   • Windows: https://github.com/linfree/wechat-bot-server/releases/latest/download/wechat-bot-server-windows-amd64.exe
   • macOS Intel: https://github.com/linfree/wechat-bot-server/releases/latest/download/wechat-bot-server-darwin-amd64
   • macOS Apple Silicon: https://github.com/linfree/wechat-bot-server/releases/latest/download/wechat-bot-server-darwin-arm64
   • Linux: https://github.com/linfree/wechat-bot-server/releases/latest/download/wechat-bot-server-linux-amd64

2. macOS 用户额外执行:
   xattr -cr wechat-bot-server-darwin-* && chmod +x wechat-bot-server-darwin-*

3. 运行程序，浏览器打开 http://localhost:18081 扫码登录

4. 登录成功后告诉我，我来继续操作。
```

**服务运行但未登录**（connected 为 false），告知用户扫码：

```
微信机器人服务已运行但未登录。请在浏览器打开 http://localhost:$PORT 扫描二维码登录。登录成功后告诉我。
```

**服务运行且已登录**（connected 为 true），继续执行消息收发。

### 3. 发送文本

中文内容需用临时文件传递，避免 shell 编码损坏：

```bash
TMPFILE="${TMPDIR:-${TEMP:-${TMP:-/tmp}}}/wb_msg.json"
echo '{"text":"消息内容"}' > "$TMPFILE"
curl -s -X POST http://localhost:$PORT/api/v1/wechat-bot/send/text \
  -H "Content-Type: application/json" --data-binary @"$TMPFILE"
```

### 4. 发送媒体

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

### 5. 查询接口

```bash
# 服务状态（连接状态 + 预算剩余 + Token 有效期）
curl -s http://localhost:$PORT/api/v1/wechat/status

# 收到的消息
curl -s http://localhost:$PORT/api/v1/wechat-bot/messages
```

## Rules

1. **每次调用必须先执行步骤 1**（检测服务状态），然后根据结果走对应分支，不得跳过
2. **端口以 config.json 为准**：服务端口非固定（默认 18081，被占用自动递增），必须以 `~/.wechat-bot-server/config.json` 中的 `web_port` 字段为准
3. **发送中文必须用文件方式**：`--data-binary @文件`，禁止直接在命令行拼接中文字符串
4. **返回 buffered 状态时告知用户**：消息已排队，本轮额度用完，用户回复任意消息后会自动补发
5. **文件路径使用操作系统原生格式**：Windows 用 `C:\path\to\file`，macOS/Linux 用 `/path/to/file`
6. **发送失败时展示错误信息**：从返回的 `error` 字段提取，不要静默失败

## Exit Criteria

- 服务未运行 → 已告知用户完整的下载、安装、启动、登录步骤，等待用户确认
- 服务未登录 → 已告知用户管理页面地址和扫码操作，等待用户确认
- 发送成功 → 返回 `{"status":"sent"}`，告知用户消息已发送
- 发送排队 → 返回 `{"status":"buffered"}`，告知用户消息已排队等待激活
- 发送失败 → 返回 `{"error":"..."}` 错误信息，告知用户具体原因
