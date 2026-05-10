---
name: nuclei
category: vulnscan
description: 模板化漏扫引擎——业界事实标准。10k+ 模板覆盖 CVE/misconfig/exposure/dast。LLM 已熟悉用法——本手册只列沙箱约束 + 高 ROI 模板筛选策略。
---

# nuclei 项目特定约束

## 沙箱环境

- **网络**：访问宿主用 `host.docker.internal`。
- **超时**：默认全模板跑 5-10min；必加 `-timeout 5 -retries 1` 单请求钳。
- **模板路径**：`/root/.config/nuclei-templates`（镜像构建时已 update-templates 预拉）。
- **输出**：**必加 `-jsonl -silent`**——一行一记录 JSON，LLM 解析最快。`-no-color` 也加（沙箱无 tty）。
- **资源**：`-c 25` 默认并发；防 DoS 目标可降到 `-c 5`。

## 项目策略

- **首选窄过滤**——10k 模板全跑等于 DoS 自己：
  - `-severity high,critical` 只跑高危
  - `-tags <category>` 按场景筛：`cve,sqli,xss,rce,ssrf,lfi,xxe,exposure,misconfig`
  - `-id <template-id>` 已知特定漏洞时直接命中
- **常用组合**：`nuclei -u http://host.docker.internal:4280 -severity high,critical -jsonl -silent -timeout 5`
- **自带探活**：`-l urls.txt` 内置 HTTP 探活，无需先 httpx
- **interactsh 联动**：`-interactsh-url https://oast.fun` 启自家 OOB（默认就是这域名），blind ssrf/xxe/rce 需要

## 写 finding 红线

nuclei 命中（`info.severity in [high,critical]` + `template-id` + `matcher-name`）**直接构成 finding**——`evidence` 字段：
- `info.name` / `info.severity` / `info.tags`（CVE id 在 tags 里）
- `matched-at` URL
- `extracted-results`（如果模板带 extractor）
- `request` / `response`（带 `-include-rr` 才输出）

**误报检查**：medium 及以下手动 curl 复跑确认；info 级直接忽略（多是指纹）。

## 决策边界（什么时候**不要**用 nuclei）

- 需要深度交互/手工 payload → 用 sqlmap/dalfox/curl
- 单 endpoint 深挖业务逻辑 → nuclei 是广度工具，不是深度
- 已知漏洞类型且有专项工具 → 用专项工具更准（如 SQLi 用 sqlmap）
- 全模板乱跑 → 浪费 10min，必窄过滤
