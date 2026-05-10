---
name: semgrep
category: sast
description: 静态代码分析——找代码逻辑漏洞（SQLi/XSS sink、不安全反序列化、SSRF pattern 等）。当目标暴露源码（开源项目/.git 泄露/S3 bucket）时一秒钟比黑盒挖几小时强。
---

# semgrep 项目特定约束

## 沙箱环境

- **运行时**：python3 + pip 装的 semgrep；规则集已含 `r/auto` 默认 ruleset。
- **网络**：本地扫描无需网；首次跑 `--config auto` 会拉规则（已镜像内预拉）。
- **超时**：扫小项目 ~10-30s；大型 monorepo 可能 5min+，配 `--timeout 60` 单文件钳。
- **输出**：**必加 `--json -q`**——非 JSON 输出含表格框线，LLM 解析痛苦。
- **磁盘**：要先把目标源码拷到容器 `/work` 才能扫；不能远程扫。

## 项目策略

### 何时跑 semgrep（关键判断）

LLM 拿到流量时**通常没有源码**——semgrep 在以下场景才有价值：
1. **目标暴露 `.git/`** → curl 拉 `.git/config` + 用 git-dumper 还原源码 → semgrep 扫
2. **泄露的 S3 bucket / public repo** → wget 下载 → semgrep 扫
3. **目标是开源项目**（指纹识别后） → git clone GitHub 仓库 → semgrep 扫
4. **trufflehog 找到 secrets** 后想知道用在哪 → semgrep grep 关键字 + sink 匹配

### 常用调用

```sh
# 默认 ruleset（覆盖 OWASP + CWE Top 25）
semgrep scan --config auto /work/src --json -q -o /tmp/semgrep.json

# 指定漏洞类型
semgrep scan --config 'r/security' /work/src --json -q
semgrep scan --config 'r/<lang>.lang.security' /work/src   # python/javascript/go/...

# 高级别只看
semgrep scan --config auto /work/src --json -q --severity ERROR

# 自定义规则
semgrep scan --config /path/to/custom-rule.yml /work/src
```

## 写 finding 红线

semgrep 命中**单独不构成 finding**——SAST 是"代码层风险提示"，需结合**实际利用验证**才能写 finding：
1. 先用 semgrep 找到嫌疑代码位置（如 `subprocess.call(user_input)`）
2. 用 curl 喂恶意 input 验证实际触发
3. finding `evidence` 含：semgrep rule_id + 代码片段 + 利用 curl + 响应

如果实在没法动态验证（如目标已下线，仅源码），可写 medium severity finding 标 "潜在漏洞，需验证"。

## 决策边界（什么时候**不要**用 semgrep）

- 没有源码 → 跑不了（黑盒模式无意义）
- 找硬编码 secrets → 用 trufflehog（专精，比 semgrep 快 10x）
- 大型 monorepo 全扫 → token 雪崩，先 `find . -name '*.py'` 选关键模块再扫
- 已有 dynamic 命中 → 不需 SAST 验证，直接挖动态漏洞
