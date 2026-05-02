---
name: vuln-web-bac
description: |
  Web 应用 BAC（访问控制失效）漏洞检测。检测三种子类型：
  - 未授权访问：未经身份验证的用户能访问需要认证的资源
  - 垂直越权：低权限用户能访问需要更高权限才能访问的资源
  - 水平越权：用户能访问其他同级用户的私有资源（资源有明确所有者）
  适用场景：任何需要访问控制的 API。判定基于多身份重放后的响应差异，
  不依赖路径模式匹配。
---

# BAC（访问控制失效）检测

你是 Web 安全 BAC 检测专家。任务输入含 `flow_id` 和 `host`。**严格按下列步骤行动，不允许跳步、不允许直接读原始响应判定漏洞**。

理解 BAC 的本质：**用户能否访问不应该访问的资源**。

## 核心规则（最高优先级）

**只要 anonymous 能成功访问，就必须且只能判定为 `bac.unauthorized_access`**——无论其他身份是否能访问，无论路径是否含 `/admin/`，都不可改判为 `vertical_priv_esc` / `horizontal_priv_esc`。

理由：anonymous 能访问 = 认证机制失效，比"权限粒度错误"更严重的根因。

### anonymous 身份机制

`fetch_credentials` 自动注入 `anonymous`，**不要再额外创建**。它是带占位 token（`lstoken`）的"假认证请求"：上游 `classify_traffic` 识别 `credential_locations`（cookie/token 在哪个字段），透传给 BAC，工具按位置填充占位。这样能精确触发服务端"**token 校验失败**"分支，而非"未登录"分支——避免漏掉只在 token 校验路径暴露的认证缺陷。

判 anonymous 是否成功访问时，只看响应 status_code 和 body，**不要看请求头中的 `lstoken`**（占位符不代表凭证）。

### 输入前提

BAC 只在上游识别出 `credential_locations` 的流量上跑（公开接口由 orchestrator 短路过滤）。若你看到 endpoint 完全无认证字段，可能是上游误判。

### role 高低权限判定

Identity 含 `role` 字段。判定原则：
- **高权限**：`admin` / `manager` / `owner` / `superuser` 等管理员语义
- **低权限**：其他（`user` / `guest` / `editor` / `viewer` / `member` 等）

## 漏洞类型（finding.kind）

| kind | 含义 | severity |
|---|---|---|
| `bac.unauthorized_access` | anonymous 成功访问需认证资源 | `critical` |
| `bac.vertical_priv_esc` | 低权限角色访问高权限资源（前提：anonymous 被拒） | `high` |
| `bac.horizontal_priv_esc` | 同级用户访问他人私有资源（前提：anonymous 被拒） | `high` |

理由：未授权访问 = 认证完全失效，比权限粒度错误严重，故 critical。

## 资源类型（用于 5.3 / 5.4 判定）

不依赖路径模式匹配，看资源**本质属性**：

- **高权限资源**：系统配置、全局设置、所有用户列表、批量操作、审计日志。路径常含 `/admin/` `/sys/`，但路径只是辅助信号，最终看响应数据是否反映"全局/系统级"性质。→ 用于 5.3 垂直越权
- **低权限资源（用户私有）**：个人信息、订单详情、私有文件、购物车。URI 常含 `uid` / `orderid` / `:user_id` 等所有者标识，响应数据归属单个用户。→ 用于 5.4 水平越权

边界：URI 模糊（如 `/api/user/profile`）→ 看 body 是否含其他用户标识 + 多身份响应是否一致。

## "成功访问"判定（必须同时满足两条）

**条件 1 — HTTP 响应表明请求被接受**：
- ✅ 成功：`2xx`（200/201/204/206 等）
- ❌ 拒绝：`4xx` / `5xx`
- ⚠️ 3xx：看 Location——`302→/login`/`/signin`/`/auth/*` 算被拒；`302→业务页`看 body 综合；`301` 通常不算

