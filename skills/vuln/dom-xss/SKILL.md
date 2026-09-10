---
name: DOM XSS 检测指南
description: DOM 型 XSS —— payload 在客户端 JS 的 source→sink 链里执行，服务端响应里看不到。判定必须用浏览器实际执行确认，curl 看反射不算数。
category: vuln
---

# DOM 型 XSS（DOM-based XSS）

## 漏洞本质

payload 全程在**客户端 JS** 里流动：从 **source**（攻击者可控输入）流进 **sink**（触发执行的 API），服务端从头到尾**不参与**。

- **常见 source**：`location.hash` / `location.search` / `location.href` / `document.referrer` / `window.name` / `postMessage` 数据 / `localStorage`
- **常见 sink**：`innerHTML` / `outerHTML` / `document.write` / `eval` / `setTimeout(str)` / `Function(str)` / jQuery `.html()` `.append()` / `location=` / `<script>.src=`

关键特征：**payload 经常压根不发给服务端**。比如 `http://t/page#<img src=x onerror=alert(1)>` 里 `#` 后的片段浏览器不发送，服务端日志/响应里完全看不到——这正是 reflected/stored XSS 与 DOM XSS 的分水岭。

## 为什么必须用浏览器执行确认（本类型最核心的红线）

**curl 看到 payload 被反射 ≠ DOM XSS 成立。** 两个独立理由：

1. **DOM XSS 的执行发生在浏览器 JS 运行时**，curl 不跑 JS——它最多证明"参数出现在响应文本里"，证明不了"sink 真的执行了脚本"。
2. **很多 DOM XSS 的 payload（`#` 片段 / 纯前端路由）根本不进服务端**，curl 拿到的响应里**连反射都没有**，却仍然可利用。

所以本类型 finding 的**唯一有效证据 = 浏览器里 payload 真实执行**。用 `browser_use` 走：

1. `browser_use open` 打开带 payload 的 PoC URL（payload 放进对应 source 位置：`#...` / `?param=...`）
2. **用 sentinel 验证执行**（不要依赖 alert 弹窗——headless 下 alert 会被自动 dismiss、阻塞、且不可断言）：
   - payload 用可被读回的副作用，如 `<img src=x onerror="window.__xss=1">` 或 `<svg onload="window.__xss=1">`
   - 再 `browser_use eval "window.__xss"` → 返回 `1` = sink 执行了脚本 = **确认命中**
   - 或 payload 注入一个可检测 DOM 节点（`document.title='XSSPWN'` / 插入特定 id 节点），用 `eval` / `source` 读回比对
3. 命中后立刻 `write_finding`（evidence 带上 eval 返回 sentinel 的输出 / 触发后的截图）

**反模式**：拿 curl 看到参数反射就 `write_finding` 写 DOM XSS——证据无效（没证明 JS 执行），且对纯 `#` 片段类根本测不到。

## 挖掘方向（思路，不是步骤）

- **先定位 source→sink 链**：`browser_use source` 拿渲染后 HTML / 内联脚本，或 `eval` 读 JS，找把 `location.*` / `referrer` 喂进 `innerHTML` / `document.write` / `eval` 的代码
- **DVWA「XSS (DOM)」类**：典型 `?default=<lang>` 被 JS 拼进 `document.write`/option——payload 走 query，但仍要浏览器执行确认（注意 source 位置，别用错参数名）
- **框架/SPA 路由**：hash 路由、`dangerouslySetInnerHTML`、`v-html`、模板里的 `{{{ }}}` 都是高危 sink
- **postMessage / window.name**：跨源消息未校验 origin 又进 sink → DOM XSS

## 判定原则

1. **执行确认是必要条件**：sentinel（`window.__xss` / DOM 变化 / title 改写）在浏览器里被实际触发，才算成立
2. **CSP 拦截要如实记**：payload 进了 sink 但被页面 CSP 挡住未执行 → **不成立**（exploit 受限），不要无视 CSP 报错硬写 finding
3. **编码即免疫**：payload 被 HTML 实体编码 / 进了不执行的上下文（纯文本节点、属性值被正确转义）→ 反射了但不执行 → **不成立**
4. **source 必须攻击者可控**：sink 里的数据来自服务端固定值 / 同源可信配置 → 不是 DOM XSS

## 误报排除

提交前过一遍：

- curl 看到反射就下结论（**最常见误报源**——没证明 JS 执行）
- payload 被实体编码 / WAF 过滤后进的页面（看 `source` 确认实际落地形态）
- CSP `script-src` 阻断了执行（控制台/eval 会报 CSP 错）
- sink 数据其实不可控（误把服务端值当成用户输入）

## write_finding 红线

- **CWE 统一 `CWE-79`**（reflected / stored / DOM 都是 79，dedup 依赖一致）
- **target.path 必填**（如 `"path": "/vulnerabilities/xss_d/"`）
- **evidence 必须含浏览器执行证据**：`eval` 返回 sentinel 的输出，或触发后的截图 / DOM 变化快照；标明 source 位置（hash / query / postMessage）与命中的 sink
- **repro 必须是浏览器可执行 PoC**：完整 PoC URL（payload 放对 source 位置）+ 浏览器执行步骤 / eval 验证命令。**禁止用 curl 当 repro_cmd**——curl 不跑 JS，对 DOM XSS 是无效复现
- summary ≤500 字单行，详情进 evidence jsonb
