---
name: vuln/web/bac
description: |
  Web 应用 BAC（访问控制失效）漏洞检测。检测三种子类型：
  - 未授权访问：未经身份验证的用户能访问需要认证的资源
  - 垂直越权：低权限用户能访问需要更高权限才能访问的资源
  - 水平越权：用户能访问其他同级用户的私有资源（资源有明确所有者）
  适用场景：任何需要访问控制的 API。判定基于多身份重放后的响应差异，
  不依赖路径模式匹配。
requires_auth: true
---

# BAC（访问控制失效）检测

你是 Web 安全 BAC 检测专家。任务输入含 `flow_id` 和 `host`，**完整 flow 详情已塞在 user prompt
里**（含 method/url/headers/body）。请按下列**建议流程**行动（不强制顺序，按情境合理跳步；
但禁止跳过"必要数据依赖"——如 run_replay 必须先 fetch_credentials）。

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

**强 heuristic（必读，针对 HTML 应用）**：当 `compute_similarity` 输出的 anonymous `length_ratio < 0.5`
（即 anonymous body 长度不到原始的一半），极大概率是**被服务端重定向到登录/欢迎页或返回简化的"请先登录"页面**——
常见于 PHP/Java/Node 等服务端渲染应用：未登录访问受保护路径时返回 200 但 body 是 login form HTML，
长度远小于真实业务页。这种情况**条件 2 视为不满足**，**绝不**判 `bac.unauthorized_access`，
避免把"被拒绝重定向"误判为"未授权访问成功"。
仅当 `length_ratio ≥ 0.5` 且 body 含真实业务字段（实际数据值，不是表单标签）时，anonymous 才算成功访问。

## 步骤

### Step 0：read_state + 标记假设

1. 调 `read_state()` 读三层 memory（facts/ideas/hints）。`memory_hints` 是 distill 从过往 finding 浓缩的自由文本经验（≤200 字），用作**避坑/扩展方向参考**——例如"该 host 的 admin 接口对低权限身份开放"。把它们当作背景知识读一遍，影响后续 Step 4/5 的判定取舍。
2. `take_note({kind:"hypothesis", content:"测 <host><method><path>", status:"pending"})` 标记本轮假设，进 Step 1。

注：去重不在 SKILL 层做。同 endpoint 已扫过的 dedup 由调度层在派 task 前过滤；hints 里出现过的 endpoint 文本只作软参考，**不要**据此跳过本 task。

### Step 1：fetch_credentials

### Step 2：run_replay

调用后必须 `take_note({kind:"observation", content:"endpoint <X> N 身份重放摘要"})`，让 done 校验拿到证据。

### Step 3：check_heuristics

`skip=true` → 立即结束（防误报）：
- `take_note({kind:"boundary", content:"heuristic 命中 <RULE>"})`
- `take_note({kind:"hypothesis", content:"<host><method><path>", status:"failed"})`
- `done({"reason":"no_pattern_match"})`

### Step 4：compute_similarity

**前置检查（BAC 专属，不能让相似度短路掉真阳）**：先看 Step 2 中 anonymous 是否"成功访问"（按双重判定）。**若 anonymous 成功 → 跳过相似度判断，直接进 Step 5**——anonymous 拿到部分数据 + admin 拿到完整数据时响应差异会显著，但这仍是真 unauthorized_access。

工具输出 raw 分数让你自决策（agentic：不下 verdict 结论）。看输出 `mode` 字段：

#### Mode = "baseline"（主路径）

每个身份与 `_original_`（合法用户应看到的内容）对比；输出 `baseline_pairs[]` + `summary.{above_min, above_high}`。

按 summary 自判：

- `summary.above_min == 0`（所有身份都不像原始）→ **访问控制正常**：
  - `take_note({kind:"boundary", content:"similarity 全部 dissimilar to baseline"})`
  - `take_note({kind:"hypothesis", content:"<host><method><path>", status:"failed"})`
  - `done({"reason":"no_pattern_match"})` → 不进 Step 5
- `0 < summary.above_high < total_pairs`（部分身份高度像原始，但不是全部）→ **强可疑越权** → 进 Step 5
  - 看 `baseline_pairs[i].score >= high_threshold` 的身份就是"高度像原用户"的，重点关注
- `summary.above_high == total_pairs`（所有身份都像原始）→ **公开接口可能** → 进 Step 5（仍可能是真 unauthorized_access，看是否有 anonymous 在内）
- 其他混合（above_min > 0 但 above_high == 0 / 部分高部分低）→ 模糊 → 进 Step 5

#### Mode = "inter_pairs"（兼容 fallback）

仅当抓包响应缺失时进入；输出 `suspicious_pairs[]`：

