#!/usr/bin/env python3
"""cdp_network_capture.py — 通过 CDP Network domain 抓 chromium 全量 req/resp → push 到 liusha
ingest endpoint，让 chromium 流量入 http_flow 字典（与 PD 工具等其他 sandbox 流量同表）。

背景（v33+ 撤回 chromium 经 martian proxy 路径）：
- chromium 经 sanitizer 8890 proxy 反复失败：HTTPS-First Mode auto-upgrade /
  Fetch.authRequired 不触发 / CDP inject WebSocket level 错 / macOS Docker NAT 多层 bug
- 改 CDP Network 主动抓（chromium 原生最稳定 API，本地连本地不走代理）→ POST 到
  cmd/proxy /internal/v1/flows/ingest endpoint → publisher.Publish → ingestor 落库
- wrapper 启 chromium daemon 后 spawn 此脚本作伴生进程，与 chromium 同生命周期

CDP 协议要点：
- 用 Target.setAutoAttach(flatten=True) 让 browser-level WS 自动 attach 所有 page targets
  - flatten=True 关键：所有 page-session 消息直接带 sessionId 字段（非嵌套
    Target.sendMessageToTarget），简化消息路由
- 每个 page session attach 后 send Network.enable 启抓流
- requestId 是 page-session-local，所以缓存 key 用 (sessionId, requestId)
- loadingFinished 后调 Network.getResponseBody 拿 body —— 用 msg_id → future 配对
- loadingFailed → 丢 cache 避免内存泄漏

env：
    LIUSHA_INGEST_URL    cmd/proxy /internal/v1/flows/ingest 完整 URL，缺失 → 自退出
    LIUSHA_INGEST_TOKEN  Bearer token，缺失 → 不带 Authorization header（dev 模式）
    LIUSHA_HUNTER_ID     当前 hunter id，缺失 → 自退出
    LIUSHA_CDP_URL       chromium DevTools HTTP base，默认 http://127.0.0.1:9222

用法（wrapper 在 chromium daemon 起后 spawn）：
    python3 cdp_network_capture.py &
"""

import asyncio
import base64
import json
import os
import signal
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from typing import Any

import httpx
import websockets

# CDP 消息缓存上限——大流量站点单 page 短时间内可能上千 in-flight 请求；
# 超过即丢最早条目防内存爆。生产 chromium 单 page 极少同时 in-flight > 200。
MAX_INFLIGHT_REQUESTS = 1000

# response body 大小硬上限——超过的直接截断（与 proxy.max_response_body_size 8MiB 对齐）。
MAX_RESPONSE_BODY_BYTES = 8 * 1024 * 1024
MAX_REQUEST_BODY_BYTES = 2 * 1024 * 1024

# /internal/v1/flows/ingest 单次 POST 超时——本地 docker bridge ping 通常 < 5ms，
# 5s 余量足够；超时即丢该 flow（不重试，避免 backpressure 压垮 chromium）。
INGEST_POST_TIMEOUT_SEC = 5.0

# getResponseBody CDP 配对超时——chromium 返回 body 通常 < 100ms，3s 安全冗余。
GET_BODY_TIMEOUT_SEC = 3.0


class FlowCache:
    """缓存 in-flight 请求数据，等 loadingFinished 后合并为 1 条 flow 推送。

    key:  (session_id, request_id) — chromium requestId 仅在 page-session 内唯一
    val:  dict 含 url/method/headers/body/status/response_headers/started_at
    """

    def __init__(self, max_size: int = MAX_INFLIGHT_REQUESTS):
        self._data: dict[tuple[str, str], dict[str, Any]] = {}
        self._max_size = max_size

    def put(self, session_id: str, request_id: str, entry: dict[str, Any]) -> None:
        if len(self._data) >= self._max_size:
            oldest = next(iter(self._data))
            self._data.pop(oldest, None)
            print(f"[cdp-capture] flow cache 满 ({self._max_size})，淘汰最早请求", file=sys.stderr)
        self._data[(session_id, request_id)] = entry

    def get(self, session_id: str, request_id: str) -> dict[str, Any] | None:
        return self._data.get((session_id, request_id))

    def pop(self, session_id: str, request_id: str) -> dict[str, Any] | None:
        return self._data.pop((session_id, request_id), None)

    def drop_session(self, session_id: str) -> None:
        """detachedFromTarget 时清掉该 session 所有 in-flight，避免泄漏。"""
        keys = [k for k in self._data if k[0] == session_id]
        for k in keys:
            self._data.pop(k, None)


