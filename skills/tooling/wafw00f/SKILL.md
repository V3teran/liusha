---
name: wafw00f
category: recon
description: WAF 指纹识别（CloudFlare/Akamai/AWS WAF/F5 BIG-IP/...）。挖洞前先跑——知道 WAF 类型才能选 sqlmap --tamper / dalfox bypass。
---

# wafw00f 项目特定约束

## 沙箱环境

- **网络**：bridge 出网，url 用 e2e 灌入的真实 host:port 即可。
- **超时**：单次 ~5-15s（发数十个探测请求看 WAF 行为）。
- **输出**：`-o output.json` 写文件（容器内随手）；或直接看 stdout 的 "is behind <WAF>" 字样。

## 项目策略

- **基本调用**：`wafw00f http://<target>:4280` —— 一行搞定。
- **`-a`**：识别**所有**WAF（默认只报第一个匹配），命中多 WAF 链路时有用。
- **`-i targets.txt`**：批量。
- **`-v` verbose** 可看每个 WAF 的探测细节，但产生大量 stdout，慎用（8KB tail 可能截断）。

## 写 finding 红线

WAF 识别**不构成 finding**——是辅助情报。但 finding 的 `evidence` 可写"目标受 CloudFlare WAF 保护，本 payload 已用 X tamper 绕过"作为利用复杂度证明。

## 决策边界（什么时候**不要**用 wafw00f）

- 流量响应 header 已含 `Server: cloudflare` / `X-Powered-By: AWS-WAF` → LLM 看响应直接判断，不用跑 wafw00f
- 内网/本地靶场 → 通常无 WAF，跑了浪费 5s
- 命中 403/418/429 等 WAF 标志码后 → wafw00f 也能确认，但优先用 nuclei `waf-detect.yaml` 模板（更快）
