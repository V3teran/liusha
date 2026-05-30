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

from browser_use import BrowserSession
from browser_use.actor.page import Page
from browser_use.browser.events import NavigateToUrlEvent
from browser_use.dom.serializer.serializer import DOMTreeSerializer

IDENTITY = sys.argv[1] if len(sys.argv) > 1 else "default"
SOCK = sys.argv[2] if len(sys.argv) > 2 else f"/tmp/browser-svc-{IDENTITY}.sock"

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

        if sub == "open":
            url = args[0]
            await self.ensure_tab(hunter_id, url)
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
        if tid and self.bs is not None:
            try:
                await self.bs.cdp_client.send.Target.closeTarget(params={"targetId": tid})
            except Exception:
                pass

    async def shutdown(self):
        try:
            if self.server is not None:
                self.server.close()
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