class CDPCapture:
    def __init__(self, cdp_url: str, ingest_url: str, ingest_token: str, hunter_id: str):
        self._cdp_url = cdp_url.rstrip("/")
        self._ingest_url = ingest_url
        self._ingest_token = ingest_token
        self._hunter_id = hunter_id
        self._cache = FlowCache()
        self._msg_id = 0
        # msg_id → future，用于 getResponseBody 等 request/response 配对调用
        self._pending: dict[int, asyncio.Future] = {}
        self._http = httpx.AsyncClient(timeout=INGEST_POST_TIMEOUT_SEC)
        # 互斥 ws.send（websockets lib 不保证多 task 并发 send 安全）
        self._send_lock = asyncio.Lock()

    def _next_id(self) -> int:
        self._msg_id += 1
        return self._msg_id

    async def _send(self, ws, method: str, params: dict[str, Any] | None = None,
                    session_id: str | None = None) -> None:
        """单向发送，不等响应。"""
        msg: dict[str, Any] = {"id": self._next_id(), "method": method}
        if params:
            msg["params"] = params
        if session_id:
            msg["sessionId"] = session_id
        async with self._send_lock:
            await ws.send(json.dumps(msg))

    async def _call(self, ws, method: str, params: dict[str, Any] | None = None,
                    session_id: str | None = None,
                    timeout: float = GET_BODY_TIMEOUT_SEC) -> dict[str, Any] | None:
        """request/response 配对调用——等 read loop 收到匹配 id 的响应。

        超时返回 None；CDP 返 error 也返 None（仅 result 字段返回）。
        """
        msg_id = self._next_id()
        msg: dict[str, Any] = {"id": msg_id, "method": method}
        if params:
            msg["params"] = params
        if session_id:
            msg["sessionId"] = session_id

        loop = asyncio.get_running_loop()
        fut: asyncio.Future = loop.create_future()
        self._pending[msg_id] = fut
        try:
            async with self._send_lock:
                await ws.send(json.dumps(msg))
            try:
                return await asyncio.wait_for(fut, timeout=timeout)
            except asyncio.TimeoutError:
                return None
        finally:
            self._pending.pop(msg_id, None)

    async def run(self) -> None:
        while True:
            try:
                ws_url = await self._discover_browser_ws()
                if not ws_url:
                    await asyncio.sleep(1)
                    continue

                print(f"[cdp-capture] 连 chromium CDP: {ws_url}", file=sys.stderr)
                async with websockets.connect(ws_url, max_size=32 * 1024 * 1024) as ws:
                    # flatten=True：所有 page-session 消息直接带 sessionId 字段
                    # autoAttach=True：新 target 创建即自动 attach
                    # waitForDebuggerOnStart=False：不卡住 page 等 debugger
                    await self._send(ws, "Target.setAutoAttach", {
                        "autoAttach": True,
                        "waitForDebuggerOnStart": False,
                        "flatten": True,
                    })
                    await self._read_loop(ws)

            except (ConnectionRefusedError, urllib.error.URLError) as e:
                print(f"[cdp-capture] chromium 暂不可达 ({e})，1s 重试", file=sys.stderr)
                await asyncio.sleep(1)
            except websockets.ConnectionClosed:
                print("[cdp-capture] WS 关闭，1s 重连", file=sys.stderr)
                await asyncio.sleep(1)
            except asyncio.CancelledError:
                raise
            except Exception as e:  # noqa: BLE001
                print(f"[cdp-capture] 主循环异常 {type(e).__name__}: {e}，1s 重试",
                      file=sys.stderr)
                await asyncio.sleep(1)

    async def _discover_browser_ws(self) -> str | None:
        try:
            req = urllib.request.Request(f"{self._cdp_url}/json/version")
            with urllib.request.urlopen(req, timeout=5) as resp:
                info = json.loads(resp.read())
            return info.get("webSocketDebuggerUrl")
        except Exception as e:  # noqa: BLE001
            print(f"[cdp-capture] /json/version 拉取失败: {e}", file=sys.stderr)
            return None

    async def _read_loop(self, ws) -> None:
        async for raw in ws:
            try:
                msg = json.loads(raw)
            except json.JSONDecodeError:
                continue

            # 响应消息（带 id 无 method）→ 找配对 future
            if "id" in msg and "method" not in msg:
                fut = self._pending.get(msg["id"])
                if fut and not fut.done():
                    if "error" in msg:
                        fut.set_result(None)
                    else:
                        fut.set_result(msg.get("result", {}))
                continue

            method = msg.get("method")
            params = msg.get("params", {})
            session_id = msg.get("sessionId", "")

            if method == "Target.attachedToTarget":
                child_session = params.get("sessionId", "")
                target_info = params.get("targetInfo", {})
                target_type = target_info.get("type", "")
                if child_session and target_type in ("page", "iframe", "webview"):
                    await self._send(ws, "Network.enable",
                                     {"maxResourceBufferSize": MAX_RESPONSE_BODY_BYTES,
                                      "maxTotalBufferSize": MAX_RESPONSE_BODY_BYTES * 4},
                                     session_id=child_session)
                continue

            if method == "Target.detachedFromTarget":
                gone = params.get("sessionId", "")
                if gone:
                    self._cache.drop_session(gone)
                continue

            if method == "Network.requestWillBeSent":
                self._on_request(session_id, params)
            elif method == "Network.responseReceived":
                self._on_response(session_id, params)
            elif method == "Network.loadingFinished":
                asyncio.create_task(self._on_loading_finished(ws, session_id, params))
            elif method == "Network.loadingFailed":
                request_id = params.get("requestId", "")
                if request_id:
                    self._cache.pop(session_id, request_id)

    def _on_request(self, session_id: str, params: dict[str, Any]) -> None:
        request_id = params.get("requestId", "")
        request = params.get("request", {})
        if not request_id or not request.get("url"):
            return

        url = request["url"]
        if not (url.startswith("http://") or url.startswith("https://")):
            return

        post_data = request.get("postData", "")
        if post_data and len(post_data.encode("utf-8")) > MAX_REQUEST_BODY_BYTES:
            post_data = post_data.encode("utf-8")[:MAX_REQUEST_BODY_BYTES].decode(
                "utf-8", errors="replace")

        self._cache.put(session_id, request_id, {
            "url": url,
            "method": request.get("method", "GET"),
            "request_headers": request.get("headers", {}),
            "request_body": post_data,
            "started_at": time.time(),
        })

    def _on_response(self, session_id: str, params: dict[str, Any]) -> None:
        request_id = params.get("requestId", "")
        response = params.get("response", {})
        entry = self._cache.get(session_id, request_id)
        if not entry:
            return
        entry["status_code"] = response.get("status", 0)
        entry["response_headers"] = response.get("headers", {})

    async def _on_loading_finished(self, ws, session_id: str, params: dict[str, Any]) -> None:
        request_id = params.get("requestId", "")
        entry = self._cache.pop(session_id, request_id)
        if not entry:
            return

        body_bytes = b""
        result = await self._call(ws, "Network.getResponseBody",
                                  {"requestId": request_id}, session_id=session_id)
        if result and "body" in result:
            raw = result["body"]
            if result.get("base64Encoded"):
                try:
                    body_bytes = base64.b64decode(raw)
                except (ValueError, TypeError):
                    body_bytes = b""
            else:
                body_bytes = raw.encode("utf-8", errors="replace")
            if len(body_bytes) > MAX_RESPONSE_BODY_BYTES:
                body_bytes = body_bytes[:MAX_RESPONSE_BODY_BYTES]

        await self._push_flow(entry, body_bytes)

    async def _push_flow(self, entry: dict[str, Any], body_bytes: bytes) -> None:
        url = entry["url"]
        try:
            parsed = urllib.parse.urlsplit(url)
        except ValueError:
            return

        host = parsed.hostname or ""
        host_port = parsed.netloc  # 含 port
        scheme = parsed.scheme
        path = parsed.path or "/"
        query_str = parsed.query
        uri = path + (("?" + query_str) if query_str else "")

        query_map: dict[str, list[str]] = {}
        if query_str:
            for k, vs in urllib.parse.parse_qs(query_str, keep_blank_values=True).items():
                query_map[k] = vs

        # CDP headers 是 dict[str, str]；endpoint 端接受 dict[str, list[str]] for response。
        # CDP 多值 header 用 "\n" 拼接，拆开。
        resp_headers_raw = entry.get("response_headers", {})
        resp_headers: dict[str, list[str]] = {}
        for k, v in resp_headers_raw.items():
            if "\n" in v:
                resp_headers[k.lower()] = v.split("\n")
            else:
                resp_headers[k.lower()] = [v]

        req_headers: dict[str, str] = {}
        for k, v in entry.get("request_headers", {}).items():
            req_headers[k.lower()] = v

        duration_ms = int((time.time() - entry.get("started_at", time.time())) * 1000)

        req_body_str = entry.get("request_body", "") or ""
        req_body_bytes = (req_body_str.encode("utf-8")
                          if isinstance(req_body_str, str) else req_body_str)

        # Go 端 []byte JSON 解码接受 base64 字符串（encoding/json 默认行为）
        payload = {
            "hunter_id": self._hunter_id,
            "host": host,
            "host_port": host_port,
            "method": entry.get("method", "GET"),
            "scheme": scheme,
            "uri": uri,
            "path": path,
            "query": query_map if query_map else None,
            "status_code": entry.get("status_code", 0),
            "request_headers": req_headers if req_headers else None,
            "request_body": (base64.b64encode(req_body_bytes).decode("ascii")
                             if req_body_bytes else None),
            "response_headers": resp_headers if resp_headers else None,
            "response_body": (base64.b64encode(body_bytes).decode("ascii")
                              if body_bytes else None),
            "duration_ms": duration_ms,
        }
        payload = {k: v for k, v in payload.items() if v is not None}

        headers = {"Content-Type": "application/json"}
        if self._ingest_token:
            headers["Authorization"] = "Bearer " + self._ingest_token

        try:
            resp = await self._http.post(self._ingest_url, json=payload, headers=headers)
            if resp.status_code >= 400:
                print(f"[cdp-capture] ingest 4xx/5xx {resp.status_code}: {resp.text[:200]}",
                      file=sys.stderr)
        except httpx.HTTPError as e:
            print(f"[cdp-capture] ingest POST 失败 {type(e).__name__}: {e}", file=sys.stderr)


