---
name: nmap
category: recon
description: 端口扫描 + 服务指纹 + NSE 脚本（半个漏扫器）。LLM 已熟悉用法——本手册只列沙箱约束 + NSE 高价值脚本清单。发现"主端口外的高危服务"标配（Tomcat 8080/Jenkins/ES 9200/Redis 6379...）。
---

# nmap 项目特定约束

## 沙箱环境

- **网络**：访问宿主用 `host.docker.internal`；扫公网 IP 直接走 bridge。
- **超时**：默认 SYN scan 全 65k 端口耗时长——必加 `-T4` 或限范围 `-p 1-10000`。
- **NSE 脚本**：`/usr/share/nmap/scripts/` 全套已装；指定 `--script <name>` 调用。
- **输出**：`-oG -` grepable 比 XML 适合 LLM 解析；普通文本也行（每行 `PORT/STATE SERVICE`）。

## 项目策略

- **快速 baseline**：`nmap -T4 -p- --min-rate 1000 host.docker.internal`（全端口 ~30s）
- **服务指纹**：发现开端口后跑 `-sV -p <found>` 拿版本（用于 CVE 关联）
- **OS 探测** `-O`：要 root 权限，沙箱默认非 root 跑不了 → 跳过
- **跳过 ping** `-Pn`：宿主可能 ICMP 关，必加避免误判 down

## NSE 脚本高价值清单（LLM 必知）

发现指定端口后立即跑对应 NSE 验证：

| 端口 | NSE 脚本 | 验证什么 |
|---|---|---|
| 22 | `ssh-auth-methods,ssh2-enum-algos` | 弱算法/认证方式 |
| 80/443 | `http-vuln-cve*,http-shellshock,http-enum` | Web CVE 速查 |
| 443 | `ssl-heartbleed,ssl-poodle,ssl-ccs-injection,ssl-enum-ciphers` | TLS 漏洞 |
| 445 | `smb-vuln-*,smb-enum-shares,smb-os-discovery` | SMB 全套 |
| 3306 | `mysql-empty-password,mysql-info,mysql-enum` | MySQL 默认凭证 |
| 5432 | `pgsql-brute` | PostgreSQL 暴破 |
| 6379 | `redis-info` | Redis 未授权 |
| 8080 | `http-tomcat-mgr-credential-bf` | Tomcat 默认凭证 |
| 9200 | `http-elasticsearch-head` | ES 未授权 |
| 27017 | `mongodb-info` | MongoDB 未授权 |

**组合**：`nmap -sV --script "default,vuln" -p <port> host` 一次性跑默认+漏洞脚本。

## 写 finding 红线

NSE 命中（如 `ssl-heartbleed: VULNERABLE`）**直接构成 finding**——`evidence` 必含 NSE 输出原文（"VULNERABLE"行 + CVE id + Description）。仅"端口开"不算 finding，要配合服务指纹+CVE。

## 决策边界（什么时候**不要**用 nmap）

- 已知目标只跑 80/443 → 直接 curl/nuclei，不用 nmap
- 想要"漏扫" → 用 nuclei（模板更新更快），nmap NSE 是补充
- ICMP/UDP 严格场景 → 沙箱网络层可能受限，nmap UDP 扫描效果差
