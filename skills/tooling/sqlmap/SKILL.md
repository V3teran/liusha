---
name: sqlmap
category: injection
description: SQL 注入自动探测/利用。LLM 已熟悉 CLI——本手册只列沙箱环境约束 + 项目策略 + 写 finding 红线 + 决策边界。常见 web SQLi 首选；GraphQL/NoSQL 不归它管。
---

# sqlmap 项目特定约束

## 沙箱环境

- **网络**：容器内 `127.0.0.1` / `localhost` = 容器自己，**不是宿主**。访问 host 服务必须把 url 里的 `127.0.0.1` / `localhost` 替换为 `host.docker.internal`（已在容器 hosts 文件注入）。
- **超时**：单次 run_command ≤ 300s。`--level 5` + `--technique=BEUSTQ` 跑不完——拆步走（先 `-p <param> --level 3`，命中再升）。
- **资源**：512MB RAM / 1 CPU。`--threads` 别超 4。
- **输出**：stdout/stderr 各只保留尾 8KB。`--dump` 大表前先用 `--count` 看大小，别拖全表。

## 项目策略（必须遵守）

- **`--batch` 必加**——无人值守，自动选默认答案；不加会卡在 y/n 提示里直到超时。
- **禁用** `--os-shell` / `--os-pwn` / `--file-write`——沙箱无意义且耗时，不要尝试。
- 已知 DBMS 时加 `--dbms=<name>` 跳过指纹，省 30-60s。
- 命中 WAF（403）才上 `--tamper=...`，默认不加。

## 写 finding 红线

`evidence` 必须含**完整 sqlmap 输出片段**（带 `Parameter:` / `Type:` / `Payload:` 三行），不要只写 "SQLi found"。`url` 用宿主可访问形式（即 `127.0.0.1:4280` 这种 e2e 灌入的原 url），不是容器内的 `host.docker.internal:4280`——前者才是真实漏洞坐标。

## 决策边界（什么时候**不要**用 sqlmap）

- GraphQL 注入 → 写 `python3` 脚本手撸（sqlmap 不懂 GraphQL）
- NoSQL（MongoDB/ES）`$ne`/`$gt` 注入 → `python3` + json payload
- 二阶段注入（写入后另一接口触发）→ sqlmap 看不到二级响应，手工 curl 多步
- 单参数纯 baseline 探测 → `curl -i` 一行更快