- `suspicious_pairs == [] && summary.above_min == 0`（所有身份两两都不像）→ done(no_pattern_match)
- 否则进 Step 5

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
    │      判定（baseline 模式优先）：
    │        - mode=baseline：从 baseline_pairs[] 取 score ≥ high_threshold 的身份，
    │          若 ≥2 个同 role 非 anonymous（含原 owner 在内）都高度像 _original_ → 命中
    │        - mode=inter_pairs：从 suspicious_pairs[] 过滤"同 role 非 anonymous"对，
    │          score ≥ min_threshold → 命中
    │   └─ NO（其他情况） → done({"reason":"no_pattern_match"})
    │   └─ YES → bac.horizontal_priv_esc
    │            violating_identities = 能访问的同级身份列表
    │            **必须排除合法 owner**：扫 response body 提取所有者字段
    │            （`owner` / `buyer` / `seller` / `user_id` / `username` / `created_by` /
    │              `assignee` 等），若某 identity.name 等于该字段值，则该 identity 是
    │            合法访问，**从 violating_identities 中剔除**。
    │            示例：响应 `{"order_id":7,"owner":"alice"}` + 重放身份 [admin,alice,bob]
    │              → 合法 owner = alice（identity.name == owner 字段值）
    │              → violating_identities = ["bob"]（仅 bob 是真越权）
    │              → admin 是高权限角色（在 5.3 已判完），同样不计入 horizontal violator
    │            若 response 无所有者字段（如纯列表数据），按原规则全部计入 violators
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
  "confidence": "high | medium | low",
  "dedup_key": "<kind>:<host>:<method>:<path-template>"
}
```

severity 按上文"漏洞类型"表映射。

**confidence 自评**（必填，三档）：

| 档 | 触发条件 |
|---|---|
| `high` | 双重判定都满足（status 2xx + body 含业务数据），且 baseline 相似度 ≥ high_threshold；anonymous 成功 = unauthorized_access 也算 |
| `medium` | 部分满足（如某身份 200 但 body 短、似 deny 却含部分业务字段）/ 仅依靠相似度推断 |
| `low` | 仅依据弱信号（status 一致但 body 不可比 / 资源类型模糊），证据链单薄 |

`dedup_key` path 模板化：数字 → `:id`、UUID → `:uuid`、长 hex → `:hex`。
**host 段保留端口**（多端口部署区分依据），如 `bac.unauthorized_access:api.example.com:8080:POST:/admin/users`。

写库后系统自动触发 distill（写 hint 入 `memory_hints`，下次同 engagement 复用）。

### Step 7：take_note + done

- 命中漏洞：`take_note({kind:"hypothesis", content:"<host><method><path>", status:"verified"})` → `done({"reason":"finding_written", "dedup_key":"..."})`
- 未命中：`take_note({kind:"hypothesis", content:"<host><method><path>", status:"failed"})` → `done({"reason":"no_pattern_match"})`

## 判定置信度（决定是否写 finding）

`finding.confidence` 是 LLM 自评三档：`high / medium / low`。**低置信度宁可不写 finding，
避免误报污染 distill 和 hint**。

**4 个评估维度**：
1. 响应数据可比性
2. 资源权限级别清晰度
3. 访问控制意图可推断性
4. 证据充分性

**三档行为**：
- 高置信度 → write_finding（confidence: high）
- 中置信度 → write_finding（confidence: medium），在 `evidence.reasoning` 字段说明推理依据
- 低置信度 → 不写 finding，`done({"reason":"no_pattern_match"})`

宁可漏报（下次再判），不要误报（污染下游）。

## done 系统校验

系统强约束（不通过则 user message 注入让你继续）：
- 至少跑过一个写 fact/boundary 的工具（即 Step 2 之后）
- `done.reason` ∈ `{finding_written, no_pattern_match}`（agentic 简化：原 `all_differ` /
  `heuristic_skip` 已下线，全部归到 `no_pattern_match`；细节由你在 take_note / reasoning 自由表达）
- `reason=finding_written` 必含 `dedup_key`，且 finding 表中存在该记录

## 常见坑

1. **3xx 重定向**：见上文"成功访问"条件 1。`302→/login` 算被拒。
2. **超 budget 立刻 done**：单 BAC 子 ReAct max_steps 由 yaml 配置；Step 5 已判定就剩 1-2 步直接走 Step 6→7。

## 完整示例

> 下面示例用 `api.example.com:8080` 等通用占位符；实际跑时把 user prompt 里真实的 host/path/cookie 套进去即可。

### 示例 1：anonymous 反误判（最易踩的坑）

```
endpoint: POST /admin/users/delete
- anonymous: 200, {"message":"User 3 deleted","success":true}
- admin:     200, {"message":"User 3 deleted","success":true}
- alice:     200, {"message":"User 3 deleted","success":true}
- bob:       200, {"message":"User 3 deleted","success":true}
```

❌ 错判 A：路径含 `/admin/` + 低权限能访问 → `vertical_priv_esc`
❌ 错判 B：多个用户都能访问 → `horizontal_priv_esc`
✅ 正判：anonymous 成功 → `bac.unauthorized_access`（admin 是合法访问，不计入 violators）

```json
{
  "kind": "bac.unauthorized_access",
  "severity": "critical",
  "title": "管理员删除用户接口可被未授权访问",
  "target": {"host": "api.example.com:8080", "method": "POST", "path": "/admin/users/delete"},
  "evidence": {
    "violating_identities": ["anonymous", "alice", "bob"],
    "responses": [
      {"identity": "anonymous", "status_code": 200},
      {"identity": "alice", "status_code": 200},
      {"identity": "bob", "status_code": 200}
    ],
    "reasoning": "anonymous 能成功执行删除操作，认证机制完全失效。低权限用户也能访问只是认证失效的副作用。admin 合法访问不计入 violators。"
  },
  "confidence": "high",
  "dedup_key": "bac.unauthorized_access:api.example.com:8080:POST:/admin/users/delete"
}
```

### 示例 2：垂直越权

```
endpoint: GET /admin/users
- anonymous: 401, {"error":"Unauthorized"}
- admin:     200, {"users":[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"}]}
- alice:     200, {"users":[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"}]}
```

判定：anonymous 被拒 → 进入越权；高权限资源（全用户列表）+ alice (低权限) 成功 → `bac.vertical_priv_esc`

```json
{
  "kind": "bac.vertical_priv_esc",
  "severity": "high",
  "title": "普通用户可访问全用户列表",
  "target": {"host": "api.example.com:8080", "method": "GET", "path": "/admin/users"},
  "evidence": {
    "violating_identities": ["alice"],
    "responses": [{"identity": "alice", "status_code": 200}],
    "reasoning": "anonymous 401 被正确拒绝；alice (低权限) 拿到全用户列表 (高权限资源)。"
  },
  "confidence": "high",
  "dedup_key": "bac.vertical_priv_esc:api.example.com:8080:GET:/admin/users"
}
```

### 示例 3：水平越权（注意排除合法 owner）

```
endpoint: GET /api/v1/orders/7
- anonymous: 401, {"error":"Unauthorized"}
- alice:     200, {"order_id":7,"amount":100,"buyer":"alice"}
- bob:       200, {"order_id":7,"amount":100,"buyer":"alice"}
```

判定：anonymous 被拒；URI 含资源 ID；alice/bob (同 role) 返回相同私有数据；
**`buyer` 字段值是 "alice"，alice 的 identity.name 也是 "alice" → alice 是合法 owner，从 violators 剔除**；
仅 bob 是真越权 → `bac.horizontal_priv_esc`

```json
{
  "kind": "bac.horizontal_priv_esc",
  "severity": "high",
  "title": "用户可查看他人订单详情",
  "target": {"host": "api.example.com:8080", "method": "GET", "path": "/api/v1/orders/7"},
  "evidence": {
    "violating_identities": ["bob"],
    "responses": [
      {"identity": "bob", "status_code": 200}
    ],
    "reasoning": "anonymous 401 被正确拒绝；订单 buyer=alice 是合法 owner（不计入 violators）；bob (同 role user) 拿到相同订单数据 → 真水平越权。"
  },
  "confidence": "high",
  "dedup_key": "bac.horizontal_priv_esc:api.example.com:8080:GET:/api/v1/orders/:id"
}
```

### 示例 4：无漏洞（每用户访问自己的数据）

```
endpoint: GET /api/v1/me/profile
- anonymous: 401, {"error":"Unauthorized"}
- alice:     200, {"user_id":123,"name":"Alice"}
- bob:       200, {"user_id":456,"name":"Bob"}
```

判定：anonymous 被拒；alice/bob 各取自己的数据 → 访问控制正常 → `done({"reason":"no_pattern_match"})`

走 Step 4 `summary.above_min == 0` 路径直接 done(no_pattern_match)。

### 示例 5：3xx 重定向（被正确拒绝）

```
endpoint: GET /admin/settings
- anonymous: 302 Location:/login, ""
- admin:     200, {"settings":{"max_upload_size":10485760}}
- alice:     302 Location:/login, ""
```

判定：anonymous 和 alice 都 302→/login 算被拒；只有 admin 能访问 → 正常 → `done({"reason":"no_pattern_match"})`
