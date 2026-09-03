"""mitm-capture.py — mitmproxy addon：把 CLI 工具（curl/sqlmap/httpx/katana...）的 HTTP
流量捕获后 POST 到 /internal/v1/flows/ingest，入 agent_traffic 表（source=internal，按 task）。

设计（与 browser-svc.py CDP capture 同款模式，复用同一 ingest endpoint）：
  - 容器内跑 `mitmdump -s mitm-capture.py`，CLI 工具经 HTTP_PROXY/HTTPS_PROXY 走本代理
  - response 钩子构造与 cmd/proxy ingestRequest 一一对应的 JSON → POST ingest
  - 归属：agent_id 从容器 env 取（容器 per-run，owner 级归属足够）

fuzz/噪音治理（源头去重，避免 agent_traffic 膨胀 + 污染攻击面图）：
  - 内存维护 per-run 已见集合 (method, templatize(path))，重复直接不上报
    → sqlmap 喷 1 万 /x?id=1..10000 → 模板化 /x?id=:id → 仅首条入库
  - 静态资源（css/js/图片/字体/媒体）按 Content-Type / 扩展名丢弃（无凭证、无业务价值）

LIUSHA_INGEST_URL 空 → 整体不上报（passive-only / 无 cmd/proxy 部署 / 单测）。
"""

from __future__ import annotations

import base64
import os
import re
from datetime import datetime, timezone
from urllib.parse import parse_qs, urlsplit

import httpx
from mitmproxy import http

INGEST_URL = os.getenv("LIUSHA_INGEST_URL", "").strip()
INGEST_TOKEN = os.getenv("LIUSHA_INGEST_TOKEN", "").strip()
AGENT_ID = os.getenv("LIUSHA_AGENT_ID", "").strip()

# 静态资源扩展名（小写，无点）——这些请求不入字典（CAPTURE_TYPES 思路的 CLI 版）。
_STATIC_EXT = {
    "css", "js", "mjs", "map", "png", "jpg", "jpeg", "gif", "ico", "svg",
    "webp", "bmp", "woff", "woff2", "ttf", "otf", "eot", "mp4", "webm",
    "mp3", "wav", "avif",
}
# 静态/无价值 Content-Type 前缀。
_STATIC_CT_PREFIX = ("image/", "font/", "audio/", "video/", "text/css")

# 路径段模板化：纯数字段 → :id；长 hex（≥12，含 uuid 去横线）→ :id。
_RE_NUM = re.compile(r"^\d+$")
_RE_HEX = re.compile(r"^[0-9a-fA-F]{12,}$")


def _templatize(path: str) -> str:
    parts = []
    for seg in path.split("/"):
        s = seg.replace("-", "")
        if _RE_NUM.match(seg) or _RE_HEX.match(s):
            parts.append(":id")
        else:
            parts.append(seg)
    return "/".join(parts)


# CLI 工具默认 User-Agent 指纹 → 工具名（子串小写匹配）。给 agent_traffic.tool 盖工具戳，
# 让 LLM 能 list_flows(tool=...) 过滤。认不出（被 --random-agent / -A 伪装）统一归 'unknown'。
_TOOL_UA_PATTERNS = [
    ("sqlmap", "sqlmap"),
    ("nuclei", "nuclei"),
    ("katana", "katana"),
    ("dirsearch", "dirsearch"),
    ("ffuf", "ffuf"),
    ("wfuzz", "wfuzz"),
    ("nikto", "nikto"),
    ("nmap", "nmap"),
    ("python-requests", "requests"),
    ("python-httpx", "httpx"),
    ("httpx", "httpx"),
    ("go-http-client", "go-http"),
    ("wget", "wget"),
    ("curl", "curl"),
]


