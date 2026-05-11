---
name: ffuf
category: discovery
description: 通用 web fuzzer——任意位置 FUZZ 占位符替换 wordlist。Go 实现速度王。dirsearch 是 ffuf 的"目录爆破特化版"，复杂场景用 ffuf。
---

# ffuf 项目特定约束

## 沙箱环境

- **网络**：bridge 出网，url 用 e2e 灌入的真实 host:port 即可。
- **超时**：`-timeout 10` 单请求；`-rate 200`/秒控速防 WAF 触发。
- **wordlist 路径**：业界标准在 `/opt/SecLists/`（已挂在镜像）：
  - 目录：`/opt/SecLists/Discovery/Web-Content/common.txt` (~4k 行)、`raft-medium-directories.txt` (~30k)
  - 参数：`/opt/SecLists/Discovery/Web-Content/burp-parameter-names.txt` (~2.6k)
  - 子域：`/opt/SecLists/Discovery/DNS/subdomains-top1million-5000.txt`
- **输出**：`-of json -o /tmp/out.json` 后 `cat | jq` 最适合 LLM；`-mc 200,301,302,401,403` 过滤命中码。

## 项目策略

- **目录爆破首选 dirsearch**——ffuf 用于 dirsearch 不擅长的场景：
  - 参数 fuzz：`-u 'http://host/api?FUZZ=1' -w wordlist`
  - 多 FUZZ 占位符：`-u 'http://host/FUZZ1/FUZZ2' -w w1:FUZZ1 -w w2:FUZZ2`
  - vhost fuzz：`-u 'http://host' -H 'Host: FUZZ.target.com' -w subs.txt`
- **必加 noise filter**：发现 baseline `-fc 404` 还不够，加 `-fs <baseline-size>` 排除"假 200"
- **递归** `-recursion`：默认关；目标小时可开发现深路径

## 写 finding 红线

ffuf 命中（如发现 `/.git/config`、`/admin/`）**可作 finding**，但 `evidence` 必含完整 url + status + size + 实际响应片段（用 curl 复跑确认非误报）。

## 决策边界（什么时候**不要**用 ffuf）

- 单纯目录爆破 → 用 dirsearch（自带智能 wordlist + filter，省配置）
- 隐藏参数发现 → 用 arjun（专精，比 ffuf 准）
- 已知具体 endpoint → 直接 curl/sqlmap/dalfox 测漏，不必爆破