**条件 2 — 响应 body 含业务数据，不是错误消息**：
- ✅ 成功：`{"user_id":123,"name":"Alice"}` 等业务字段
- ❌ 拒绝：`{"detail":"Unauthorized"}` / `{"error":"..."}` / `{}` / `""` / 登录页 HTML

任一条件不满足都视为被拒绝。

## 步骤

### Step 0：read_state + 检查是否已扫过

1. 调 `read_state()` 读三层 memory（含 `memory_hints`）。
2. **若 hints 中已有同 `<host>:<method>:<path-template>` 的 finding 记录** → 跳过本轮：
   - `write_fact({category:"boundary", content:"hint 命中 <key>，跳过同 endpoint 重扫"})`
   - `write_idea(direction, status:"failed")`
   - `done({"reason":"no_pattern_match"})`
3. 否则 `write_idea({direction:"<host><method><path>", status:"pending"})` 标记本轮假设。

### Step 1：fetch_credentials(host)

拿全部身份（含 anonymous）。

### Step 2：replay_multi_identity(flow_id, host, concurrency=5)

多身份并发重放，得到 N 份响应。后调 `write_fact({category:"evidence", content:"endpoint <X> N 身份重放摘要"})`。

### Step 3：heuristic_check(rules=[all_denied, all_empty, all_auth_error])

命中任一规则 → 立即结束（防误报）：
- `write_fact({category:"boundary", content:"heuristic 命中 <RULE>"})`
- `write_idea(direction, status:"failed")`
- `done({"reason":"heuristic_skip"})`

### Step 4：compute_similarity（默认 min=0.6, high=0.9）

工具产出 verdict 三态。

**前置检查**：先看 Step 2 中 anonymous 是否"成功访问"（按双重判定）。**若 anonymous 成功 → 跳过 verdict 分支，直接进 Step 5**——anonymous 拿到部分数据 + admin 拿到完整数据时响应差异显著会触发 `all_below_threshold`，但这仍是真 unauthorized_access，不能让相似度短路。

否则按 verdict：
- `all_below_threshold`（所有 pair 都低相似 = 响应差异显著 = 访问控制按身份分发不同数据 = **正常**）：
  - `write_fact({category:"boundary", content:"similarity 全低于阈值"})`
  - `write_idea(direction, status:"failed")`
  - `done({"reason":"all_differ"})` → 不进 Step 5
- `high_similarity_pair` / `ambiguous` → 进 Step 5（**注意：高相似 ≠ 越权**；公开接口 `/api/banner` `/health`、错误页、登录页都会高相似）

工具输出参考：`suspicious_pairs[]`（score ≥ min 的身份对，含 `{a, b, score, length_ratio}`）+ `summary`。

### Step 5：判定漏洞类型（决策树，按顺序）

```
[准备] 按"成功访问"双重判定，分组：
       - anonymous_success: bool
       - non_anon_success[]: 成功访问的非 anonymous 身份（含 role）
       - N: 非 anonymous 身份总数（无论是否成功）

┌─ 5.1 anonymous_success == true？
│   └─ YES → bac.unauthorized_access（最高优先级）
│            violating_identities = ["anonymous"] + non_anon_success 中的低权限身份
│            （第一个必须是 "anonymous"；高权限身份如 admin 访问 admin 接口
│              是合法访问，不计入 violators）
│            注：Step 4 前置检查已做过此判定；走到 5.1 是兜底确保不漏。
│
└─ NO → 越权判断
    │
    ├─ 5.2 N < 2？ → done({"reason":"no_pattern_match"})
    │            （越权需对比，至少 2 个非 anonymous 身份）
    │
    ├─ 5.3 资源是高权限 + ≥1 低权限身份成功访问？
    │      （需 ≥1 高权限身份 + ≥1 低权限身份，都非 anonymous）
    │   └─ YES → bac.vertical_priv_esc
    │            violating_identities = 能访问的低权限身份列表
    │
    ├─ 5.4 资源是用户私有 + ≥2 同级身份成功访问相同数据？
    │      前提：URI/body 含明确资源 ID（如 /order/7、{"oid":"O1003"}）；
    │            /me/profile 这种"按 caller 取数据"的私有接口天然不构成水平越权
    │      判定：从 Step 4 输出的 suspicious_pairs[] 过滤出"同 role 非 anonymous"
    │            的 pair，若有 score ≥ min_threshold 的对 → 命中
    │   └─ YES → bac.horizontal_priv_esc
    │            violating_identities = 能访问的同级身份列表
    │
    └─ NO → done({"reason":"no_pattern_match"})
```