def install_signal_handlers(loop: asyncio.AbstractEventLoop) -> None:
    def shutdown() -> None:
        for task in asyncio.all_tasks(loop):
            task.cancel()

    for sig in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(sig, shutdown)


def main() -> int:
    # wrapper 端已 guard `LIUSHA_INGEST_URL && LIUSHA_HUNTER_ID` 才 spawn 本脚本；
    # 这里直接读 + 缺失即 fail-fast（非 0），让 wrapper log 暴露配置错误。
    ingest_url = os.environ["LIUSHA_INGEST_URL"].strip()
    hunter_id = os.environ["LIUSHA_HUNTER_ID"].strip()
    ingest_token = os.environ.get("LIUSHA_INGEST_TOKEN", "").strip()
    cdp_url = os.environ.get("LIUSHA_CDP_URL", "http://127.0.0.1:9222").strip()

    print(f"[cdp-capture] 启动 hunter={hunter_id} cdp={cdp_url} ingest={ingest_url} "
          f"token={'set' if ingest_token else 'unset'}", file=sys.stderr)

    capture = CDPCapture(cdp_url, ingest_url, ingest_token, hunter_id)

    loop = asyncio.new_event_loop()
    install_signal_handlers(loop)
    try:
        loop.run_until_complete(capture.run())
    except asyncio.CancelledError:
        pass
    finally:
        loop.run_until_complete(capture._http.aclose())
        loop.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
