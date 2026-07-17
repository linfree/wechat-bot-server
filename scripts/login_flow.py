#!/usr/bin/env python3
"""
微信 iLink Bot 登录流程调试工具
观察每一步的 API 请求和响应数据变化
"""
import requests, base64, random, time, json, os, sys

BASE = "https://ilinkai.weixin.qq.com"
STATE_FILE = os.path.expanduser("~/.wechat-bot-server/login_state.json")


def uin():
    return base64.b64encode(str(random.randint(0, 2**32)).encode()).decode()


def headers(token=None):
    h = {
        "Content-Type": "application/json",
        "AuthorizationType": "ilink_bot_token",
        "X-WECHAT-UIN": uin(),
    }
    if token:
        h["Authorization"] = f"Bearer {token}"
    return h


def do(method, path, body=None, token=None):
    """发送请求并打印完整响应"""
    url = f"{BASE}/{path}"
    h = headers(token)
    print(f"\n{'='*60}")
    print(f"REQUEST: {method} {path}")
    if body:
        print(f"BODY: {json.dumps(body, ensure_ascii=False)[:300]}")
    if token:
        print(f"TOKEN: {token[:30]}...")

    if method == "GET":
        r = requests.get(url, headers=h, timeout=30)
    else:
        r = requests.post(url, headers=h, json=body, timeout=30)

    print(f"STATUS: {r.status_code}")
    try:
        d = r.json() if r.text else {}
        print(f"RESPONSE:")
        print(json.dumps(d, indent=2, ensure_ascii=False))
    except:
        print(f"RAW: {r.text[:500]}")
    return r.json() if r.text else {}


def save_state(token, base_url, from_id, ctx_token, login_time):
    json.dump(
        {
            "bot_token": token,
            "base_url": base_url or BASE,
            "from_id": from_id,
            "ctx_token": ctx_token,
            "login_time": login_time,
        },
        open(STATE_FILE, "w"),
        indent=2,
    )


def load_state():
    if os.path.exists(STATE_FILE):
        return json.load(open(STATE_FILE))
    return None


# ═══════════════════════════════════════════════════════════════
# Step 1: 获取登录二维码
# ═══════════════════════════════════════════════════════════════
def step1_get_qrcode():
    print("\n" + "█" * 60)
    print("█ Step 1: 获取登录二维码")
    print("█" * 60)
    result = do("GET", "ilink/bot/get_bot_qrcode?bot_type=3")
    qrcode_id = result.get("qrcode", "")
    if not qrcode_id:
        print("ERROR: 未获取到二维码")
        sys.exit(1)

    print(f"\n>>> 扫码链接:")
    print(f"    https://liteapp.weixin.qq.com/q/7GiQu1?qrcode={qrcode_id}&bot_type=3")
    print(f"\n>>> 请在微信中打开上面的链接扫码...")
    return qrcode_id


# ═══════════════════════════════════════════════════════════════
# Step 2: 轮询等待扫码确认
# ═══════════════════════════════════════════════════════════════
def step2_wait_scan(qrcode_id, timeout=300):
    print("\n" + "█" * 60)
    print("█ Step 2: 轮询扫码状态 (每 2 秒)")
    print("█" * 60)

    start = time.time()
    last_status = None
    while time.time() - start < timeout:
        result = do(
            "GET", f"ilink/bot/get_qrcode_status?qrcode={qrcode_id}"
        )
        status = result.get("status", "?")
        if status != last_status:
            elapsed = int(time.time() - start)
            print(f"\n  [{elapsed}s] status → {status}")
            last_status = status

        if status == "confirmed":
            token = result.get("bot_token", "")
            base_url = result.get("baseurl", "") or BASE
            print(f"\n  ✅ 扫码确认!")
            print(f"  token  = {token}")
            print(f"  baseURL= {base_url}")
            return token, base_url

        if status == "expired":
            print(f"\n  ❌ QR 码已过期!")
            return None, None

        time.sleep(2)

    print(f"\n  ⏰ 超时 ({timeout}s)")
    return None, None


