#!/usr/bin/env python3
"""每身份常驻 Page 路由服务：持有 1 个 BrowserSession，按 HUNTER_ID 把命令路由到各自 tab。

替代 browser-use-cli 的全局焦点 daemon —— CLI 的 handle() 只认一个全局 active tab，强制每次
`switch N` + flock 串行化同身份所有 agent。本服务改用 browse-use 的 actor.Page 层按 target_id
直接寻址：同身份多 agent（每 HUNTER_ID 一个 tab）并发读写不串台、热路径零锁（PoC 实测 5 轮并发
eval 0 串台、cdp_client 并发 send 安全）。唯一的 asyncio.Lock 只守 tab 创建。

复用 browse-use（0 行重造）：
  - DOM numbered 清单：dom_service.get_dom_tree + DOMTreeSerializer.serialize_accessible_elements
  - 点击/填充/悬停/下拉：actor.Element.click/fill/hover/select_option
  - 导航/截图/按键：actor.Page.goto/screenshot/press
  - 建 tab：事件总线 NavigateToUrlEvent（让 SessionManager 一致登记，避免裸 CDP createTarget 触发重连）

输出严格对齐原 CLI（daemon 把 handle() 返回包成 success:True+data，main.py 渲染）：
  - data 含 _raw_text → 原样打印；否则逐 `key: value`（跳过 _ 前缀；screenshot 值 >100 → `<N bytes>`）
  - 仅 Python 异常 → stderr `Error: ...` + exit 1（软错误如 index 失效走 data.error 行，exit 0）
"""
import asyncio
import base64
import json
import os
import sys
import time
from datetime import datetime, timezone
from urllib.parse import parse_qs, urlsplit

import httpx
from browser_use import BrowserSession
from browser_use.actor.page import Page
from browser_use.browser.events import NavigateToUrlEvent
from browser_use.dom.serializer.serializer import DOMTreeSerializer

IDENTITY = sys.argv[1] if len(sys.argv) > 1 else "default"
SOCK = sys.argv[2] if len(sys.argv) > 2 else f"/tmp/browser-svc-{IDENTITY}.sock"

# ---- B1：CDP Network capture → cmd/proxy /internal/v1/flows/ingest ----
# active 容器内 browser-svc.py 持单一 CDP 连接，内建 Network observer 把 chromium 真实认证请求
# （含凭证位置）抓出 → POST 到 LIUSHA_INGEST_URL → ingestor.handleInternalSnap（source=internal，
# owner=active_scan）→ commander/striker 经 list_flows/view_flow 看真实请求结构 + 凭证 → replay_flow
# 做水平/垂直越权（BAC）测试。LIUSHA_INGEST_URL 空 → 整体不启用（单测 / passive-only / 无 cmd/proxy 部署）。
INGEST_URL = os.getenv("LIUSHA_INGEST_URL", "").strip()
INGEST_TOKEN = os.getenv("LIUSHA_INGEST_TOKEN", "").strip()
# 只抓真实认证请求三类 chromium resourceType——静态资源/图片/字体/脚本不入字典（噪音 + 无凭证）。
CAPTURE_TYPES = {"Document", "XHR", "Fetch"}

# 状态变化 action 跑完自动截图喂回 vision LLM（与原 sh wrapper 同集）。
STATE_CHANGING = {
    "open", "click", "input", "type", "select",
    "scroll", "back", "keys", "hover", "dblclick", "rightclick", "wait",
}

# 视口前缀模板，逐字对齐 CLI state（commands/browser.py）。
VIEWPORT_JS = (
    "JSON.stringify({"
    "vw:window.innerWidth,vh:window.innerHeight,"
    "pw:Math.max(document.documentElement.scrollWidth,document.body?document.body.scrollWidth:0),"
    "ph:Math.max(document.documentElement.scrollHeight,document.body?document.body.scrollHeight:0),"
    "sx:Math.round(window.scrollX),sy:Math.round(window.scrollY)})"
)


def render(data) -> str:
    """复刻 main.py 的响应渲染：_raw_text 原样 / 否则 key: value。"""
    if not data:
        return ""
    if isinstance(data, dict):
        if "_raw_text" in data:
            return str(data["_raw_text"]) + "\n"
        lines = []
        for k, v in data.items():
            if k.startswith("_"):
                continue
            if k == "screenshot" and len(str(v)) > 100:
                lines.append(f"{k}: <{len(v)} bytes>")
            else:
                lines.append(f"{k}: {v}")
        return ("\n".join(lines) + "\n") if lines else ""
    return str(data) + "\n"


