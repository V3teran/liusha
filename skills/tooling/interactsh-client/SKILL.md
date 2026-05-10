---
name: interactsh-client
category: utility
description: OOB（Out-Of-Band）回调检测——拿一个 oast.fun 子域名作 callback 钩子，让目标"反向通话"。Blind SSRF/RCE/XXE/XSS 唯一通用挖法。
---

# interactsh-client 项目特定约束

## 沙箱环境

- **网络**：必须能出网（client 要 polling `oast.fun` 公共服务）；macOS docker desktop 默认 bridge OK。
- **超时**：client 是**长连接 polling**，不能短跑——run_command 300s 上限内最多能 polling ~5min。
- **输出**：默认 stdout 实时打印每个收到的请求；`-json` JSON 一行一记录适合 LLM 解析。

## 项目策略

### 工作流（关键三步）

```sh
# 1. 启动 client 拿 callback 域名（输出到 stdout）
interactsh-client -json -o /tmp/oob.json &
sleep 2
# stdout 含: [INF] Listing 1 payload for OAST Server
#           [INF] cabcde123.oast.fun

# 2. 把 callback 域名塞 payload 发给目标
DOMAIN=cabcde123.oast.fun
curl "http://target/api?url=http://${DOMAIN}/ssrf-test"
# 或 sqlmap / dalfox / nuclei -interactsh-url 也支持

# 3. 看 client 是否收到回调（grep stdout 或 cat /tmp/oob.json）
cat /tmp/oob.json | jq '.'   # 收到的 DNS / HTTP 请求 + 时间戳
```

### 在 react step 内的实践

由于 client 长连接特性 + run_command 300s 限制，**推荐每条流量启一次**：
1. 启 client（异步 `&`）+ 立即拿域名
2. 发 1-3 个 payload（curl/sqlmap）
3. `wait 30 + cat /tmp/oob.json` 看回调
4. 单 run_command 内完成探测周期

**禁忌**：不要把 client 当后台守护进程跨 react step 运行（容器 --rm 会清掉）。

## 写 finding 红线

OOB 命中（client 收到来自目标的 DNS / HTTP 请求）**直接构成 high/critical finding**——这是盲态漏洞唯一确定证据。`evidence` 必含：
- 注入位置（哪个参数 / header / cookie）
- 完整 payload（含使用的 callback 域名）
- 收到的回调记录（unique-id / protocol DNS|HTTP / timestamp / source-ip）
- 推断的漏洞类型（SSRF / Blind XSS / Blind RCE / XXE / 反序列化）

## 决策边界（什么时候**不要**用 interactsh-client）

- 漏洞已有直接回显（如响应体里看到 payload reflect）→ 直接 dalfox/sqlmap，不需 OOB
- 完全离线/内网无出网 → callback 永远收不到
- 单 step 30s 来不及看回调 → 拆到下个 step `cat /tmp/oob.json` 兜底（提前用 write_memory 存域名）
