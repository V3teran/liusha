---
name: sqli
category: web
description: SQL 注入——服务端必须解析了恶意 SQL 才算确认，仅看 payload reflect 不够。优先用 sqlmap，手工补 sqlmap 不擅长的场景。
---

# SQL 注入（SQLi）

## 漏洞本质

应用把用户输入**直接拼接进 SQL 语句**（未参数化查询/未 escape），导致：

- **Error-based**：触发 DB 错误回显 → 直接暴露 schema
- **UNION-based**：注入 UNION SELECT 拼出数据回显
- **Boolean-based blind**：响应内容差异（true vs false condition）
- **Time-based blind**：服务端响应时长可控（`SLEEP(N)`）
- **二阶注入**：注入 payload 存储在数据库，下次查询触发

## 挖掘方向（思路，不是步骤）

**唯一铁律**：验证 SQLi 成功 = **服务端解析了恶意 SQL 后产生可观测差异**——DB 错误回显 / UNION 数据回显 / 布尔差异稳定 / 时延差 ≥2s 多次稳定。**仅看 payload reflect 在响应里不够**（那是 reflected，可能是 XSS 嫌疑或单纯回显参数，不是 SQL 注入）。

**优先 sqlmap，自己手工只在 sqlmap 不擅长时**。

下面是倾向性建议，不是规则：

- 流量参数是 `?id=N` / `?uid=N` / `?cat=X` 等典型 SQL 参数名 → 高概率 SQLi
- 流量原值是数字（如 `id=1`）→ 通常 integer-based，无需引号
- 流量原值是字符串 → 试 single-quote / double-quote / backslash 看错误回显

## sqlmap 协同（沙箱已装，优先用）

```sh
sqlmap -u "http://target/path?param=value" --batch --random-agent --level 3 --risk 2
```

看输出有 `Parameter:` / `Type:` / `Payload:` 三件套即为成功。

**sqlmap 不擅长的场景**（要手工补）：

- GraphQL endpoint（参数在 JSON body 嵌套）
- 二阶注入（注入位置 ≠ 触发位置）
- NoSQL（MongoDB `{$ne: null}` 那种语法，sqlmap 不识别）
- 自定义 body 格式（base64 包裹 / 加密参数）
- 复杂 cookie/header 注入（用 `--cookie` 但 sqlmap 默认排除某些 header）

完整工具用法参考 `read_tooling_skill(name="sqlmap")`。

## 判定原则（按优先级）

### 1. DB 错误回显 → Error-based 确认（high）

错误特征字符串（按 DBMS）：

- MySQL：`You have an error in your SQL syntax` / `MySQL server version`
- PostgreSQL：`PG::SyntaxError` / `unterminated quoted string`
- Oracle：`ORA-00933` / `ORA-01756`
- MSSQL：`Unclosed quotation mark` / `Microsoft OLE DB`
- SQLite：`SQLite/JDBCDriver` / `SQLite3::SQLException`

### 2. UNION-based 数据回显 → critical

payload 如 `' UNION SELECT NULL,concat(database(),version()),NULL--`，响应中出现 DB 名+版本字符串 → 数据库结构可被任意 dump。

### 3. Boolean-based blind：响应内容稳定差异 → Blind SQLi

`' AND 1=1--` vs `' AND 1=2--` 响应 body 不同（长度差 / 关键字段差），且**多次重放稳定**。

### 4. Time-based blind：`SLEEP(N)` 多次稳定生效 → Time-based Blind

`' AND SLEEP(5)--` / `'; WAITFOR DELAY '0:0:5'--`（MSSQL）。

**关键**：必须**多次采样**（≥3 次），每次时延都 ≥ 设定值；网络抖动单次 5s 不可信。子查询 gating 保险（`AND IF(condition, SLEEP(5), 0)`）避免误报。

### 5. 响应 500 但 stack trace 不含 SQL 错误关键字 → 可能是其他错误，不是 SQLi

应用层异常 / 业务校验失败 / 框架报错都可能 500，但不一定是 SQL 错误。看响应 body 关键字判断。

## 手工 payload 策略（sqlmap 不覆盖时）

| 类型 | payload 示例 |
|---|---|
| 引号探测 | `'` / `"` / `\` / `1)` / `1'` |
| Error-based | `' OR 1=1--` / `'; SELECT 1/0--` |
| UNION | `' UNION SELECT NULL,NULL,NULL--`（先试列数） |
| Boolean | `' AND 1=1--` / `' AND 1=2--`（对比响应） |
| Time-based | `' AND SLEEP(5)--` / `'; WAITFOR DELAY '0:0:5'--` |
| 注释符 | `--` / `#` / `/*` |
| Stack queries | `'; DROP TABLE x--`（MSSQL/PostgreSQL；MySQL 默认不允许） |

## 误报排除

- payload reflect 在响应里但状态码不变 → 是参数回显，不是 SQLi（可能是 XSS 嫌疑）
- 时延差只测了 1 次 → 网络抖动嫌疑，必须 ≥3 次稳定
- 数据库错误回显但出现在 stack trace 顶部 → 看错误来自哪个参数（可能其他参数引起，不是当前测的）
- 时延差 <2s → 网络噪声范围，不可靠

## write_finding 红线

evidence 必须包含：

- **sqlmap 路径**：完整三件套 `Parameter: <name>` / `Type: <type>` / `Payload: <payload>`（缺一不可）
- **手工路径**：完整 payload + 响应特征（错误片段 / 回显数据 / boolean 差异 body / 时延采样数据）
- **时基 SQLi**：≥3 次时延采样数据（每次实测毫秒数）
- **repro_cmd**：sqlmap 命令或最小 curl，别人 copy 能复现

summary ≤500 字单行：`SQLi in GET /vulnerabilities/sqli/?id= — error-based MySQL via single-quote (id parameter)`
