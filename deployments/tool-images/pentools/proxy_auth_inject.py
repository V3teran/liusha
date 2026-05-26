#!/usr/bin/env python3
"""
proxy_auth_inject.py — 通过 CDP 自动响应 chromium 的 Proxy-Authorization basic auth。

背景（0060+ Phase 3 B 方案）：
- chromium 命令行 --proxy-server=http://host:port 不接受内嵌 user:pass
- 必须通过 Chrome DevTools Protocol (CDP) Fetch.authRequired 事件响应
- browser-use 0.12.9 的 skill_cli 入口不传 proxy 给 playwright launch，
  liusha pentools wrapper 自己启 chromium daemon（带 --proxy-server）+ 跑本脚本注入 auth
- browser-use-cli 通过 --cdp-url ws://127.0.0.1:9222 连到这个 chromium 共享 daemon

用法（wrapper 在 chromium daemon 起后 spawn 本脚本）：
    python3 proxy_auth_inject.py \
        --cdp-url http://127.0.0.1:9222 \
        --proxy-user hunter_<uuid> \
        --proxy-pass _

行为：
- 连 chromium browser-level WebSocket (`/json/version`)
- 子 session 启 Fetch.enable + handleAuthRequests=true
- 收到 Fetch.authRequired → Fetch.continueWithAuth(ProvideCredentials)
- 收到 Fetch.requestPaused → Fetch.continueRequest（不拦截内容，仅借用 auth flow）
- 容错：网络抖动 / chromium 重启 → 自动重连
- 优雅退出：SIGTERM / SIGINT → 关 WebSocket
"""

import argparse
import asyncio
import json
import signal
import sys
import urllib.request

import websockets


async def listen_session(ws, proxy_user: str, proxy_pass: str, msg_id_start: int) -> None:
    """监听单个 CDP session 上的 Fetch 事件并响应 auth。"""
    msg_id = msg_id_start

    # 启 Fetch domain，handleAuthRequests=True 让 chromium 把 auth 挑战转给我们
    await ws.send(json.dumps({
        "id": msg_id,
        "method": "Fetch.enable",
        "params": {"handleAuthRequests": True},
    }))
    msg_id += 1

    async for raw in ws:
        evt = json.loads(raw)
        method = evt.get("method")
        params = evt.get("params", {})

        if method == "Fetch.authRequired":
            await ws.send(json.dumps({
                "id": msg_id,
                "method": "Fetch.continueWithAuth",
                "params": {
                    "requestId": params["requestId"],
                    "authChallengeResponse": {
                        "response": "ProvideCredentials",
                        "username": proxy_user,
                        "password": proxy_pass,
                    },
                },
            }))
            msg_id += 1

        elif method == "Fetch.requestPaused":
            # 不拦截请求体，仅借用 auth 流程透传
            await ws.send(json.dumps({
                "id": msg_id,
                "method": "Fetch.continueRequest",
                "params": {"requestId": params["requestId"]},
            }))
            msg_id += 1


async def main_loop(cdp_url: str, proxy_user: str, proxy_pass: str) -> None:
    """主循环：连接 chromium browser-level WS，监听 Fetch 事件。"""
    while True:
        try:
            with urllib.request.urlopen(f"{cdp_url}/json/version", timeout=5) as resp:
                browser_info = json.loads(resp.read())
            ws_url = browser_info.get("webSocketDebuggerUrl")
            if not ws_url:
                print("[proxy-auth-inject] /json/version 缺 webSocketDebuggerUrl，1s 后重试",
                      file=sys.stderr)
                await asyncio.sleep(1)
                continue

            print(f"[proxy-auth-inject] 连 chromium CDP: {ws_url}", file=sys.stderr)
            async with websockets.connect(ws_url, max_size=10 * 1024 * 1024) as ws:
                await listen_session(ws, proxy_user, proxy_pass, msg_id_start=1)

        except (ConnectionRefusedError, urllib.error.URLError) as e:
            print(f"[proxy-auth-inject] chromium 暂不可达 ({e})，1s 后重试", file=sys.stderr)
            await asyncio.sleep(1)
        except websockets.ConnectionClosed:
            print("[proxy-auth-inject] WS 关闭，1s 后重连", file=sys.stderr)
            await asyncio.sleep(1)
        except Exception as e:  # noqa: BLE001
            print(f"[proxy-auth-inject] 异常 {type(e).__name__}: {e}，1s 后重试", file=sys.stderr)
            await asyncio.sleep(1)


def install_signal_handlers(loop: asyncio.AbstractEventLoop) -> None:
    """SIGTERM/SIGINT 优雅退出。"""
    def shutdown() -> None:
        for task in asyncio.all_tasks(loop):
            task.cancel()
    for sig in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(sig, shutdown)


def main() -> int:
    parser = argparse.ArgumentParser(description="chromium CDP proxy auth injector")
    parser.add_argument("--cdp-url", required=True,
                        help="chromium DevTools HTTP base, e.g. http://127.0.0.1:9222")
    parser.add_argument("--proxy-user", required=True)
    parser.add_argument("--proxy-pass", required=True)
    args = parser.parse_args()

    loop = asyncio.new_event_loop()
    install_signal_handlers(loop)
    try:
        loop.run_until_complete(main_loop(args.cdp_url, args.proxy_user, args.proxy_pass))
    except asyncio.CancelledError:
        pass
    finally:
        loop.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
