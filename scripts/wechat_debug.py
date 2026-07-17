#!/usr/bin/env python3
"""
微信 iLink Bot 调试工具
直接调用微信 API，不依赖 Go 服务，用于调试完整的生命周期：
登录 → 轮询 → 发消息 → 续期 → 断开
"""
import requests
import json
import time
import base64
import random
import sys
import os

BASE_URL = "https://ilinkai.weixin.qq.com"
HEADERS_TEMPLATE = {
    "Content-Type": "application/json",
    "AuthorizationType": "ilink_bot_token",
}


def rand_uin():
    uin = str(random.randint(0, 2**32))
    return base64.b64encode(uin.encode()).decode()


def do_request(method, path, body=None, token=None):
    """发送请求到微信 API"""
    url = f"{BASE_URL}/{path}"
    headers = dict(HEADERS_TEMPLATE)
    headers["X-WECHAT-UIN"] = rand_uin()
    if token:
        headers["Authorization"] = f"Bearer {token}"

    print(f"  -> {method} {path}")
    if method == "GET":
        resp = requests.get(url, headers=headers, timeout=30)
    else:
        resp = requests.post(url, headers=headers, json=body, timeout=30)

    print(f"  <- {resp.status_code} {resp.text[:200]}")
    return resp.json() if resp.text else {}


# ─── Step 1: 获取二维码 ───
def get_qrcode():
    """获取登录二维码"""
    print("\n" + "="*60)
    print("Step 1: 获取登录二维码")
    print("="*60)
    result = do_request("GET", "ilink/bot/get_bot_qrcode?bot_type=3")
    qrcode_id = result.get("qrcode", "")
    qrcode_img = result.get("qrcode_img_content", "")
    print(f"  qrcode_id: {qrcode_id}")
    return qrcode_id


# ─── Step 2: 轮询扫码确认 ───
def wait_scan(qrcode_id, timeout=300):
    """轮询等待用户扫码确认"""
    print("\n" + "="*60)
    print("Step 2: 等待扫码确认")
    print("="*60)
    print(f"  激活链接: https://liteapp.weixin.qq.com/q/7GiQu1?qrcode={qrcode_id}&bot_type=3")
    print(f"  请在微信中打开上面的链接扫码，或手动扫码...")

    start = time.time()
    last_status = None
    while time.time() - start < timeout:
        result = do_request("GET", f"ilink/bot/get_qrcode_status?qrcode={qrcode_id}")
        status = result.get("status", "unknown")
        if status != last_status:
            print(f"  status: {status}")
            last_status = status

        if status == "confirmed":
            token = result.get("bot_token", "")
            base_url = result.get("baseurl", "")
            print(f"\n  ✅ 扫码确认！")
            print(f"  token:  {token}")
            print(f"  baseURL: {base_url}")
            return token, base_url

        if status == "expired":
            print(f"\n  ⚠️ QR 码已过期")
            return None, None

        time.sleep(2)

    print(f"\n  ⏰ 超时 ({timeout}s)")
    return None, None


# ─── Step 3: 开始轮询消息 ───
def start_polling(token, duration=60):
    """开始长轮询接收消息"""
    print("\n" + "="*60)
    print("Step 3: 开始轮询消息")
    print("="*60)
    print(f"  轮询 {duration}s，请向机器人发送消息测试...")

    buf = ""
    start = time.time()
    msg_count = 0

    while time.time() - start < duration:
        body = {
            "get_updates_buf": buf,
            "base_info": {"channel_version": "1.0.2"},
        }
        result = do_request("POST", "ilink/bot/getupdates", body, token=token)

        new_buf = result.get("get_updates_buf", "")
        if new_buf:
            buf = new_buf

        msgs = result.get("msgs", [])
        for msg in msgs:
            msg_type = msg.get("message_type", 0)
            if msg_type != 1:  # 只关心文本消息
                continue
            text_items = msg.get("item_list", [])
            for item in text_items:
                text = item.get("text_item", {}).get("text", "")
                if text:
                    msg_count += 1
                    from_user = msg.get("from_user_id", "?")
                    ctx_token = msg.get("context_token", "")
                    print(f"\n  📩 收到消息 #{msg_count}:")
                    print(f"     from:  {from_user}")
                    print(f"     text:  {text}")
                    print(f"     ctx:   {ctx_token[:40]}...")
        time.sleep(1)

    print(f"\n  共收到 {msg_count} 条消息")
    return buf


