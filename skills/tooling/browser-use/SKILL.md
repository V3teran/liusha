---
name: browser-use
description: 无头 Chromium 浏览器自动化，typed `browser_use` 工具是首选入口。
category: utility
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

## 9 个 typed action 详解

### open
导航 URL。chromium 首次 cold start ~25-30s，**第 1 个命令 `timeout_seconds` 至少 60**；后续 session 复用 15s 够。

```json
{"action": "open", "url": "http://target/login.php", "timeout_seconds": 60}
```

### state（推荐先调）

返回当前页 numbered DOM 清单（`[1]<a>X</a> [2]<button>Login</button>...`）+ viewport 尺寸。

**先调 state 拿 element index，再 click/input index=N，比 vision 猜 x/y 稳得多**。LLM 给坐标偏差通常 ±50 像素，足以错过按钮。

```json
{"action": "state"}
```

### click / input

```json
{"action": "click", "index": 5}                    // 推荐
{"action": "click", "x": 638, "y": 410}            // 兜底（vision 不准易 timeout）

{"action": "input", "index": 7, "text": "admin"}   // 推荐
{"action": "input", "x": 100, "y": 200, "text": "..."}  // 兜底
```

### wait
等条件：秒数（`"3"`）/ CSS selector（`"#main"`）/ `"networkidle"`。

### eval
在当前页执行任意 JS：

```json
{"action": "eval", "code": "document.querySelector('button').click()"}
{"action": "eval", "code": "Array.from(document.querySelectorAll('a')).map(a=>a.href)"}
```

### extract
LLM 抽页面数据（如 "抽出所有商品价格"）。

```json
{"action": "extract", "query": "抽出所有外链 URL"}
```

### source
拿当前页渲染后**完整 HTML**（看 selector / 分析 DOM 结构）。比 `eval document.documentElement.outerHTML` 更直接无编码风险。

### reset（异常恢复）

强杀 chromium daemon 重启。**反复 click timeout / 卡死场景**显式调。
**副作用**：所有 tab 含登录态丢失，LLM 自决何时调（不要随便 reset）。

```json
{"action": "reset"}
```

## 子命令自动附图规则（wrapper 内）

| 命令类型 | 行为 |
|---|---|
| **状态变化**（open / click / input / type / select / scroll / back / keys / hover / dblclick / rightclick / wait）| 跑完自动 screenshot 到 `$OUTPUT_DIR/auto_<ns>.png`，LLM 立即看到视觉 |
| **读取/特殊**（state / extract / eval / get / cookies / close / python）| **不附图**（已返文本/无视觉变化） |
| `screenshot` 子命令 | 显式调用，自动附图失败的 fallback；路径必须写到 `$OUTPUT_DIR/xxx.png` 才能进 LLM 上下文 |

## Cookie 同步（与 curl 互通）

curl 登录拿到 cookies 后**一行注入**：

```bash
browser-use cookies import /tmp/cookies.json
# 单条
browser-use cookies set name=PHPSESSID value=xxx domain=target.com
```

**不要**用 `eval document.cookie=...` 同步——HttpOnly cookie 注入不了，会卡 20+ 步。

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
