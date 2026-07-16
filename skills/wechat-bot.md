---
name: wechat-bot
description: 通过微信机器人发送和接收消息
allowed-tools: Bash(curl *)
---

通过本地微信机器人服务收发消息。

## 获取端口

端口配置在 `~/.wechat-bot-server/config.json` 的 `web_port` 字段中，发送请求前先读取：

```bash
PORT=$(grep -o '"web_port":[[:space:]]*[0-9]*' ~/.wechat-bot-server/config.json | grep -o '[0-9]*')
if [ -z "$PORT" ]; then PORT=18081; fi
# 跨平台临时目录
TMPFILE="${TMPDIR:-${TEMP:-${TMP:-/tmp}}}/wb_msg.json"
```

## 发送文本消息

注意：中文消息需用文件方式传递，避免 shell 编码损坏。

```bash
echo '{"text":"通知内容"}' > "$TMPFILE"
curl -s -X POST http://localhost:$PORT/api/v1/wechat-bot/send/text \
  -H "Content-Type: application/json" \
  --data-binary @"$TMPFILE"
```

## 发送图片

```bash
echo '{"file_path":"C:/path/to/image.png"}' > $TMPFILE
curl -s -X POST http://localhost:$PORT/api/v1/wechat-bot/send/image \
  -H "Content-Type: application/json" \
  --data-binary @$TMPFILE
```

## 发送文件

```bash
echo '{"file_path":"C:/path/to/document.pdf"}' > $TMPFILE
curl -s -X POST http://localhost:$PORT/api/v1/wechat-bot/send/file \
  -H "Content-Type: application/json" \
  --data-binary @$TMPFILE
```

## 发送视频

```bash
echo '{"file_path":"C:/path/to/video.mp4"}' > $TMPFILE
curl -s -X POST http://localhost:$PORT/api/v1/wechat-bot/send/video \
  -H "Content-Type: application/json" \
  --data-binary @$TMPFILE
```

## 查看状态

```bash
curl -s http://localhost:$PORT/api/v1/wechat/status
```

## 查看收到的消息

```bash
curl -s http://localhost:$PORT/api/v1/wechat-bot/messages
```

## 返回值

- `{"status":"sent"}` — 已发送
- `{"status":"buffered"}` — 已排队等待用户回复激活
- 若连接失败返回 `{"error":"..."}`

## 适用场景

- 长时间任务完成通知
- 错误或重要状态变更提醒
- 主动推送结果或摘要
- 查询微信机器人连接状态
