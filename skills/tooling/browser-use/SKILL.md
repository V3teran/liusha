---
name: browser-use
description: 无头 Chromium 浏览器自动化，typed `browser_use` 工具是首选入口。
---

# browser-use 使用手册

## 架构

CLI 包装 playwright，多 task 安全：
- **共享 chromium daemon**：cookies / session 跨 task 共享，登录不顶掉
- **每 task 独立 tab**：截图/浏览历史独立，commander/striker 并发不互覆（按 TASK_ID 切 tab）

## 入口选择

**优先用 typed `browser_use` 工具**（单工具 + action 枚举），而不是 `run_command "browser-use ..."`。
原因：typed JSON 比 raw shell 命令字符串错率低，且状态变化 action wrapper 自动附截图喂回 vision LLM。

仅以下场景才走 `run_command` 通道（typed 工具未覆盖的低频子命令）：
`select / scroll / back / keys / hover / dblclick / rightclick / python / get / close / cookies {get|set|clear|import|export} / screenshot`

## action 补充（schema 没说清的差异）

完整 action 列表 + 字段说明见 `browser_use` tool schema。本节只记 schema 不便说的实战要点：

- **open**：chromium 首次 cold start ~25-30s，**第 1 个命令 `timeout_seconds` 至少 60**；后续 session 复用 15s 够
- **state 先于 click**：LLM 直接给 click x/y 偏差通常 ±50 像素，足以错过按钮。**先 state 拿 numbered DOM → 再 click index=N**，比 vision 猜稳得多
- **input index 一步到位**：`{action:"input", index:N, text:"..."}` 内部已处理"先 click 拿焦点 + type"两步；x/y 兜底路径才是分两步
- **source vs eval HTML**：拿渲染后 HTML 用 `source`（原生 `get html`），不要 `eval document.documentElement.outerHTML`（JS 字符串编码风险）
- **reset 是核选项**：所有 tab 含登录态会丢失。**仅在反复 click timeout / open 都卡时调**，不要日常用

## 子命令自动附图规则（wrapper 内）

| 命令类型 | 行为 |
|---|---|
| **状态变化**（open / click / input / type / select / scroll / back / keys / hover / dblclick / rightclick / wait）| 跑完自动 screenshot 到 `$OUTPUT_DIR/auto_<ns>.png`，LLM 立即看到视觉 |
| **读取/特殊**（state / extract / eval / get / cookies / close / python）| **不附图**（已返文本/无视觉变化） |
| `screenshot` 子命令 | 显式调用，自动附图失败的 fallback；路径必须写到 `$OUTPUT_DIR/xxx.png` 才能进 LLM 上下文 |

## Cookie 同步（curl → chromium 单向需要显式）

**browser-use 和 curl 是独立 cookie jar**。chromium 的 cookie store 由 CDP/Playwright 内部管理，curl 发的请求不经过 chromium，chromium 不会自动持有 curl 拿到的 cookie，必须显式注入：

```bash
browser-use cookies import /tmp/cookies.json   # Playwright JSON 格式
browser-use cookies set name=PHPSESSID value=xxx domain=target.com  # 单条
```

**反向（chromium → curl）不需要做 cookie 同步动作** —— chromium 操作的 HTTP 请求自动入流量字典（hunter_id 关联），直接 `list_flows host=target` 找登录响应 → `view_flow id=N` 看 Set-Cookie header，比 `cookies get` 多步转格式优雅得多。

### 反模式

**不要**用 `eval document.cookie=...` 同步——**HttpOnly cookie 注入不了**（JS 看不到），会卡 20+ 步。

## 何时用 / 何时不用

**适合 browser_use 的场景**：
- 必须 JS 执行才看到的页面（SPA / React / Vue）
- 复杂登录链（OAuth redirect / SAML / 多步表单）
- DOM-based XSS 验证（payload 在 DOM 中执行 vs 仅 reflect）
- 视觉验证 finding（截图作证据）

**不适合的场景**：
- 静态页面 HTTP 请求 → 用 curl（快 10x）
- 大批量 endpoint 探测 → 用 httpx / katana
- 已知 endpoint 的注入测试 → 用 sqlmap / dalfox / curl + payload

## 失败排查

| 症状 | 可能原因 | 处理 |
|---|---|---|
| `click x,y` timeout 20-30s | LLM 猜的坐标错位，chromium 等不到事件 | 换 `state` + `click index=N` |
| 反复 click 都 timeout | chromium daemon 进 hung 状态 | 显式调 `reset` 重启 |
| open 60s timeout | 第 1 个命令 cold start 没等够 | 单次重试 + 加大 timeout |
| 拿 cookie 失败 | HttpOnly cookie 用 `eval document.cookie` 拿不到 | 用 `cookies get` 子命令（CDP 层抓） |
