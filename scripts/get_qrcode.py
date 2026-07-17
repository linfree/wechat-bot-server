#!/usr/bin/env python3
"""获取微信机器人登录二维码"""
import requests, base64, random, json, os

BASE = "https://ilinkai.weixin.qq.com"

h = {"Content-Type": "application/json", "AuthorizationType": "ilink_bot_token"}
h["X-WECHAT-UIN"] = base64.b64encode(str(random.randint(0, 2**32)).encode()).decode()

r = requests.get(f"{BASE}/ilink/bot/get_bot_qrcode?bot_type=3", headers=h, timeout=30)
d = r.json() if r.text else {}

print(f"status: {r.status_code}")
print(json.dumps(d, indent=2, ensure_ascii=False))

# 保存二维码图片
img = d.get("qrcode_img_content", "")
if img:
    path = os.path.expanduser("~/.wechat-bot-server/qrcode.png")
    with open(path, "wb") as f:
        f.write(base64.b64decode(img))
    print(f"\n图片已保存: {path}")

# 激活链接
qid = d.get("qrcode", "")
if qid:
    print(f"\n激活链接 (微信打开):")
    print(f"https://liteapp.weixin.qq.com/q/7GiQu1?qrcode={qid}&bot_type=3")
