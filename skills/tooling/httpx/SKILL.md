---
name: httpx
category: recon
description: HTTP 探活 + 指纹 + tech detection。给 url 列表 → 输出哪些活/状态码/title/server/tech-stack。subfinder 之后必跑这步过滤死域。
---

# httpx 项目特定约束

## 沙箱环境

- **网络**：完全出网，访问 host 服务用 `host.docker.internal`。
- **超时**：`-timeout 10` 单 url，`-rate-limit 100`/秒；列表 1k+ url 时拆 run_command。
- **输出**：**必加 `-json` + `-silent`**——LLM 解析 line-delimited JSON 比文本表格快 10 倍。

## 项目策略

- **常用组合**：`httpx -l urls.txt -silent -json -title -server -tech-detect -status-code -content-length`
  - 一次拿到完整指纹，不用多次跑
- **`-follow-redirects` 默认关**——开了会把 30x 算成最终 200，掩盖真实 status
- **`-mc 200,301,302,401,403`** 过滤状态码；`-fc 404` 排除死链
- 直接喂 `-u <single-url>` 也行，但单 url 用 curl 更轻

## 写 finding 红线

httpx **指纹**（Server: Apache/2.4.49）可作 finding 的 `evidence` 一部分（如指纹+CVE 关联），但单独**不构成 finding**——仅是 enrichment。CVE 验证要跑 nuclei。

## 决策边界（什么时候**不要**用 httpx）

- 单 url 探活 → `curl -sI` 一行更快（httpx 启动 ~200ms 开销）
- 已知目标 alive → 直接跑漏扫
- 想跑 nuclei → nuclei `-l urls.txt` 自带探活，不必先 httpx