def _parse_tool(ua: str) -> str:
    """从 User-Agent 解析发起工具名；认不出统一归 'unknown'（不存原始 UA）。

    认不出的多是伪装成浏览器 UA 的工具（--random-agent / 默认浏览器 UA）。存原始 UA 会污染
    tool 字段语义（该字段契约是工具名，供 list_flows(tool=) 过滤），让筛选失效；且原始 UA
    本就在 request_headers 里没丢。故统一归 'unknown'，字段干净。
    """
    if not ua:
        return "unknown"
    low = ua.lower()
    for needle, name in _TOOL_UA_PATTERNS:
        if needle in low:
            return name
    return "unknown"


class CLICapture:
    def __init__(self) -> None:
        self._seen: set[tuple[str, str]] = set()
        self._http: httpx.Client | None = None
        self._enabled = bool(INGEST_URL) and bool(AGENT_ID)

    def _is_static(self, flow: http.HTTPFlow) -> bool:
        ext = urlsplit(flow.request.pretty_url).path.rsplit(".", 1)
        if len(ext) == 2 and ext[1].lower() in _STATIC_EXT:
            return True
        ct = (flow.response.headers.get("content-type", "") if flow.response else "").lower()
        return ct.startswith(_STATIC_CT_PREFIX)

    def response(self, flow: http.HTTPFlow) -> None:
        if not self._enabled or flow.response is None:
            return
        if self._is_static(flow):
            return
        # 丢 404/410（资源不存在/已删）——工具爆破的这类响应是纯噪音（实测占爆破流量 99%+），
        # 对下游零价值：sitemap 攻击面本就 WHERE status NOT IN (404,410)、replay/view 无人碰、
        # agent 从工具 stdout 已拿到汇总。与 sitemap 投影器同一套判据。保留 401/403/405/429/5xx
        # 等正信号（存在受保护/越权/限流/服务端错误——都是渗透线索）。
        if flow.response.status_code in (404, 410):
            return
        req = flow.request
        u = urlsplit(req.pretty_url)
        key = (req.method, _templatize(u.path or "/"))
        if key in self._seen:
            return  # 源头去重：该路由已上报过代表请求，fuzz/重复不再入库
        self._seen.add(key)
        try:
            self._post(self._build(flow, u))
        except Exception:
            pass  # 上报失败不影响代理转发（fire-and-forget）

    @staticmethod
    def _build(flow: http.HTTPFlow, u) -> dict:
        req = flow.request
        resp = flow.response
        req_body = req.raw_content or b""
        resp_body = resp.raw_content or b""
        dur_ms = 0
        if resp.timestamp_end and req.timestamp_start:
            dur_ms = int(max(0.0, resp.timestamp_end - req.timestamp_start) * 1000)
        return {
            "agent_id": AGENT_ID,
            "host": req.host or "",
            "host_port": f"{req.host}:{req.port}" if req.host else "",
            "method": req.method,
            "scheme": req.scheme or "",
            "uri": (u.path + ("?" + u.query if u.query else "")) or "/",
            "path": u.path or "/",
            "query": parse_qs(u.query) if u.query else None,
            "status_code": resp.status_code or 0,
            "request_headers": {k: v for k, v in req.headers.items()},
            "request_body": base64.b64encode(req_body).decode() if req_body else "",
            "response_headers": {k: [v] for k, v in resp.headers.items()},
            "response_body": base64.b64encode(resp_body).decode() if resp_body else "",
            # tool 从 UA 解析（identity 不传：CLI 透明代理标不准，且 CLI 凭证走 redis/自登不需要）
            "tool": _parse_tool(req.headers.get("user-agent", "")),
            "duration_ms": dur_ms,
            "timestamp": datetime.now(timezone.utc).isoformat(),
        }

    def _post(self, payload: dict) -> None:
        if self._http is None:
            self._http = httpx.Client(timeout=5.0)
        headers = {"Content-Type": "application/json"}
        if INGEST_TOKEN:
            headers["Authorization"] = "Bearer " + INGEST_TOKEN
        self._http.post(INGEST_URL, json=payload, headers=headers)


addons = [CLICapture()]
