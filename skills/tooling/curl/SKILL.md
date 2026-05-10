---
name: curl
description: 原生 HTTP 客户端。LLM 完全熟悉——本手册只列沙箱网络约束 + 写 finding 红线。所有漏洞验证的瑞士军刀，sqlmap/python3 之前先用 curl 探 baseline。
---

# curl 项目特定约束

## 沙箱环境

- **网络**：容器内 `127.0.0.1` / `localhost` = 容器自己，**不是宿主**。访问 host 服务（如 e2e 灌入的 `Host: 127.0.0.1:4280`）必须把 url 替换为 `host.docker.internal:4280`。
- **超时**：单次 run_command ≤ 300s。批量探测（`for i in $(seq 1 100); do curl ...`）小心总时长——拆多次 run_command。
- **TLS**：自签名证书加 `-k` 跳过校验（沙箱不维护 CA bundle）。
- **输出**：stdout 只保留尾 8KB，大响应用 `--max-filesize 100k` 钳制或 pipe `head -c 4096`。

## 写 finding 红线

`evidence.repro_cmd` 必须是**别人 copy 就能复现的完整 curl 行**（含 `-i`/`-X`/`-H`/`-d`/`-b` 全部参数）。复现命令里的 url **必须用宿主可访问形式**（`127.0.0.1:4280`），不是容器内 `host.docker.internal:4280`——前者才是漏洞真实坐标。

## 决策边界（什么时候**不要**用 curl）

- 复杂状态机（登录 → 拿 token → 多步操作）→ `python3` + `urllib` Session 更清晰
- 大批量 fuzz（>50 次）→ `python3` + `concurrent.futures` 并发
- JSON 响应字段提取 → pipe 给 `jq`，别自己 grep
