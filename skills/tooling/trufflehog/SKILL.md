---
name: trufflehog
category: sast
description: Secrets 扫描——专精在源码 / git 历史 / 文件系统 / S3 bucket 找硬编码 API key / token / 密码 / 证书。命中后自动验证 secret 是否仍生效。
---

# trufflehog 项目特定约束

## 沙箱环境

- **网络**：本地扫描无需网；`--only-verified` 模式会向 cloud provider 发请求验 secret 有效性，需出网。
- **超时**：扫小项目 ~5-10s；扫 git 全历史 30s-2min。
- **输出**：**必加 `--json`**——一行一记录 JSON，含 `DetectorName` / `Verified` / `Raw` / `SourceMetadata`。
- **`/opt/SecLists`**：trufflehog 不依赖 wordlist，自带 700+ 检测器（GitHub PAT / AWS / Stripe / Slack / GCP / etc.）。

## 项目策略

### 扫源码 / 文件系统

```sh
# 扫单文件夹
trufflehog filesystem /work/src --json --only-verified

# 扫 git 仓库（含全部历史 commit）
trufflehog git file:///work/repo --json --only-verified

# 扫远程仓库
trufflehog git https://github.com/org/repo --json --only-verified
```

### 扫 S3 / GCS bucket（公开 / 已拿凭证）

```sh
trufflehog s3 --bucket=public-bucket --json --only-verified
```

### 关键 flag

- **`--only-verified`** —— **必加**，过滤未验证的 secrets（极大降噪声；trufflehog 会真去 cloud API 验 token 是否还有效）
- **`--no-update`** —— 跳过版本检查，沙箱内省时间
- **`--include-detectors=<list>`** / **`--exclude-detectors=<list>`** —— 限定/排除检测器（默认 700+ 全跑）

## 写 finding 红线

trufflehog 命中且 `Verified: true` 的 secret **直接构成 critical finding**（凭证泄露 + 实际可用 = 立即可滥用）。`evidence` 必含：
- `DetectorName`（如 `AWS`、`GitHubPAT`、`Stripe`）
- `Verified: true`（关键证据，未验证的别写）
- `SourceMetadata`（文件路径 + 行号 + commit hash if git）
- secret 前 6 字符 + `***`（脱敏；完整 secret 私下记录）
- 影响评估（cloud / payment / 生产 / 测试）

## 决策边界（什么时候**不要**用 trufflehog）

- 没有源码 / 文件系统访问 → 黑盒挖不出来（除非目标暴露 `.git/.env/config.yaml` 等 → 先 dirsearch / curl 拿到再扫）
- 找代码逻辑漏洞 → 用 semgrep（trufflehog 只看字符串模式，不懂代码语义）
- 拿到一个 secret 后想知道用在哪里 → grep + semgrep（trufflehog 没有反向追踪）
