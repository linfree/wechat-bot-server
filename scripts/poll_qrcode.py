#!/usr/bin/env python3
"""轮询二维码状态直到确认/过期"""
import requests, base64, random, time, json, sys

if len(sys.argv) < 2:
    print("用法: python poll_qrcode.py <qrcode_id>")
    sys.exit(1)

QR = sys.argv[1]
BASE = "https://ilinkai.weixin.qq.com"
TIMEOUT = 180
INTERVAL = 2

def uin():
    return base64.b64encode(str(random.randint(0, 2**32)).encode()).decode()

print(f"轮询 QR: {QR}")
print(f"超时: {TIMEOUT}s,  间隔: {INTERVAL}s")
print(f"{'时间':>6s}  {'状态':<20s}  {'详情'}")
print("-" * 60)

start = time.time()
last = None
while time.time() - start < TIMEOUT:
    try:
        h = {"Content-Type": "application/json", "AuthorizationType": "ilink_bot_token"}
        h["X-WECHAT-UIN"] = uin()
        r = requests.get(f"{BASE}/ilink/bot/get_qrcode_status?qrcode={QR}", headers=h, timeout=10)
        d = r.json() if r.text else {}
    except Exception as e:
        print(f"{int(time.time()-start):>6}s  ERROR: {e}")
        time.sleep(INTERVAL)
        continue

    s = d.get("status", "?")
    if s != last:
        detail = ""
        if s == "confirmed":
            detail = f"token={d.get('bot_token','')} base={d.get('baseurl','')}"
        elif s == "expired":
            detail = "QR 已过期"
        print(f"{int(time.time()-start):>6}s  {s:<20s}  {detail}")
        last = s

    if s in ("confirmed", "expired"):
        print(f"\n完整响应:")
        print(json.dumps(d, indent=2, ensure_ascii=False))
        break

    time.sleep(INTERVAL)
