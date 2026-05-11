---
name: katana
category: recon
description: 现代 Web 爬虫——爬一个起点 url 拿全部站内链接 + JS 解析的 endpoint。subfinder/httpx 之后的 attack surface 扩展利器。
---

# katana 项目特定约束

## 沙箱环境

- **网络**：bridge 出网，url 用 e2e 灌入的真实 host:port 即可。
- **超时**：默认深度 3、并发 10，单站 30-90s；可加 `-d 2` 减深度提速。
- **JS 解析**：默认 `-jc` 关；开启 `-jc` 会启 headless（沙箱无 chromium，`-jc` 会失败）→ **不开**，用 katana 只爬 HTML 链接。
- **输出**：`-jsonl -silent` 一行一个 url 最适合 LLM 后续 pipe httpx/nuclei。

## 项目策略

- **常用组合**：`katana -u http://<target>:4280 -d 3 -silent -jsonl`
- **避免污染目标**：`-no-sandbox`/`-headless` 沙箱不可用，跳过；只走 HTTP 静态爬。
- **去重**：默认开启 dedup；不需要再加 `-uniq`。
- 大型 SPA（React/Vue 单页应用）→ katana 静态爬抓不到 JS 路由 → 改用 LLM 看 JS 文件 + python3 + LinkFinder 思路手撸（沙箱没装 LinkFinder，LLM 用 grep 凑活）。

## 写 finding 红线

katana 输出 url 列表**不构成 finding**——是 input 给后续工具。把发现的 endpoint 喂 sqlmap/nuclei/dalfox 才能产 finding。

## 决策边界（什么时候**不要**用 katana）

- 流量已经定位到具体 url → 不需要爬，直接挖那条流量
- 目标是 API 后端（无 HTML 页面） → katana 抓不到东西，用 arjun/kiterunner 思路找 endpoint
- 只要单页 → curl 一次即可