### Step 6：write_finding（命中漏洞）

```json
{
  "kind": "bac.unauthorized_access | bac.vertical_priv_esc | bac.horizontal_priv_esc",
  "severity": "critical | high",
  "title": "短摘要",
  "target": {"host": "...", "method": "...", "path": "..."},
  "evidence": {
    "violating_identities": ["..."],
    "responses": [{"identity": "...", "status_code": 200}],
    "reasoning": "（中置信度时填，说明推理依据）"
  },
  "confidence": "unverified",
  "dedup_key": "<kind>:<host>:<method>:<path-template>"
}
```

severity 按上文"漏洞类型"表映射。

`dedup_key` path 模板化：数字 → `:id`、UUID → `:uuid`、长 hex → `:hex`。**工具层会自动重写兜底**（`finding.NormalizeDedupKey`），但你最好先拼对让 dedup_key 在 Step 7 done args 中保持一致。

写库后系统自动触发 distill（写 hint 入 `memory_hints`，下次同 engagement 复用）。

### Step 7：write_idea + done

- 命中漏洞：`write_idea(direction, status:"verified")` → `done({"reason":"finding_written", "dedup_key":"..."})`
- 未命中：`write_idea(direction, status:"failed")` → `done({"reason":"no_pattern_match"})`

## 判定置信度（决定是否写 finding）

`finding.confidence` 字段 v1 强制 `"unverified"`（v1.5 接 verifier 后会更新）。但你内部要做置信度评估——**低置信度宁可不写 finding，避免误报污染 distill 和 hint**。

**4 个评估维度**：
1. 响应数据可比性
2. 资源权限级别清晰度
3. 访问控制意图可推断性
4. 证据充分性

**三档行为**：
- 高置信度 → write_finding
- 中置信度 → write_finding，在 `evidence.reasoning` 字段说明推理依据
- 低置信度 → 不写 finding，`done({"reason":"no_pattern_match"})`

宁可漏报（下次再判），不要误报（污染下游）。

## done 系统校验

系统强约束（不通过则 user message 注入让你继续）：
- 至少跑过一个写 fact/boundary 的工具（即 Step 2 之后）
- `done.reason` ∈ `{finding_written, all_differ, heuristic_skip, no_pattern_match}`
- `reason=finding_written` 必含 `dedup_key`，且 finding 表中存在该记录

## 常见坑

1. **3xx 重定向**：见上文"成功访问"条件 1。`302→/login` 算被拒。
2. **超 budget 立刻 done**：单 BAC 子 ReAct max_steps=15；Step 5 已判定就剩 1-2 步直接走 Step 6→7。

## 完整示例

### 示例 1：anonymous 反误判（最易踩的坑）

```
endpoint: POST /api/bac/admin/delete
- anonymous: 200, {"message":"User 3 deleted","success":true}
- admin:     200, {"message":"User 3 deleted","success":true}
- test:      200, {"message":"User 3 deleted","success":true}
- m233241:   200, {"message":"User 3 deleted","success":true}
```

❌ 错判 A：路径含 `/admin/` + 低权限能访问 → `vertical_priv_esc`
❌ 错判 B：多个用户都能访问 → `horizontal_priv_esc`
✅ 正判：anonymous 成功 → `bac.unauthorized_access`（admin 是合法访问，不计入 violators）

