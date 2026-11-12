---
name: Browser Use 浏览器自动化
description: 沙箱内真实 chromium 自动化，typed `drive_browser` 工具是唯一入口；每身份独立浏览器、每 agent 独立 tab，交互流量自动进流量字典。
category: tool
---

# browser-use 使用手册

## 架构

每身份一个常驻浏览器服务托管 chrome（自启自管，跨调用状态保持）：

- **identity = 身份（cookie jar 边界 = 独立 chromium）**：同一身份下所有执行单元（并行 action）**共用一个浏览器**，登录态/cookies 共享。每个身份一个独立的浏览器服务，任务结束统一清理。
- **tab = agent**：服务按 `AGENT_ID`（工具层自动注入，无需你传）把命令路由到各自的 tab，页面/编号快照/并发互不干扰——同身份多 agent 并发各操作自己的 tab，无全局 active tab、无串行等待。
- **多身份**：越权/BAC 要多账号对比时，给 `drive_browser` 传不同 `identity`（= 账号名，如 `admin`/`lowpriv`）→ 各开一个**独立浏览器**（独立 cookie jar）。缺省走共享的 `default` 身份，无需传。
- **交互流量自动入字典**：浏览器服务内建 CDP Network 抓流，Document/XHR/Fetch 请求自动回灌流量库（`list_traffic` 可查，`tool=browser`、`identity=身份名`）——登录 POST、带凭证的 XHR 都会被抓下，`view_traffic` 连 httpOnly cookie 头都看得到（取舍：每身份**首个导航 GET 可能漏抓**，后续请求不漏；BAC 要的带凭证请求都在）。

## 入口选择

**一律用 typed `drive_browser` 工具**（16 个 action 全集：open / state / click / input / type / select / hover / dblclick / rightclick / scroll / back / keys / wait / eval / get / screenshot），不要 `run_command "browser-use ..."` 拼 shell。

仅以下子命令 typed 工具未覆盖、才走 `run_command`（`IDENTITY=... browser-use <sub>` 形态）：

- `extract`：结构化文本抽取
- `reset`：重启本身份浏览器会话（见失败排查；正常由 wrapper 自动自愈，别日常调）
- `release-tab`：释放本 agent 的 tab
- `python` / `install` / `doctor` 等管理类（透传底层 CLI）

注意：**没有 `cookies`、`close` 子命令**。要浏览器登录态给 curl 链路用？走流量字典：`view_traffic` 读该身份被 CDP 抓下的请求头（含 httpOnly Cookie）→ `write_credential` 录成命名身份 → `http_request` 传同值 `identity`（详见 vuln/bac skill「模态互通」）。

## action 补充（schema 没说清的差异）

完整字段说明见 `drive_browser` tool schema。本节只记 schema 不便说的实战要点：

- **单命令超时固定 120s（无需也无法传超时参数）**：chromium 首次 cold start ~20-30s 已被覆盖；注册表级上限 180s。
- **state 先于 click**：LLM 直接给 click x/y 偏差通常 ±50 像素，足以错过按钮。**先 state 拿 numbered DOM → 再 click index=N**，比猜坐标稳得多
- **index 失效后重新 `state`、别复用旧 N**（编号来自最近一次 state 快照）：除导航/点击触发重渲染外，SPA 路由、shadow DOM 变化也会让编号错位；同一元素反复 `not found` 就改 `eval` 走稳定 CSS selector（如 `document.querySelector('form').submit()` / `button[type=submit].click()`），别死磕 index
- **input index 一步到位**：`{action:"input", index:N, text:"..."}` 内部已处理"先 click 拿焦点 + type"两步；`type` 是对当前焦点元素输入
- **拿渲染后 HTML 用 `get`（html/title）**，不要 `eval document.documentElement.outerHTML`（JS 字符串编码风险）
- **你看不到截图**：状态变化 action（open/click/input/type/select/scroll/back/keys/hover/dblclick/rightclick/wait）成功后服务**自动存截图**到 `$OUTPUT_DIR/auto_*.png`——但那是操作员工件与 finding 证据（工作空间页可见），**不进你的上下文**。你的"看"= `state`（渲染后带编号 DOM）与 `get html` 的文本
- **identity 默认不传**：常规挖洞共用默认身份即可；**只有越权/BAC 需要"以 A 身份 vs 以 B 身份分别访问同一资源"对比时**才传不同 identity（判断逻辑见 vuln/bac skill）
- **reset 是核选项**：停掉**本身份**会话、所有 tab 含登录态丢失、不影响其它身份。仅在该身份反复超时且自动自愈也没救时调（见下）

## 自动自愈（wrapper 内建，先于你感知）

`drive_browser` 的 Go wrapper 会检测浏览器 daemon 的退化态签名（watchdog 超时类错误）并自动两级恢复：L1 `reset` → 仍退化则 L2 杀 daemon + chromium 冷启 → 重试原命令，恢复过程随结果回放日志。**你通常不需要手动 reset**——只有结果里带自愈日志且仍持续失败时才手动 `run_command "IDENTITY=... browser-use reset"`。

## 浏览器登录（独立有状态会话，不从 redis 注入 cookie）

chromium 的 cookie jar 按 identity 持久（服务生命周期内），与 curl 链路**独立**。浏览器登录走**登录页输账号密码**这一条路：

- **同一身份只登一次**：同 identity 下所有执行单元共用一个浏览器。**任一 agent 在登录页登录过后，整个身份的 jar 就有态**——后续同身份 agent 直接 `drive_browser open` 受保护页即带登录态，无需各自重登。
- **没人登过 → 自己在登录页登录**：`state` 拿表单 numbered DOM → `input` 填账密 → `click` 提交。这对浏览器是**正确路径**，不是重复劳动。
- **多账号对比（越权/BAC）**：brief 给几组账号就传几个不同 `identity` 各开一个独立浏览器，每个各自登录，cookie jar 互不污染。
- **浏览器 → curl**：见上文「入口选择」——经流量字典 + `write_credential`，不存在反向（redis → 浏览器）注入。

### 反模式

- ❌ **同身份每个执行单元各自重登**——第一个登过后 jar 全身份共享，后续直接 `open` 即可。
- ❌ **找 `cookies get` 导出登录态**——该子命令已不存在；CDP 抓流已经把带凭证请求（含 httpOnly cookie）落进字典，`view_traffic` 即得。
- ❌ **把浏览器当默认 HTTP 客户端**——纯请求级操作用 `http_request`（快一个量级、可注入凭证、可精确重放）；浏览器只用于 JS 渲染 / 复杂登录链 / 需要真实交互的场景（何时用见 tool schema 使用时机段）。

## 失败排查

| 症状 | 可能原因 | 处理 |
|---|---|---|
| `click x,y` timeout | LLM 猜的坐标错位，chromium 等不到事件 | 换 `state` + `click index=N` |
| `Element index N not found` | state 后页面变了，编号失效 | 重新 `state` 拿新 index（别复用旧 N）；反复失效改 `eval` 走 CSS selector |
| 反复 click / open 都 timeout，结果含 `[自愈]` 日志 | daemon 退化，自动恢复也没救 | 手动 `run_command "IDENTITY=<身份> browser-use reset"` 重启该身份会话 |
| 首次 `open` 等待很久 | chromium cold start（20-30s） | 正常，120s 超时内会完成；无需任何参数 |
| 需要 cookie 值但 `eval document.cookie` 拿不到 | HttpOnly cookie 对 JS 不可见 | 用流量字典：`list_traffic`（tool=browser）→ `view_traffic` 看请求头 |
