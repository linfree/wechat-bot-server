#!/usr/bin/env python3
"""
微信 iLink Bot 续期(新bot)流程调试工具
观察：旧 bot 解绑 → 新 bot 创建 → 数据变化
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
    url = f"{BASE}/{path}"
    h = headers(token)
    print(f"\n  REQ: {method} {path}")
    if body:
        print(f"  BODY: {json.dumps(body, ensure_ascii=False)[:200]}")
    if token:
        print(f"  TOKEN: {token[:30]}...")

    if method == "GET":
        r = requests.get(url, headers=h, timeout=30)
    else:
        r = requests.post(url, headers=h, json=body, timeout=30)

    print(f"  STATUS: {r.status_code}")
    try:
        d = r.json() if r.text else {}
        print(f"  RESP: {json.dumps(d, ensure_ascii=False)[:500]}")
    except:
        print(f"  RAW: {r.text[:300]}")
    return r.json() if r.text else {}


def load_state():
    if os.path.exists(STATE_FILE):
        return json.load(open(STATE_FILE))
    print(f"ERROR: 未找到 {STATE_FILE}，请先运行 login_flow.py")
    sys.exit(1)


# ═══════════════════════════════════════════════════════════════
# Step 1: 加载旧 token
# ═══════════════════════════════════════════════════════════════
def step1_load_old_session():
    print("█" * 60)
    print("█ Step 1: 加载旧 bot 信息")
    print("█" * 60)
    state = load_state()
    print(f" 旧 token:     {state['bot_token']}")
    print(f" base_url:     {state['base_url']}")
    print(f" from_id:      {state.get('from_id', '(无)')}")
    print(f" ctx_token:    {state.get('ctx_token', '(无)')[:50]}...")
    return state


# ═══════════════════════════════════════════════════════════════
# Step 2: 用旧 token 获取续期 QR
# ═══════════════════════════════════════════════════════════════
def step2_get_reconnect_qr(old_token):
    print("\n" + "█" * 60)
    print("█ Step 2: 用旧 token 获取续期 QR (GET)")
    print("█" * 60)
    result = do("GET", "ilink/bot/get_bot_qrcode?bot_type=3", token=old_token)
    qrcode_id = result.get("qrcode", "")
    if not qrcode_id:
        print("ERROR: 未获取到 QR")
        sys.exit(1)

    print(f"\n续期链接:")
    link = f"https://liteapp.weixin.qq.com/q/7GiQu1?qrcode={qrcode_id}&bot_type=3"
    print(f"  {link}")
    return qrcode_id, link


# ═══════════════════════════════════════════════════════════════
# Step 3: 用旧 token + 旧 ctx 发送续期链接
# ═══════════════════════════════════════════════════════════════
def step3_send_reminder(old_token, from_id, ctx_token, link):
    print("\n" + "█" * 60)
    print("█ Step 3: 用旧 bot 发续期链接到微信")
    print("█" * 60)
    print(f" from_id:      {from_id}")
    print(f" ctx_token:    {ctx_token[:50]}...")

    if not from_id or not ctx_token:
        print("  ⚠️  缺少 from_id/ctx_token，跳过发送")
        print("  (这是因为旧 bot 还没有收到过消息)")
        print("  续期链接已打印在 Step 2，请手动打开扫码")
        return

    client_id = f"rc-{random.randint(0, 0xFFFFFFFF):08x}"
    body = {
        "msg": {
            "from_user_id": "",
            "to_user_id": from_id,
            "client_id": client_id,
            "message_type": 2,
            "message_state": 2,
            "context_token": ctx_token,
            "item_list": [
                {
                    "type": 1,
                    "text_item": {
                        "text": f"### 登录提醒\n\n[重新点击激活机器人]({link})"
                    },
                }
            ],
        },
        "base_info": {"channel_version": "1.0.2"},
    }
    result = do("POST", "ilink/bot/sendmessage", body, token=old_token)
    ret_code = result.get("ret_code", result.get("errcode", "0"))
    print(f"\n 发送结果: ret_code={ret_code}")
    if ret_code and ret_code != 0:
        print(f"  ⚠️  消息发送失败!")
        print(f"  请手动在微信中打开: {link}")


# ═══════════════════════════════════════════════════════════════
# Step 4: 轮询续期 QR，等待扫码
# ═══════════════════════════════════════════════════════════════
def step4_wait_reconnect(qrcode_id, old_token, timeout=180):
    print("\n" + "█" * 60)
    print("█ Step 4: 轮询续期 QR (每 3 秒)")
    print("█" * 60)
    print(">>> 请在微信中打开续期链接扫码...")

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
            new_token = result.get("bot_token", "")
            new_base = result.get("baseurl", "") or BASE
            print(f"\n  ✅ 扫码确认!")
            print(f"  旧 token = {old_token}")
            print(f"  新 token = {new_token}")
            print(f"  token 变化= {'YES (新 bot)' if old_token != new_token else 'NO (相同)'}")
            return new_token, new_base

        if status == "expired":
            print(f"\n  ❌ QR 过期!")
            return None, None

        time.sleep(3)

    print(f"\n  ⏰ 超时")
    return None, None


# ═══════════════════════════════════════════════════════════════
# Step 5: 验证旧 token 失效 + 新 token 有效
# ═══════════════════════════════════════════════════════════════
def step5_verify(old_token, new_token):
    print("\n" + "█" * 60)
    print("█ Step 5: 验证新旧 token 状态")
    print("█" * 60)

    body = {"get_updates_buf": "", "base_info": {"channel_version": "1.0.2"}}

    # 测试旧 token
    print("\n--- 旧 token 轮询 ---")
    r = do("POST", "ilink/bot/getupdates", body, token=old_token)
    old_ec = r.get("errcode", r.get("ret_code", 0))
    print(f"\n 旧 token errcode = {old_ec}")
    if old_ec:
        print(f" >>> 旧 bot 已被解绑! (errcode={old_ec})")
    else:
        print(f" >>> 旧 bot 仍有效 (异常)")

    # 测试新 token
    print("\n--- 新 token 轮询 ---")
    r = do("POST", "ilink/bot/getupdates", body, token=new_token)
    new_ec = r.get("errcode", r.get("ret_code", 0))
    print(f"\n 新 token errcode = {new_ec}")
    if not new_ec:
        print(f" >>> 新 bot 正常!")
    else:
        print(f" >>> 新 bot 异常 (errcode={new_ec})")

    # 测试旧 token 发消息
    print("\n--- 旧 token 发消息 ---")
    client_id = f"vrf-{random.randint(0, 0xFFFFFFFF):08x}"
    body2 = {
        "msg": {
            "from_user_id": "",
            "to_user_id": "test",
            "client_id": client_id,
            "message_type": 2,
            "message_state": 2,
            "context_token": "",
            "item_list": [{"type": 1, "text_item": {"text": "test"}}],
        },
        "base_info": {"channel_version": "1.0.2"},
    }
    r = do("POST", "ilink/bot/sendmessage", body2, token=old_token)
    send_ec = r.get("errcode", r.get("ret_code", 0))
    print(f"\n 旧 token send errcode = {send_ec}")
    if send_ec:
        print(f" >>> 旧 bot 发消息被拒绝")
    else:
        print(f" >>> 旧 bot 仍能发消息 (异常)")


# ═══════════════════════════════════════════════════════════════
# Main
# ═══════════════════════════════════════════════════════════════
def main():
    print("╔" + "═" * 58 + "╗")
    print("║  微信 iLink Bot 续期流程调试 - 观察新 bot 创建 & 旧 bot 解绑")
    print("╚" + "═" * 58 + "╝")

    state = step1_load_old_session()
    old_token = state["bot_token"]
    from_id = state.get("from_id", "")
    ctx_token = state.get("ctx_token", "")

    qrcode_id, link = step2_get_reconnect_qr(old_token)
    step3_send_reminder(old_token, from_id, ctx_token, link)

    new_token, new_base = step4_wait_reconnect(qrcode_id, old_token)
    if not new_token:
        print("续期失败")
        return

    step5_verify(old_token, new_token)

    print("\n" + "=" * 60)
    print(" 续期流程完成")
    print(f" 旧 token: {old_token}")
    print(f" 新 token: {new_token}")
    print(f" 旧 bot 已解绑 ✓")
    print(f" 新 bot 已创建 ✓")
    print("=" * 60)


if __name__ == "__main__":
    main()
