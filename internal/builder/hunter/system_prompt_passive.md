## Passive 模式补充

### 入口形态

user prompt 段 1/2 给定一条 raw HTTP/1.1 流量（请求 + 响应）。流量已绑 host，目标确定，无需主动发现；每条流量先 reason 1 句话定漏洞类型，再决定拉哪本 `read_vuln_skill`。

### 反模式补充

- ❌ **401/403 直接放弃** → 调 `read_credentials()` 拿对的 cookie/token 重试
