---
name: subfinder
category: recon
description: 子域名被动枚举（多源 OSINT）。LLM 已熟悉 ProjectDiscovery 用法——本手册只列沙箱约束 + 项目策略。零接触目标，只查公网情报源（crt.sh/passive DNS/...）。
---

# subfinder 项目特定约束

## 沙箱环境

- **API key**：默认只用免费源；要启用 censys/shodan/virustotal 等付费源需挂 `~/.config/subfinder/provider-config.yaml`，沙箱**没挂**——只用 `-all` 拿默认源结果。
- **网络**：完全出网（容器 bridge），无需 host.docker.internal。
- **超时**：`-timeout 10` 单源 ≤10s，整体走 run_command 300s 上限。
- **输出**：`-silent` 关 banner；`-oJ` JSON 一行一记录最适合 LLM 解析。

## 项目策略

- **必加 `-silent`**：banner 占 stdout 8KB tail 浪费空间。
- **首选 `-d <root-domain>`**——单域 enumerate；多域用 `-dL <file>` 但容器内拷文件麻烦，少用。
- **`-recursive` 仅在子域有子子域时（如 `*.cn.example.com`）**——大幅增加耗时，默认关。
- 目标是私有域名（非公网根域）→ 直接跳过 subfinder，无意义。

## 写 finding 红线

subfinder 单独输出**不构成 finding**——仅作 attack surface 扩展。把发现的子域名喂给 httpx 探活后，对活的子域跑 nuclei/sqlmap 才有可能产 finding。

## 决策边界（什么时候**不要**用 subfinder）

- 流量已经定位到具体 host → 不需要扩 attack surface
- 内网/本地靶场（如 DVWA `127.0.0.1`） → 公网情报源对内网为空
- 想找子域接管 → 用 nuclei `subdomain-takeover-*` 模板（subfinder 只发现，nuclei 验证）
