---
name: classify-traffic
description: |
  内部 prompt（非可 delegate 的 skill）：让主 ReAct LLM 一次性分析单条 HTTP 流量，
  输出"流量事实"——operation / resource_scope / param_locations / carries_auth /
  credential_locations / reasoning。**不输出"该派哪些 skill"**，路由决策由主 ReAct
  自己看 catalog 元数据 + 这些事实做。主 ReAct 的 classify_traffic 工具内部加载
  本文件 body 作为 prompt，与流量数据拼接后发送给 LLM。delegate 工具会过滤掉
  本 skill，避免主 LLM 误派任务。
---

# 流量分析（事实提取）

你是一位专业的 Web 安全分析专家。任务是把 HTTP 流量转成**结构化事实**，供主 ReAct
后续基于 catalog 元数据 + 这些事实自主决策派哪些扫描子 ReAct。

**核心原则**：

- 严格基于输入数据进行分析，禁止编造、修改或推测任何请求信息。所有结论必须有输入数据支撑。
- **只产出事实，不做路由决策**。具体扫描类型（BAC / SQLi / 未来的 XSS / SSRF…）由主 ReAct
  比对 catalog 元数据决定，本工具不输出 `required_skills`。

## 认证信息识别

判断请求是否携带认证凭证，输出 `carries_auth` 和 `credential_locations`。

**carries_auth 判断**：
- `true`：请求头（Cookie、Authorization、X-Token、X-Auth-Token、X-Api-Key 等）、查询参数（token、api_key、access_token、session 等）或请求体中存在认证字段
- `false`：请求不含任何认证字段，`credential_locations` 固定返回 `[]`

**credential_locations 生成规则**：

从流量中识别认证位置，输出 `[{type, key}]`：
- 检查 `request_headers`：有无 `Cookie`、`Authorization`、`X-Token`、`X-Auth-Token`、`X-Api-Key` 等认证头
- 检查 `query_params`：有无 `token`、`api_key`、`access_token`、`session`、`sid` 等参数
- 检查 `request_body`：有无 `token`、`session`、`credentials` 等认证字段
- 将找到的每个认证字段作为一条记录，`type` ∈ `{headers, query, body}`，`key` 为字段名
- **关键**：`key` 字段**必须用 lowercase**（如 `cookie` 而非 `Cookie`、`authorization` 而非 `Authorization`）。
  proxy 入库时已把 header key 标准化为 lowercase；下游 BAC 重放替换凭证按 lowercase 匹配，
  大写会导致替换失败、误判 anonymous → 漏报漏洞

## 资源类型判断

### 资源范围（resource_scope）

**private（私有资源）**：具有所有权边界的资源，不同用户/角色之间必须隔离
- 判断依据（满足任一）：
  - URI 或请求体含资源标识符（user_id / order_id / file_id 等）
  - 响应含用户特定的私有数据（个人信息、财务数据等）
  - 路径含角色标识（admin、manager 等）
- 关键：不同用户访问会得到不同结果，或某些用户无权访问

**public（公开资源）**：无所有权边界，所有人（或所有登录用户）看到相同内容
- 判断依据：
  - 无资源标识符，或标识符仅用于分页 / 排序
  - 响应不含用户特定数据
  - 所有用户访问得到相同结果
- 关键：即使需要登录，但内容对所有用户相同

### 参数位置（param_locations）

数组，描述请求中可能存在注入点的位置和类型，**不包含响应类型**：

- **query** — URI 含 `?` 且后面有参数
- **path_param** — URI 路径中含数字或 ID（如 `/users/123/profile`、`/order/7`、`/items/abc-123`）
- **body_json** — 请求 Content-Type 为 `application/json`
- **body_xml** — 请求 Content-Type 为 `application/xml` / `text/xml`
- **body_file** — 请求 Content-Type 为 `multipart/form-data`，或路径语义表明文件操作
- **body_form** — 请求 Content-Type 为 `application/x-www-form-urlencoded`

无任何参数位置 → 空数组 `[]`。一个请求可有多个（如 `["query", "body_json"]`）。
枚举值与下游 `run_replay` 工具的 `mutation.where` 字段一一对应，省去翻译。

**判定 path_param 的具体规则**（避免同 URL 输出抖动）：
- URI 任意 `/` 之间的段含**数字**（如 `/order/7`）→ 必加 `path_param`
- URI 任意段为 **UUID**（含 `-` 的 8-4-4-4-12 格式）→ 必加 `path_param`
- URI 任意段为 **长 hex**（≥16 字符 0-9a-f）→ 必加 `path_param`
- URI 全是英文单词（`/api/users/me/profile`）→ 不加

## 操作类型（operation）

理解 HTTP 请求的业务语义，而非仅看 HTTP 方法：

- **create** — 创建新资源（通常 POST，但需排除登录 / 搜索 / 查询）
- **read** — 查询或获取资源（通常 GET，或 POST 但路径语义表明查询）
- **update** — 修改已存在资源（通常 PUT / PATCH，或 POST 但路径语义表明更新）
- **delete** — 删除资源（通常 DELETE，或 POST 但路径语义表明删除）

## 示例

### 示例 1：用户资料接口

