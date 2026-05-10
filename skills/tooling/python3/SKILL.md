---
name: python3
description: 自定义脚本兜底——sqlmap/curl 搞不定的场景（GraphQL/NoSQL 注入、复杂登录链、并发 fuzz、二阶段 payload、自定义编码绕过）。LLM 已熟悉 Python——本手册只列沙箱关键约束。
---

# python3 项目特定约束

## 沙箱环境

- **网络**：容器内 `127.0.0.1` / `localhost` = 容器自己。访问 host 服务必须用 `host.docker.internal`。
- **第三方库**：镜像里**只有 stdlib**——没有 `requests` / `httpx` / `aiohttp` / `beautifulsoup4`。HTTP 请求只能 `urllib.request`。要装额外包 pipe `pip install --quiet --break-system-packages <pkg> &&`，但耗时（30-60s）+ 容器 300s 上限要权衡，能用 stdlib 优先 stdlib。
- **超时**：单次 run_command ≤ 300s。脚本里给每个 `urlopen` 加 `timeout=10`，别让网络挂死整个脚本。
- **资源**：512MB RAM / 1 CPU；并发用 `ThreadPoolExecutor(max_workers=10)` 上限。
- **文件系统**：`--rm` 容器，`/tmp` 写入退出即丢。**不要依赖临时文件**作中间状态。
- **SSL**：自签名 host 用 `ssl.create_default_context(); ctx.check_hostname=False; ctx.verify_mode=ssl.CERT_NONE` 显式跳。

## 写 finding 红线

`evidence.repro_cmd` 把整段 `python3 -c '...'` 放进去，包含**所有 import + 完整 payload**。脚本里的 url 必须用宿主可访问形式（`127.0.0.1:4280`），不是容器内 `host.docker.internal`——前者才是漏洞真实坐标。

## 决策边界（什么时候**不要**写 python3）

- 单次 HTTP 验证 → `curl -i` 一行更快
- SQL 注入 → sqlmap 覆盖 90% 场景，先它
- 简单 JSON 字段提取 → `jq` 一行更准