# ─── Step 4: 发送消息 ───
def send_message(token, to_user, context_token, text):
    """发送文本消息"""
    print("\n" + "="*60)
    print("Step 4: 发送消息")
    print("="*60)
    print(f"  to:   {to_user}")
    print(f"  text:  {text}")

    client_id = f"debug-{random.randint(0, 0xFFFFFFFF):08x}"
    body = {
        "msg": {
            "from_user_id": "",
            "to_user_id": to_user,
            "client_id": client_id,
            "message_type": 2,
            "message_state": 2,
            "context_token": context_token,
            "item_list": [
                {"type": 1, "text_item": {"text": text}}
            ],
        },
        "base_info": {"channel_version": "1.0.2"},
    }
    result = do_request("POST", "ilink/bot/sendmessage", body, token=token)
    ret_code = result.get("ret_code", result.get("errcode", "N/A"))
    ret_msg = result.get("ret_msg", result.get("errmsg", ""))
    print(f"  result: ret_code={ret_code} ret_msg={ret_msg}")
    return ret_code == 0


# ─── Step 5: 续期测试 ───
def test_reconnect(old_token):
    """
    模拟续期流程：获取新 QR → 等待确认 → 获取新 token
    旧的 token 应该在此过程中失效
    """
    print("\n" + "="*60)
    print("Step 5: 续期测试")
    print("="*60)
    print("  模拟 20h 后的续期提醒...")

    # 1. 获取续期 QR
    qrcode_id = get_qrcode()
    if not qrcode_id:
        print("  ❌ 无法获取续期 QR")
        return None, None

    # 2. 发送续期提醒（用旧 token）
    print("\n  发送续期提醒链接...")
    reminder = f"### 登录提醒\n\n[重新点击激活机器人](https://liteapp.weixin.qq.com/q/7GiQu1?qrcode={qrcode_id}&bot_type=3)"
    # TODO: 这里需要通过旧 token 发消息

    # 3. 等待用户扫码
    new_token, new_base = wait_scan(qrcode_id, timeout=120)
    if not new_token:
        return None, None

    # 4. 对比新旧 token
    print(f"\n  旧 token: {old_token[:20]}...")
    print(f"  新 token: {new_token[:20]}...")
    print(f"  token 是否变化: {'YES (新 session)' if old_token != new_token else 'NO (相同)'}")

    # 5. 验证旧 token 已失效
    print("\n  验证旧 token 是否失效...")
    result = do_request("POST", "ilink/bot/getupdates",
                        {"get_updates_buf": "", "base_info": {"channel_version": "1.0.2"}},
                        token=old_token)
    errcode = result.get("errcode", result.get("ret_code", 0))
    if errcode and errcode != 0:
        print(f"  ✅ 旧 token 已失效 (errcode={errcode})")
    else:
        print(f"  ⚠️ 旧 token 仍然有效？response: {json.dumps(result)[:200]}")

    return new_token, new_base


# ─── 主流程 ───
def main():
    print("="*60)
    print("  微信 iLink Bot 调试工具")
    print("="*60)

    # Step 1-2: 登录
    qrcode_id = get_qrcode()
    if not qrcode_id:
        print("❌ 获取二维码失败")
        return

    token, base_url = wait_scan(qrcode_id)
    if not token:
        print("❌ 未确认扫码")
        return

    # 更新 base URL（如果服务端返回了不同的）
    global BASE_URL
    if base_url:
        BASE_URL = base_url
        print(f"  baseURL 更新为: {BASE_URL}")

    # Step 3: 开始接收消息
    buf = start_polling(token, duration=60)

    # Step 4: 交互发送消息
    print("\n" + "="*60)
    print("Step 4: 交互模式")
    print("="*60)
    print("  先收到一条消息以获取 to_user 和 context_token...")

    # 再轮询一会儿等消息
    body = {"get_updates_buf": buf, "base_info": {"channel_version": "1.0.2"}}
    result = do_request("POST", "ilink/bot/getupdates", body, token=token)
    buf = result.get("get_updates_buf", buf)

    msgs = result.get("msgs", [])
    to_user = None
    ctx_token = None
    for msg in msgs:
        if msg.get("message_type") == 1:
            to_user = msg.get("from_user_id")
            ctx_token = msg.get("context_token")
            break

    if not to_user:
        print("  没有待处理的消息，跳过发送测试")
    else:
        while True:
            text = input("\n  输入要发送的消息 (回车跳过): ").strip()
            if not text:
                break
            send_message(token, to_user, ctx_token, text)

    # Step 5: 续期测试
    ans = input("\n  测试续期流程？(y/N): ").strip().lower()
    if ans == 'y':
        new_token, _ = test_reconnect(token)
        if new_token:
            # 用新 token 继续轮询
            start_polling(new_token, duration=30)


if __name__ == "__main__":
    main()
