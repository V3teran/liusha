---
name: dirsearch
category: discovery
description: 智能目录/文件爆破——自带 11k+ 行内置 wordlist、扩展名探测、smart filter。比 ffuf 配置少，省 token，目录爆破首选。
---

# dirsearch 项目特定约束

## 沙箱环境

- **网络**：访问宿主用 `host.docker.internal`。
- **超时**：默认 `-t 30` 线程；可降到 `-t 10` 防 WAF 触发。整体走 run_command 300s 上限。
- **输出**：`--format=json -o /tmp/out.json` JSON 给 LLM；不加 `-q` 时 banner 大，记得加 `-q` quiet。

## 项目策略

- **基本调用**：`dirsearch -u http://host.docker.internal:4280 -q --format=json -o /tmp/dirsearch.json` 然后 `cat /tmp/dirsearch.json | jq '.results[] | select(.status<400)'`
- **`-e <ext>` 扩展名**：探 `.php`/`.bak`/`.sql`/`.zip`/`.env` 等可能泄露文件 → 默认列表已含常见扩展
- **`-w <custom-wordlist>`**：要更针对场景时用 SecLists（如 `/opt/SecLists/Discovery/Web-Content/api/objects.txt` 找 API endpoint）
- **`--exclude-status=404,403`** 减噪
- **`-r` 递归**：默认关；爆出 `/admin/` 后单独再扫一次更准

## 写 finding 红线

发现敏感路径（`.git/config` / `.env` / `/admin/` / `backup.sql`）**直接构成 finding** —— `evidence` 必含 url + status + Content-Length + 用 curl 复跑确认（dirsearch 自带 wildcard filter 但仍可能误报）。

## 决策边界（什么时候**不要**用 dirsearch）

- 复杂 fuzz（参数/vhost/多占位符）→ 用 ffuf
- 已知 SPA/前后端分离 → 路径在 JS 里，dirsearch 抓不到 → 用 katana 爬 + LLM 看 JS
- 流量已经命中具体 endpoint → 不必爆，专心挖那条流量