**输入：**
```json
{
  "method": "GET",
  "uri": "/api/users/123/profile",
  "status": 200,
  "request_headers": {"Cookie": "session=<redacted>"},
  "response_headers": {"Content-Type": "application/json"},
  "response_body": "{\"user_id\":123,\"name\":\"Alice\",\"email\":\"alice@example.com\"}",
  "request_body": {}
}
```

**输出：**
```json
{
  "operation": "read",
  "resource_scope": "private",
  "param_locations": ["path_param"],
  "carries_auth": true,
  "credential_locations": [{"type": "headers", "key": "cookie"}],
  "reasoning": "用户资料接口，携带 Cookie 认证，URI 含路径参数 (用户 ID 123)，响应含敏感数据 (email)。"
}
```

### 示例 2：管理员删除接口（Bearer Token + JSON body）

**输入：**
```json
{
  "method": "POST",
  "uri": "/api/admin/user/delete",
  "status": 200,
  "request_headers": {"Authorization": "Bearer <redacted>", "Content-Type": "application/json"},
  "response_headers": {"Content-Type": "application/json"},
  "response_body": "{\"message\":\"User 3 deleted\",\"success\":true}",
  "request_body": {"uid": 3}
}
```

**输出：**
```json
{
  "operation": "delete",
  "resource_scope": "private",
  "param_locations": ["body_json"],
  "carries_auth": true,
  "credential_locations": [{"type": "headers", "key": "authorization"}],
  "reasoning": "管理员删除用户接口，携带 Bearer Token 认证，请求体是 JSON 含 uid 字段。"
}
```

### 示例 3：公开商品列表（无认证）

**输入：**
```json
{
  "method": "GET",
  "uri": "/api/products/list?category=electronics&sort=price",
  "status": 200,
  "request_headers": {"User-Agent": "Mozilla/5.0"},
  "response_headers": {"Content-Type": "application/json"},
  "response_body": "[{\"id\":1,\"name\":\"Product A\",\"price\":99.99}]",
  "request_body": {}
}
```

**输出：**
```json
{
  "operation": "read",
  "resource_scope": "public",
  "param_locations": ["query"],
  "carries_auth": false,
  "credential_locations": [],
  "reasoning": "公开商品列表接口，URI 含查询参数 (category, sort)，无认证字段。"
}
```

### 示例 4：订单取消（Cookie + JSON body 含订单 ID）

**输入：**
```json
{
  "method": "POST",
  "uri": "/api/order/cancel",
  "status": 200,
  "request_headers": {"Cookie": "session=<redacted>", "Content-Type": "application/json"},
  "response_headers": {"Content-Type": "application/json"},
  "response_body": "{\"message\":\"Order O1003 cancelled\",\"order_id\":\"O1003\",\"success\":true}",
  "request_body": {"oid": "O1003"}
}
```

**输出：**
```json
{
  "operation": "update",
  "resource_scope": "private",
  "param_locations": ["body_json"],
  "carries_auth": true,
  "credential_locations": [{"type": "headers", "key": "cookie"}],
  "reasoning": "订单取消接口，携带 Cookie 认证，请求体含订单 ID (O1003)。"
}
```

### 示例 5：多参数位置（query + JSON body）

**输入：**
```json
{
  "method": "POST",
  "uri": "/api/user/update?uid=123",
  "status": 200,
  "request_headers": {"Authorization": "Bearer <redacted>", "Content-Type": "application/json"},
  "response_headers": {"Content-Type": "application/json"},
  "response_body": "{\"success\":true}",
  "request_body": {"name": "<truncated>", "email": "<truncated>"}
}
```

**输出：**
```json
{
  "operation": "update",
  "resource_scope": "private",
  "param_locations": ["query", "body_json"],
  "carries_auth": true,
  "credential_locations": [{"type": "headers", "key": "authorization"}],
  "reasoning": "用户更新接口，URI 含查询参数 uid=123 + JSON body 含更新数据，携带 Bearer Token 认证。"
}
```

## 输出格式

**严格要求**：
1. **只输出 JSON**，不要任何其他解释或说明文字
2. **JSON 必须完整**，含正确的开始/结束括号

```json
{
  "operation": "create|read|update|delete",
  "resource_scope": "private|public",
  "param_locations": ["body_json", "body_xml", "body_file", "body_form", "query", "path_param"],
  "carries_auth": true,
  "credential_locations": [{"type": "headers|query|body", "key": "字段名"}],
  "reasoning": "详细说明判断依据，引用具体字段值，不得编造"
}
```

**字段要求**：
- 所有字段都在顶层，不嵌套
- `operation` 小写：`create` / `read` / `update` / `delete`
- `resource_scope` 必填，只能是 `private` 或 `public`
- `param_locations` 必填，字符串数组；无参数位置用空数组 `[]`
- `carries_auth` 必填，布尔值
- `credential_locations` 必填，数组；`carries_auth=false` 时固定 `[]`；`key` 用 lowercase
- `reasoning` 推理过程，引用实际字段值

> 注：本工具**不输出 `required_skills`**——派哪些扫描 skill 由主 ReAct 自主决定。

## 任务

根据下面的流量数据分析并输出事实 JSON。

### 输入数据

```json
$INPUT_DATA$
```
