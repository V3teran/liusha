---
name: sqlmap
description: SQL 注入自动探测/利用，web SQLi 首选；GraphQL/NoSQL 不归它管。
category: injection
---

# sqlmap 使用手册

## 何时用

- 怀疑 URL 参数 / form / cookie / header 有 SQL 注入
- 已知注入点想自动枚举 DB / table / column
- 常见数据库：MySQL / PostgreSQL / MSSQL / Oracle / SQLite 全覆盖

## 何时不用

- **GraphQL 注入**：sqlmap 不识别 GraphQL 语法，用 python3 + 自定义 payload
- **NoSQL 注入**（MongoDB / Redis）：操作符注入语义不同，sqlmap 无能为力
- **二阶段注入**（payload 经存储后才触发）：sqlmap 单点探测不到，需 python3 编排两次请求

## 关键限速规则（避免烧时间）

**blind / time-based 必加限速**：

```bash
sqlmap -u 'http://x/y?id=1' -p id --batch \
  --time-sec=1 --stop=5 \
  -D <db> -T <table>
```

- `--time-sec=1`：单次盲注延迟 1s（默认 5s 太慢）
- `--stop=5`：单 dump 操作 5 条就停（绝不整库 dump）
- 盲注每数据点 ~千次 HTTP 请求，无限速会卡 10+ 分钟

**绝不整库 dump**：拿到 1-2 行佐证 + 写 finding，**不要 `--dump-all` 或全表 dump**。证据级别"能读 information_schema"已经足以 critical 入库，多 dump 是浪费时间 + 留痕迹。

## 失败后决策树

单次 timeout 后**优先 pivot**：

1. **curl 手动 boolean fuzz**：sqlmap timeout 可能是 payload 编码问题，curl 直接 `' OR 1=1--` / `' AND 1=2--` 看响应差异
2. **直接 write_finding 不 dump**：探测阶段已经确认注入存在 → 写 high/critical finding + 后续上下文里描述"可能进一步 dump"，**不要**为了完整证据反复重跑
3. **换 flag 重试 sqlmap 的前提**：**确有新假设**（如换 `--tamper` bypass WAF / 换 payload 模式 / 换 `--technique=T` 切到 time-based）

**反模式**：原地无新假设地微调 sqlmap flag 是死循环。LLM 实测会卡 8-10 次 retry 烧 token。

## 输出关键字 grep

| 关键字 | 含义 |
|---|---|
| `parameter '<X>' is vulnerable` | 注入点确认 |
| `back-end DBMS:` | 数据库类型识别 |
| `available databases:` | DB 枚举成功 |
| `Database: <X>` + `Table: <Y>` | 表结构 dump 中 |

无关键字 + 长时间无输出 → 多半 timeout，pivot。

## 写 finding 红线

- severity=**critical**：dump 出 user 表 / 密码 hash / API key
- severity=**high**：成功 union / boolean / time-based 注入但未 dump 敏感数据
- severity=**medium**：可注入但未 bypass WAF（仅探测确认）