# ═══════════════════════════════════════════════════════════════
# Step 3: 开始长轮询，等待第一条消息
# ═══════════════════════════════════════════════════════════════
def step3_poll_messages(token, duration=60):
    print("\n" + "█" * 60)
    print("█ Step 3: 长轮询 getupdates (等待用户消息)")
    print("█" * 60)
    print(f">>> 请给机器人发一条消息 (微信里随便发点什么)...")

    buf = ""
    start = time.time()
    while time.time() - start < duration:
        body = {"get_updates_buf": buf, "base_info": {"channel_version": "1.0.2"}}
        result = do("POST", "ilink/bot/getupdates", body, token=token)

        new_buf = result.get("get_updates_buf", "")
        if new_buf:
            buf = new_buf

        msgs = result.get("msgs", [])
        for msg in msgs:
            if msg.get("message_type") != 1:
                continue
            for item in msg.get("item_list", []):
                text = item.get("text_item", {}).get("text", "")
                if text:
                    from_id = msg.get("from_user_id", "")
                    ctx_token = msg.get("context_token", "")
                    print(f"\n  📩 收到消息:")
                    print(f"    from_id      = {from_id}")
                    print(f"    context_token= {ctx_token[:50]}...")
                    print(f"    text         = {text}")
                    return buf, from_id, ctx_token
        time.sleep(2)

    print("\n  ⏰ 未收到消息")
    return buf, None, None


# ═══════════════════════════════════════════════════════════════
# Step 4: 发送回复
# ═══════════════════════════════════════════════════════════════
def step4_send_message(token, to_id, ctx_token, text):
    print("\n" + "█" * 60)
    print("█ Step 4: 发送消息")
    print("█" * 60)

    client_id = f"debug-{random.randint(0, 0xFFFFFFFF):08x}"
    body = {
        "msg": {
            "from_user_id": "",
            "to_user_id": to_id,
            "client_id": client_id,
            "message_type": 2,
            "message_state": 2,
            "context_token": ctx_token,
            "item_list": [{"type": 1, "text_item": {"text": text}}],
        },
        "base_info": {"channel_version": "1.0.2"},
    }
    result = do("POST", "ilink/bot/sendmessage", body, token=token)
    ret_code = result.get("ret_code", result.get("errcode", "N/A"))
    print(f"\n  ret_code = {ret_code}")
    return ret_code == 0


# ═══════════════════════════════════════════════════════════════
# Main
# ═══════════════════════════════════════════════════════════════
def main():
    print("╔" + "═" * 58 + "╗")
    print("║  微信 iLink Bot 登录流程调试 - 观察数据变化")
    print("╚" + "═" * 58 + "╝")

    # 检查之前的状态
    state = load_state()
    if state:
        print(f"\n发现之前的登录状态:")
        print(f"  token: {state['bot_token'][:30]}...")
        ans = input("  使用保存的状态跳过登录? (Y/n): ").strip().lower()
        if ans != "n":
            token = state["bot_token"]
            base_url = state["base_url"]
            from_id = state.get("from_id", "")
            ctx_token = state.get("ctx_token", "")
            print(f"\n使用已保存的 token，直接进入轮询...")
            buf, fid, ctk = step3_poll_messages(token, duration=30)
            if fid:
                from_id, ctx_token = fid, ctk
            if from_id and ctx_token:
                step4_send_message(token, from_id, ctx_token, "登录验证 ✓")
            return

    # Step 1-2: 登录
    qrcode_id = step1_get_qrcode()
    token, base_url = step2_wait_scan(qrcode_id)
    if not token:
        print("登录失败")
        return

    # Step 3: 等消息
    buf, from_id, ctx_token = step3_poll_messages(token, duration=120)
    if not from_id:
        print("未收到消息，跳过发送测试")
    else:
        # Step 4: 发送
        step4_send_message(token, from_id, ctx_token, "登录成功! token已获取 ✓")

    # 保存状态
    save_state(
        token,
        base_url,
        from_id or "",
        ctx_token or "",
        time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    )
    print(f"\n状态已保存到 {STATE_FILE}")


if __name__ == "__main__":
    main()
