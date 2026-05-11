---
name: xss
category: web
description: 跨站脚本（reflected / stored / DOM）——payload 必须在响应中以"可执行 context"出现才算确认。沙箱无 chromium，DOM XSS 需降级方案。
---

# 跨站脚本（XSS）

## 漏洞本质

应用把用户输入**直接放进响应内容**未做 context-aware 转义，导致前端执行恶意脚本：

- **Reflected XSS**：单次请求—响应链路里 payload 立即回显（URL/POST 即时）
- **Stored XSS**：payload 持久化到 DB，下次任意用户查看时触发（评论/留言/资料）
- **DOM XSS**：服务端响应无变化，纯客户端 JS 把 source（`location.hash` / `document.URL` 等）写入 sink（`innerHTML` / `eval` / `document.write` 等）

## 挖掘方向（思路，不是步骤）

**唯一铁律**：验证 XSS 成功 = **payload 在响应中以可执行 context 出现**（HTML body 未被 entity-encode / 在 attribute 中能 break out / 在 JS string 中能逃逸）。**仅看 payload 字串出现在响应里不够**——可能在 `<textarea>` / `<noscript>` / HTML 注释 / 已 entity-encode 的位置，那不执行。

**沙箱无 chromium**：DOM XSS 自动化测试受限——`dalfox` 自动降级到 reflected only。DOM XSS 需要降级方案（手工 source/sink 分析）。

下面是倾向性建议，不是规则：

- 流量参数原值已在响应中回显 → 高概率 reflected XSS
- 流量是 POST + 表单字段（如 `name=` / `message=`）→ 高概率 stored XSS（需 POST 后 GET 验证）
- 响应里有大量 inline JS / `innerHTML` 等 sink → 高概率 DOM XSS
- 响应 CSP header 严格（`script-src 'self'`）→ 即使有 XSS 也可能被 CSP 阻执行

## dalfox 协同（沙箱已装，优先用）

```sh
dalfox url 'http://<target>/path?param=test' --format json -S
```

dalfox 自动测 reflect context + 推荐 context-aware payload。

**dalfox 限制**：

- DOM XSS 需要 chromium（沙箱没装）→ dalfox 自动降级到 reflected only
- Stored XSS：dalfox 不主动测"POST 提交后 GET 查看"链路 → 需手工补
- 复杂 cookie/header / 自定义 body 格式 → 手工 curl 更灵活

完整工具用法参考 `read_tooling_skill(name="dalfox")`。

## 判定原则（按优先级）

### 1. payload 在响应可执行 context → XSS 确认（high）

**可执行 context**（payload 注入到这些位置且未被防御）：

- HTML body（直接 `<script>...</script>` 或 `<img src=x onerror=...>`）
- HTML attribute 未引号包裹或未 entity-encode（`<input value=XXX>` 注入 `" onmouseover="alert(1)`）
- JavaScript string（`var x="XXX"` 注入 `"; alert(1); //`）
- URL attribute（`href="XXX"` 注入 `javascript:alert(1)`）

### 2. Stored XSS：POST 提交后 GET 查看仍能执行 → 同等级 high

测试链路（**两段**必须都跑）：

1. POST 提交 payload（如 `<script>alert('pwn')</script>` 到 message/comment 字段）
2. GET 查看该资源/列表页 → payload 出现在响应可执行 context

dalfox 不主动跑这个链路，需手工 curl POST + curl GET。

### 3. DOM XSS：source/sink taint flow 完整（沙箱无 chromium 时降级方案）→ medium-high

降级方案：用 curl 拉响应 HTML，grep JS：

- **source**：`location.hash` / `location.search` / `document.URL` / `document.referrer` / `window.name`
- **sink**：`innerHTML` / `outerHTML` / `eval(` / `document.write(` / `setTimeout(...string...)` / `setInterval(...string...)` / jQuery `$(...)`

如果某 source 直接流入 sink 且无 sanitize → DOM XSS 嫌疑。但**无 chromium 无法实证执行**，finding severity 降一档（medium-high）+ evidence 标注"无浏览器执行验证"。

### 4. payload 被 HTML entity-encode（`<` → `&lt;`）→ 防护正常

服务端做了基础 entity-encode → 大部分 payload 失效。但仍可试：

- attribute 内的 unicode 转义绕过
- 非常规 tag（`<svg>` / `<math>` / `<details>`）
- 事件 handler（`onmouseover` / `onfocus`）

### 5. payload 在 `<textarea>` / `<noscript>` / HTML 注释 → 不执行，非漏洞

这些 context 里 `<script>` 不会被解析为标签。除非能 break out（`</textarea>` / `--><script>`）才有戏。

## payload context 表

| context | payload |
|---|---|
| HTML body | `<script>alert(1)</script>` / `<img src=x onerror=alert(1)>` / `<svg/onload=alert(1)>` |
| HTML attribute（双引号包裹） | `"><script>alert(1)</script>` / `" onmouseover="alert(1)` |
| HTML attribute（单引号包裹） | `'><script>alert(1)</script>` / `' onmouseover='alert(1)` |
| HTML attribute（无引号） | ` onmouseover=alert(1)` |
| JS string（双引号） | `";alert(1);//` |
| JS string（单引号） | `';alert(1);//` |
| URL attribute（href） | `javascript:alert(1)` |
| CSS context | `expression(alert(1))`（IE only） |
| JSON 响应 | `</script><script>alert(1)</script>`（XSS via JSON） |

## 误报排除

- payload 被 HTML entity-encode（响应中 `&lt;script&gt;`）
- payload 在 `<textarea>` / `<noscript>` / HTML 注释里
- 响应 CSP header 阻止 inline script（`script-src 'self'`）→ XSS 存在但无法利用，severity 降级
- payload 只显示但不执行（如 `value` 属性内）→ 需 break out 才有效
- payload 被 sanitizer 改写（如 `<script>` → `<scr1pt>`）→ 防护正常

## write_finding 红线

evidence 必须包含：

- **完整 payload**（含 context-specific break-out）
- **响应 HTML 片段**：明确显示 payload 注入到响应的**哪个 context**（用 `<<<HERE>>>` 标注位置）
- **payload 类型**：reflected / stored / DOM
- **stored 路径**：POST 请求 + GET 验证两段 curl 命令
- **DOM 路径**：source/sink taint flow 分析（若沙箱无浏览器实证，标注"未浏览器验证"）
- **CSP 状态**：有 CSP 时附响应 CSP header，说明是否阻 execution

summary ≤500 字单行：`Reflected XSS in GET /vulnerabilities/xss_r/?name= — HTML body context, no entity-encode`
