---
name: dalfox
category: injection
description: XSS 自动探测 + 利用——支持反射/存储/DOM XSS。Go 实现，业界 XSS 之于 sqlmap。LLM 已熟悉用法——本手册只列沙箱约束 + 写 finding 红线。
---

# dalfox 项目特定约束

## 沙箱环境

- **网络**：访问宿主用 `host.docker.internal`。
- **超时**：`-w 10` 并发 worker；单参数 ~10-30s，多参数线性增长。
- **输出**：`--format json` JSON 输出 + `-S`（silent）；`-o /tmp/dalfox.json` 写文件。
- **headless 限制**：DOM XSS 检测需要 chromium，沙箱**没装**——dalfox 自动降级到 reflected only，DOM 漏洞挖不到。

## 项目策略

- **基本调用**：`dalfox url 'http://host.docker.internal:4280/x?q=test' --format json -S`
- **POST/JSON body**：`dalfox url ... -X POST -d 'q=test'` 或 `-d '{"q":"test"}'` + `-H 'Content-Type: application/json'`
- **`pipe` 模式批量**：`echo url1 url2 | dalfox pipe --format json -S`
- **`--mining-dom` / `--mining-dict`**：默认开启的参数挖掘已够用；不必额外加
- **跳过 alert 验证**：默认不弹 popup（沙箱无浏览器），dalfox 看响应 reflect 模式判断
- **`--blind <interactsh-url>`**：blind XSS 联动 interactsh-client，注入 `<script src=//<interact>/x.js>`

## 写 finding 红线

dalfox 命中（输出含 `[VULN]` + `param` + `payload`）**直接构成 finding**——`evidence` 必含：
- 完整 url + 参数位置
- 完整 payload（dalfox 输出的 `payload` 字段，含 reflect 上下文标记如 `inAttr` / `inJS` / `inHTML`）
- 响应中 reflect 的字符串片段
- 评估等级（dalfox 给 `severity`：High = 可执行 JS / Medium = 可注入 HTML / Low = 仅 reflect）

## 决策边界（什么时候**不要**用 dalfox）

- 已经手工试出 XSS payload（`<svg/onload=alert(1)>` 等）→ 直接 curl 验证 + 写 finding
- 目标只接受 JSON 且无 reflection → dalfox 命中率低，看响应 + python3 手撸更准
- 想测 DOM XSS → 沙箱无 chromium，dalfox 也跑不了，写 LLM 看 JS 源码
- 多 step 复杂登录后才能测 → 先用 curl/python3 拿 cookie，再喂 dalfox `-C 'sessid=...'`