def parse_timeout_ms(args, default=30000) -> int:
    for i, a in enumerate(args):
        if a == "--timeout" and i + 1 < len(args):
            try:
                return int(args[i + 1])
            except ValueError:
                return default
    return default


class Service:
    def __init__(self):
        self.bs: BrowserSession | None = None
        self.tabs: dict[str, str] = {}       # hunter_id -> target_id
        self.smaps: dict[str, dict] = {}     # hunter_id -> selector_map(index->node)
        self.tab_lock = asyncio.Lock()       # 仅守 tab 创建
        self.start_lock = asyncio.Lock()     # 仅守首次冷启
        self.server: asyncio.AbstractServer | None = None
        # ---- B1 CDP Network capture 状态 ----
        self._http: httpx.AsyncClient | None = None   # 懒构造 POST 客户端
        self._cap_client = None                       # 已注册 handler 的 cdp_client 身份（重连换实例时重注册）
        self._cap_sids: set[str] = set()              # 已 Network.enable 的 session_id（幂等）
        self._sess2hunter: dict[str, str] = {}        # session_id -> hunter_id（逐请求归属）
        self._cap_reqs: dict[str, dict] = {}          # requestId -> 累积的 req/resp 元数据
        self._cap_extra: dict[str, str] = {}          # requestId -> 真实 Cookie（ExtraInfo 早于 request 到达时暂存）
        self._cap_tasks: set[asyncio.Task] = set()    # 持 flush task 引用防 GC

    # ---- 生命周期 ----
    async def ensure_started(self):
        if self.bs is not None:
            return
        async with self.start_lock:
            if self.bs is None:
                bs = BrowserSession(headless=True)
                await bs.start()
                self.bs = bs

    async def settle(self, secs=1.0):
        """等导航/建 tab 引发的重连结束，再取句柄。"""
        await asyncio.sleep(secs)
        for _ in range(40):
            if not self.bs.is_reconnecting:
                return
            await asyncio.sleep(0.25)

    def page(self, hunter_id: str) -> Page:
        # 每次现构造，自带当前 cdp_client，重连安全（target_id 跨重连稳定）。
        return Page(self.bs, self.tabs[hunter_id])

    async def eval_raw(self, page: Page, code: str):
        """裸 Runtime.evaluate（returnByValue），对齐 CLI _execute_js，保留「末表达式=返回值」语义。"""
        sid = await page.session_id
        r = await page._client.send.Runtime.evaluate(
            params={"expression": code, "returnByValue": True},
            session_id=sid,
        )
        if "exceptionDetails" in r:
            raise RuntimeError(str(r["exceptionDetails"]))
        return r.get("result", {}).get("value")

    async def ensure_tab(self, hunter_id: str, url: str):
        """为 task 建/取 tab：首个 task 占初始 about:blank，后来者 new_tab=True。"""
        async with self.tab_lock:
            if hunter_id in self.tabs:
                await self.page(hunter_id).goto(url)
                await self.settle(0.5)
                return
            if not self.tabs:
                # 首个 open：走事件总线把初始 blank 页导到目标（更新焦点 + 离开 about:blank）。
                await self.bs.event_bus.dispatch(NavigateToUrlEvent(url=url, new_tab=False))
                await self.settle()
                self.tabs[hunter_id] = self.bs.get_page_targets()[0].target_id
                return
            # 后来者：new_tab=True，用 target 差集认领新 tab。
            before = {t.target_id for t in self.bs.get_page_targets()}
            await self.bs.event_bus.dispatch(NavigateToUrlEvent(url=url, new_tab=True))
            await self.settle()
            after = self.bs.get_page_targets()
            new = [t.target_id for t in after if t.target_id not in before]
            self.tabs[hunter_id] = new[0] if new else after[-1].target_id

    def require_tab(self, hunter_id: str):
        if hunter_id not in self.tabs:
            raise RuntimeError(
                f"身份={IDENTITY} HUNTER_ID={hunter_id} 还没 open 过 URL；先 browser_use open <URL>"
            )

    async def element_by_index(self, hunter_id: str, page: Page, idx: int):
        """index → selector_map 缓存节点 → actor.Element；index 失效返回 None。"""
        node = self.smaps.get(hunter_id, {}).get(idx)
        if node is None:
            return None
        return await page.get_element(node.backend_node_id)

    async def auto_screenshot(self, page: Page, output_dir: str):
        if not output_dir:
            return
        try:
            b64 = await page.screenshot(format="png")
            path = os.path.join(output_dir, f"auto_{time.monotonic_ns()}.png")
            with open(path, "wb") as f:
                f.write(base64.b64decode(b64))
        except Exception:
            pass  # 截图失败不阻断主动作

    # ---- B1 CDP Network capture ----
    # 设计取自 browser_use HarRecordingWatchdog（已验证）：handler 是 SYNC def，签名 (params, session_id)；
    # register 只挂回调不发命令，Network.enable 按 session 幂等开。v33 sidecar 抢 attach 的 race 在这里
    # 根除——browser-svc.py 本就是唯一 CDP owner，handler 直接挂在它的 cdp_client 上；重连换 client 实例时
    # _ensure_handlers 检测身份变化重注册 + 清 enable 缓存，下条命令自动重新武装。
    # 取舍：首个 open 的初始 Document GET 在 enable 之前发出 → 会漏；但登录 POST/XHR 等带凭证的请求都发生在
    # 后续交互（此时 Network 已 enable）→ BAC 要的真实认证请求不漏。宁可漏首帧也不重蹈 v33 的脆弱完整性。
    def _ensure_handlers(self):
        """在当前 cdp_client 上注册 Network 事件 handler（幂等；重连换 client 实例时重注册）。"""
        client = self.bs.cdp_client
        if client is self._cap_client:
            return
        client.register.Network.requestWillBeSent(self._cap_on_request)
        # ExtraInfo 单独隔离注册：它只负责补 httpOnly Cookie，万一 cdp-use 版本不暴露此事件
        # （AttributeError）也只丢 cookie 补全，绝不连累下面四个已验证的 capture handler——
        # 否则整个 _ensure_handlers 抛错会让 Document/XHR/Fetch 也抓不到，比无 cookie 更糟。
        try:
            client.register.Network.requestWillBeSentExtraInfo(self._cap_on_request_extra)
        except Exception:
            pass  # 降级：无 httpOnly Cookie 捕获，view_flow/replay_flow 仍带非 httpOnly 头
        client.register.Network.responseReceived(self._cap_on_response)
        client.register.Network.loadingFinished(self._cap_on_finished)
        client.register.Network.loadingFailed(self._cap_on_failed)
        self._cap_client = client
        self._cap_sids.clear()  # client 实例已换 → 旧 session enable 作废，让 _ensure_capture 重 enable

    async def _ensure_capture(self, hunter_id: str, page: Page):
        """把 page 的 session 登记到 hunter + 在该 session 上幂等开 Network domain。"""
        if not INGEST_URL:
            return
        self._ensure_handlers()
        sid = await page.session_id
        self._sess2hunter[sid] = hunter_id
        if sid in self._cap_sids:
            return
        try:
            await self.bs.cdp_client.send.Network.enable(params={}, session_id=sid)
            self._cap_sids.add(sid)
        except Exception:
            pass  # enable 失败不阻断主动作，下条命令再试

    # sync handlers：不可 await——重活塞进 _cap_reqs / 调度 task。
    def _cap_on_request(self, params: dict, session_id: str):
        if session_id not in self._sess2hunter:
            return
        req = params.get("request", {})
        rid = params["requestId"]
        self._cap_reqs[rid] = {
            "sid": session_id,
            "type": params.get("type", ""),
            "url": req.get("url", ""),
            "method": req.get("method", ""),
            "req_headers": req.get("headers", {}),
            "req_body": req.get("postData", ""),
            "start": params.get("timestamp", 0.0),
            "wall": params.get("wallTime", 0.0),
            # requestWillBeSent 不含网络层附加的 httpOnly Cookie，由 requestWillBeSentExtraInfo 补；
            # 它若早于本事件到达则从暂存区取。
            "cookie": self._cap_extra.pop(rid, ""),
        }

    def _cap_on_request_extra(self, params: dict, session_id: str):
        # ExtraInfo 携带网络层真实发出的头（含 httpOnly Cookie），requestWillBeSent 里没有。
        # 两事件不保证先后：req 已登记则直接补 cookie，否则暂存等 _cap_on_request 取。
        if session_id not in self._sess2hunter:
            return
        cookie = ""
        for k, v in (params.get("headers") or {}).items():
            if k.lower() == "cookie":
                cookie = v
                break
        if not cookie:
            return
        rid = params.get("requestId")
        ent = self._cap_reqs.get(rid)
        if ent is not None:
            ent["cookie"] = cookie
        else:
            self._cap_extra[rid] = cookie

    def _cap_on_response(self, params: dict, session_id: str):
        ent = self._cap_reqs.get(params.get("requestId"))
        if ent is None:
            return
        resp = params.get("response", {})
        ent["status"] = resp.get("status", 0)
        ent["resp_headers"] = resp.get("headers", {})
        if params.get("type"):
            ent["type"] = params["type"]  # responseReceived 的 type 更准

    def _cap_on_finished(self, params: dict, session_id: str):
        ent = self._cap_reqs.pop(params.get("requestId"), None)
        if ent is None or ent.get("type") not in CAPTURE_TYPES:
            return
        ent["finish"] = params.get("timestamp", ent.get("start", 0.0))
        t = asyncio.create_task(self._cap_flush(params["requestId"], ent))
        self._cap_tasks.add(t)
        t.add_done_callback(self._cap_tasks.discard)

    def _cap_on_failed(self, params: dict, session_id: str):
        self._cap_reqs.pop(params.get("requestId"), None)

    async def _cap_flush(self, request_id: str, ent: dict):
        """取 response body → 组 payload → POST。任何异常吞掉，绝不影响浏览。"""
        hunter_id = self._sess2hunter.get(ent["sid"], "")
        if not hunter_id:
            return
        body_b64 = ""
        try:
            r = await self.bs.cdp_client.send.Network.getResponseBody(
                params={"requestId": request_id}, session_id=ent["sid"]
            )
            raw = r.get("body", "")
            body_b64 = raw if r.get("base64Encoded") else base64.b64encode(
                raw.encode("utf-8", "replace")
            ).decode()
        except Exception:
            pass  # body 取不到（已 evict / redirect / 无 body）→ 仅上报元数据
        try:
            await self._cap_post(self._cap_build(hunter_id, ent, body_b64))
        except Exception:
            pass

    @staticmethod
    def _cap_build(hunter_id: str, ent: dict, body_b64: str) -> dict:
        u = urlsplit(ent.get("url", ""))
        # Go []byte 字段 JSON 走 base64 字符串：request_body / response_body 都要 b64。
        req_body = ent.get("req_body", "") or ""
        req_b64 = base64.b64encode(req_body.encode("utf-8", "replace")).decode() if req_body else ""
        # Go ResponseHeaders 是 map[string][]string——CDP 的 dict[str,str] 值包成单元素 list。
        resp_h = {k: [v] for k, v in (ent.get("resp_headers") or {}).items()}
        dur_ms = int(max(0.0, ent.get("finish", 0.0) - ent.get("start", 0.0)) * 1000)
        # 合入 ExtraInfo 抓到的真实 Cookie（含 httpOnly）：requestWillBeSent 的头里没有，
        # view_flow / replay_flow 要靠它才带得上登录态。有 cookie 才覆盖，否则保留原头不动。
        req_h = dict(ent.get("req_headers") or {})
        cookie = ent.get("cookie", "")
        if cookie:
            req_h = {k: v for k, v in req_h.items() if k.lower() != "cookie"}
            req_h["Cookie"] = cookie
        payload = {
            "hunter_id": hunter_id,
            "host": u.hostname or "",
            "host_port": u.netloc or "",
            "method": ent.get("method", ""),
            "scheme": u.scheme or "",
            "uri": (u.path + ("?" + u.query if u.query else "")) or "/",
            "path": u.path or "/",
            "query": parse_qs(u.query) if u.query else None,
            "status_code": ent.get("status", 0),
            "request_headers": req_h,
            "request_body": req_b64,
            "response_headers": resp_h,
            "response_body": body_b64,
            "duration_ms": dur_ms,
        }
        wall = ent.get("wall", 0.0)
        if wall:
            payload["timestamp"] = datetime.fromtimestamp(wall, tz=timezone.utc).isoformat()
        return payload

    async def _cap_post(self, payload: dict):
        if self._http is None:
            self._http = httpx.AsyncClient(timeout=5.0)
        headers = {"Content-Type": "application/json"}
        if INGEST_TOKEN:
            headers["Authorization"] = "Bearer " + INGEST_TOKEN
        await self._http.post(INGEST_URL, json=payload, headers=headers)

    # ---- action 路由 ----
    async def dispatch(self, req: dict):
        sub = req.get("sub", "")
        args = req.get("args", [])
        hunter_id = req.get("hunter_id", "default")
        output_dir = req.get("output_dir", "")

        if sub == "release-tab":
            await self._release_tab(hunter_id)
            return {}
        if sub == "reset":
            return {"_raw_text": "ok"}  # 真正杀进程在 handle_conn 回包后做

        await self.ensure_started()
        # B1：已建 tab 的 hunter，确保其 session 已挂 Network capture（幂等；重连后下条命令自动重武装）。
        if INGEST_URL and hunter_id in self.tabs:
            await self._ensure_capture(hunter_id, self.page(hunter_id))

        if sub == "open":
            url = args[0]
            await self.ensure_tab(hunter_id, url)
            # 新 tab 刚建好就武装 capture——尽早覆盖登录交互（首帧 Document GET 仍可能漏，见 _ensure_handlers 取舍）。
            if INGEST_URL:
                await self._ensure_capture(hunter_id, self.page(hunter_id))
            data = {"url": url}

        elif sub == "state":
            self.require_tab(hunter_id)
            page = self.page(hunter_id)
            enhanced, _ = await page.dom_service.get_dom_tree(
                target_id=page._target_id, all_frames=None
            )
            serialized, _ = DOMTreeSerializer(
                enhanced, None, paint_order_filtering=True, session_id=self.bs.id
            ).serialize_accessible_elements()
            self.smaps[hunter_id] = serialized.selector_map
            text = serialized.llm_representation()
            try:
                vp = json.loads(await self.eval_raw(page, VIEWPORT_JS))
                prefix = (
                    f"viewport: {vp['vw']}x{vp['vh']}\n"
                    f"page: {vp['pw']}x{vp['ph']}\n"
                    f"scroll: ({vp['sx']}, {vp['sy']})\n"
                )
                text = prefix + text
            except Exception:
                pass
            return {"_raw_text": text}

        elif sub == "click":
            self.require_tab(hunter_id)
            page = self.page(hunter_id)
            if len(args) == 2:
                x, y = int(args[0]), int(args[1])
                mouse = await page.mouse
                await mouse.click(x, y)
                data = {"clicked_coordinate": {"x": x, "y": y}}
            else:
                idx = int(args[0])
                el = await self.element_by_index(hunter_id, page, idx)
                if el is None:
                    data = {"error": f"Element index {idx} not found - page may have changed"}
                else:
                    await el.click()
                    data = {"clicked": idx}

        elif sub == "input":
            self.require_tab(hunter_id)
            page = self.page(hunter_id)
            idx, text = int(args[0]), args[1]
            el = await self.element_by_index(hunter_id, page, idx)
            if el is None:
                data = {"error": f"Element index {idx} not found - page may have changed"}
            else:
                await el.fill(text)
                data = {"input": text, "element": idx}

        elif sub == "type":
            self.require_tab(hunter_id)
            page = self.page(hunter_id)
            text = args[0]
            sid = await page.session_id
            await page._client.send.Input.insertText(params={"text": text}, session_id=sid)
            data = {"typed": text}

        elif sub == "select":
            self.require_tab(hunter_id)
            page = self.page(hunter_id)
            idx, value = int(args[0]), args[1]
            el = await self.element_by_index(hunter_id, page, idx)
            if el is None:
                data = {"error": f"Element index {idx} not found - page may have changed"}
            else:
                await el.select_option(value)
                data = {"selected": value, "element": idx}

        elif sub in ("hover", "dblclick", "rightclick"):
            self.require_tab(hunter_id)
            page = self.page(hunter_id)
            idx = int(args[0])
            el = await self.element_by_index(hunter_id, page, idx)
            if el is None:
                data = {"error": f"Element index {idx} not found - page may have changed"}
            elif sub == "hover":
                await el.hover()
                data = {"hovered": idx}
            elif sub == "dblclick":
                await el.click(click_count=2)
                data = {"double_clicked": idx}
            else:
                await el.click(button="right")
                data = {"right_clicked": idx}

        elif sub == "scroll":
            self.require_tab(hunter_id)
            page = self.page(hunter_id)
            direction = args[0] if args else "down"
            amount = int(args[1]) if len(args) > 1 else 500
            dx, dy = 0, 0
            if direction == "down":
                dy = amount
            elif direction == "up":
                dy = -amount
            elif direction == "right":
                dx = amount
            elif direction == "left":
                dx = -amount
            await self.eval_raw(page, f"window.scrollBy({dx},{dy})")
            data = {"scrolled": direction, "amount": amount}

        elif sub == "back":
            self.require_tab(hunter_id)
            await self.page(hunter_id).go_back()
            data = {"back": True}

        elif sub == "keys":
            self.require_tab(hunter_id)
            keys = args[0]
            await self.page(hunter_id).press(keys)
            data = {"sent": keys}

        elif sub == "wait":
            self.require_tab(hunter_id)
            page = self.page(hunter_id)
            kind = args[0]
            cond = args[1]
            timeout_s = parse_timeout_ms(args) / 1000.0
            elapsed = 0.0
            found = False
            while elapsed < timeout_s:
                if kind == "selector":
                    js = f"document.querySelector({json.dumps(cond)}) !== null"
                else:
                    js = f"document.body.innerText.includes({json.dumps(cond)})"
                if await self.eval_raw(page, js):
                    found = True
                    break
                await asyncio.sleep(0.1)
                elapsed += 0.1
            data = ({"selector": cond, "found": found} if kind == "selector"
                    else {"text": cond, "found": found})

        elif sub == "eval":
            self.require_tab(hunter_id)
            page = self.page(hunter_id)
            result = await self.eval_raw(page, args[0])
            data = {"result": result}

        elif sub == "extract":
            # extract 需 LLM 注入，原 CLI 本就未实现 —— 保持同样 stub（行为零变化）。
            data = {"query": args[0] if args else "", "error": "extract is not yet implemented"}

        elif sub == "get":
            self.require_tab(hunter_id)
            page = self.page(hunter_id)
            gc = args[0] if args else "html"
            if gc == "html":
                html = await self.eval_raw(page, "document.documentElement.outerHTML")
                data = {"html": html or ""}
            elif gc == "title":
                title = await self.eval_raw(page, "document.title")
                data = {"title": title or ""}
            else:
                data = {"error": f"unsupported get command: {gc}"}

        elif sub == "screenshot":
            self.require_tab(hunter_id)
            page = self.page(hunter_id)
            b64 = await page.screenshot(format="png")
            raw = base64.b64decode(b64)
            if args:
                with open(args[0], "wb") as f:
                    f.write(raw)
                data = {"saved": args[0], "size": len(raw)}
            else:
                data = {"screenshot": b64, "size": len(raw)}

        else:
            raise RuntimeError(f"未知子命令: {sub}")

        if sub in STATE_CHANGING and "error" not in data:
            await self.auto_screenshot(self.page(hunter_id), output_dir)
        return data

    async def _release_tab(self, hunter_id: str):
        tid = self.tabs.pop(hunter_id, None)
        self.smaps.pop(hunter_id, None)
        # 清 capture 反查表里属于该 hunter 的 session，避免后续 session 复用 id 时脏归属。
        for sid in [s for s, h in self._sess2hunter.items() if h == hunter_id]:
            self._sess2hunter.pop(sid, None)
            self._cap_sids.discard(sid)
        if tid and self.bs is not None:
            try:
                await self.bs.cdp_client.send.Target.closeTarget(params={"targetId": tid})
            except Exception:
                pass

    async def shutdown(self):
        try:
            if self.server is not None:
                self.server.close()
            if self._http is not None:
                await self._http.aclose()
            if self.bs is not None:
                await self.bs.kill()
        finally:
            try:
                os.remove(SOCK)
            except OSError:
                pass
            os._exit(0)

    # ---- 连接处理（每连接独立 task → 天然并发）----
    async def handle_conn(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter):
        sub = ""
        try:
            line = await reader.readline()
            if not line:
                writer.close()
                return
            req = json.loads(line.decode())
            sub = req.get("sub", "")
            cap = parse_timeout_ms(req.get("args", [])) / 1000.0 + 15 if sub == "wait" \
                else (150 if sub == "open" else 90)
            try:
                data = await asyncio.wait_for(self.dispatch(req), timeout=cap)
                resp = {"stdout": render(data), "stderr": "", "code": 0}
            except Exception as e:
                resp = {"stdout": "", "stderr": f"Error: {e}\n", "code": 1}
            writer.write(json.dumps(resp).encode())
            await writer.drain()
        except Exception:
            pass
        finally:
            try:
                writer.close()
            except Exception:
                pass
        if sub == "reset":
            await self.shutdown()

    async def serve(self):
        if os.path.exists(SOCK):
            os.remove(SOCK)
        self.server = await asyncio.start_unix_server(self.handle_conn, path=SOCK)
        async with self.server:
            await self.server.serve_forever()


if __name__ == "__main__":
    asyncio.run(Service().serve())