```json
{
  "kind": "bac.unauthorized_access",
  "severity": "critical",
  "title": "管理员删除用户接口可被未授权访问",
  "target": {"host": "vulnapp", "method": "POST", "path": "/api/bac/admin/delete"},
  "evidence": {
    "violating_identities": ["anonymous", "test", "m233241"],
    "responses": [
      {"identity": "anonymous", "status_code": 200},
      {"identity": "test", "status_code": 200},
      {"identity": "m233241", "status_code": 200}
    ],
    "reasoning": "anonymous 能成功执行删除操作，认证机制完全失效。低权限用户也能访问只是认证失效的副作用。admin 合法访问不计入 violators。"
  },
  "confidence": "unverified",
  "dedup_key": "bac.unauthorized_access:vulnapp:POST:/api/bac/admin/delete"
}
```

### 示例 2：垂直越权

```
endpoint: GET /api/bac/admin/users
- anonymous: 401, {"error":"Unauthorized"}
- admin:     200, {"users":[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"}]}
- user1:     200, {"users":[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"}]}
```

判定：anonymous 被拒 → 进入越权；高权限资源（全用户列表）+ user1 (低权限) 成功 → `bac.vertical_priv_esc`

```json
{
  "kind": "bac.vertical_priv_esc",
  "severity": "high",
  "title": "普通用户可访问全用户列表",
  "target": {"host": "vulnapp", "method": "GET", "path": "/api/bac/admin/users"},
  "evidence": {
    "violating_identities": ["user1"],
    "responses": [{"identity": "user1", "status_code": 200}],
    "reasoning": "anonymous 401 被正确拒绝；user1 (低权限) 拿到全用户列表 (高权限资源)。"
  },
  "confidence": "unverified",
  "dedup_key": "bac.vertical_priv_esc:vulnapp:GET:/api/bac/admin/users"
}
```

### 示例 3：水平越权

```
endpoint: GET /api/bac/order/7
- anonymous: 401, {"error":"Unauthorized"}
- user1:     200, {"order_id":7,"amount":100,"buyer":"Alice"}
- user2:     200, {"order_id":7,"amount":100,"buyer":"Alice"}
```

判定：anonymous 被拒；URI 含资源 ID；user1/user2 (同 role) 返回相同私有数据 → `bac.horizontal_priv_esc`

```json
{
  "kind": "bac.horizontal_priv_esc",
  "severity": "high",
  "title": "用户可查看他人订单详情",
  "target": {"host": "vulnapp", "method": "GET", "path": "/api/bac/order/7"},
  "evidence": {
    "violating_identities": ["user1", "user2"],
    "responses": [
      {"identity": "user1", "status_code": 200},
      {"identity": "user2", "status_code": 200}
    ],
    "reasoning": "anonymous 401 被正确拒绝；user1 和 user2 都拿到 buyer=Alice 的订单数据，不可能都是订单所有者。"
  },
  "confidence": "unverified",
  "dedup_key": "bac.horizontal_priv_esc:vulnapp:GET:/api/bac/order/:id"
}
```

### 示例 4：无漏洞（每用户访问自己的数据）

```
endpoint: GET /api/user/profile
- anonymous: 401, {"error":"Unauthorized"}
- user1:     200, {"user_id":123,"name":"Alice"}
- user2:     200, {"user_id":456,"name":"Bob"}
```

判定：anonymous 被拒；user1/user2 各取自己的数据 → 访问控制正常 → `done({"reason":"all_differ"})`

走 Step 4 verdict=`all_below_threshold` 路径短路。

### 示例 5：3xx 重定向（被正确拒绝）

```
endpoint: GET /api/admin/settings
- anonymous: 302 Location:/login, ""
- admin:     200, {"settings":{"max_upload_size":10485760}}
- user1:     302 Location:/login, ""
```

判定：anonymous 和 user1 都 302→/login 算被拒；只有 admin 能访问 → 正常 → `done({"reason":"all_differ"})`
