# BAC（访问控制失效）认知地图

## 1. 检测点

哪类请求 / 流量特征触发本 skill：

- URL 路径含 `/api/bac/`（vulnapp 测试前缀）、`/admin/`、`/sys/`
- URL 含数字/UUID 参数：`?uid=123` / `?orderid=abc-...` / `/:user_id/` / `/:order_id/`
- POST/DELETE 高危管理动作（如 `/api/bac/admin/delete` body 含 uid）

## 2. 类型矩阵

漏洞子类型枚举（finding.kind）：

- `bac.unauthorized_access`：未授权（最高优先级）— anonymous 能正常访问需登录的资源
- `bac.vertical_priv_esc`：垂直越权 — 低权限角色（如 test）能访问高权限资源（admin 路径）
- `bac.horizontal_priv_esc`：水平越权 — 同级用户能访问其他用户的私有数据

## 3. 能力矩阵

已注册的 Action：

| Action | 输入 | 输出 |
|---|---|---|
| `fetch_credentials` | `{host}` | 全部身份（含 anonymous） |
| `replay_multi_identity` | `{flow_id, host, concurrency=5}` | N 身份并发响应（body_hint 截 400 字） |
| `heuristic_check` | `{rules: [all_denied, all_empty, all_auth_error]}` | `{skip, hit_rule}` |
| `compute_similarity` | `{algorithm=structural, threshold=0.3}` | 矩阵 + all_below_threshold + max_pair |

## 4. 有效 Payload

替换什么字段：

- **headers**：Cookie（session ID）、Authorization（Bearer/Basic）
- **query**：uid、user_id、orderid、id、page（业务上下文敏感字段）
- **body**：JSON/form-encoded 中的 oid、target_user、user_id 等

## 5. 判定规则

similarity 阈值与 heuristic 命中：

- `compute_similarity.threshold=0.3`：< 0.3 视为响应"显著不同"（不算越权）
- heuristic 命中（任一为 true）→ 立即 done(heuristic_skip)：
  - all_denied：全部 4xx/5xx
  - all_empty：全部 body 为空 / `{}` / `[]`
  - all_auth_error：全部含鉴权关键词
- compute_similarity 全 pair < 0.3 → done(all_similar)
- 命中漏洞类型（按优先级）：
  1. anonymous 与登录态的 status_code 都 < 400 且相似度 ≥ threshold → unauthorized_access
  2. 低权限用户访问 /admin/、/sys/ 路径 status_code < 400 → vertical_priv_esc
  3. 多个同级用户对同一私有资源 status_code < 400 且相似度 ≥ threshold → horizontal_priv_esc

## 6. 失败方向

已知不该再走的方向（对应 done.reason 取值）：

- `heuristic_skip`：heuristic 三选一命中，跳过本 endpoint
- `all_similar`：响应全相似，无差异化越权信号
- `no_pattern_match`：跑完 step 1-5 都无任何漏洞类型命中
- `finding_written`：成功命中并已 write_finding（注意 dedup_key 要在 finding 表中存在）
