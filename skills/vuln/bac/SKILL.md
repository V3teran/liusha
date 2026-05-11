---
name: bac
category: web
description: 访问控制失效（Broken Access Control）—— 未授权访问 / 水平越权 / 垂直越权。给挖掘方向与判定原则，不框定具体工具与步骤。
---

# 访问控制失效（BAC）

## 漏洞本质

用户能否访问/操作不属于自己的资源。三种形态：

- **未授权访问 Unauthorized**：anonymous（无凭证）能拿到需要认证的资源
- **垂直越权 Vertical**：低权限角色能拿到高权限专属的资源
- **水平越权 Horizontal**：同级用户能拿到别人的私有资源

## 挖掘方向（思路，不是步骤）

BAC 的核心动作只有一个：**多身份重放对比**。用什么身份、几个、对哪些请求做、按什么顺序——你自己根据流量判断。下面是倾向性的判断，不是规则：

- 流量本身没带任何凭证 → 多半是公开接口，BAC 通常不适用
- 写操作（create / update / delete）的 BAC 危险度通常 > 读操作
- 路径或 body 里出现资源标识符（任何 ID、文件名、用户名）→ 多半要测水平越权
- 路径或角色语义指向特权域（管理后台、系统设置、批量操作）→ 多半要测垂直越权

`read_credentials` 能拿到的身份组合往往是 anonymous + 若干普通用户 +（可能）管理员。怎么用、用几个，自己拿捏。**身份数量不足 2 个（只有 anonymous）时，只能判 Unauthorized，无法判越权类**——越权判定需要至少 2 个非匿名身份做对比。

## 重要前提：anonymous 与占位 token `lstoken`

测匿名访问时，按 `read_credentials` 工具的约定，用占位 token **`lstoken`** 替换 cookie 值跑 curl——这能精确触发服务端"token 校验失败"分支（比完全无 cookie 走"未登录"分支更接近真实未授权场景）。

**`lstoken` 等同于无凭证**：anonymous 身份的请求头里看到 `Cookie: lstoken` 不代表认证成功，只是占位符。判断 anonymous 是否能成功访问时**只看响应状态码和响应内容，不看其请求头里的 Cookie 值**。

## 判定原则（关键 5 条，按优先级）

### 1. anonymous 能访问 → 必判 Unauthorized（最高优先级）

哪怕同时还有低权限用户也能访问、哪怕 URI 含 `/admin`——只要 anonymous 拿得到业务数据，就只能是 Unauthorized Access。理由：anonymous 能进意味着认证机制本身失效，更具体的越权分类失去意义。

### 2. "成功访问"必须双重判断（缺一不可）

仅靠状态码会大量误报。同时满足：

- **HTTP 状态**：2xx（含 200 / 201 / 204 / 206），明确排除 4xx / 5xx
- **响应内容**：包含业务数据，不是 `{"error": "..."}` / `{"detail": "Unauthorized"}` / 空响应——状态码 200 + 错误体仍是被拒绝

### 3. 资源权限本质判断（不要靠路径模式匹配，看响应数据语义）

不要因为 URI 含 `/admin` 就认定高权限资源、含 `/user` 就认定用户私有。要看**响应内容的本质属性**：

- 系统配置 / 全局设置 / 所有用户列表 / 审计日志 → 高权限资源（用于判 vertical）
- 个人资料 / 订单 / 私有文件 / 个人设置 → 用户私有资源（用于判 horizontal）

**看数据语义，不仅看体积**：两个身份返回 body 大小相同不代表是同样数据。不同 user_id 但字段结构相同 → 各看自己的数据（无漏洞）；不同身份拿到**相同** ID 的同一份私有数据 → 越权（他们不可能都是所有者）。

### 4. 重定向需要看 Location 综合判断

3xx 不能一刀切：

- 302 → 登录页或登录态相关 URL → 访问控制在起作用，**不算**成功访问
- 302 → 业务页面 → 看 Location 和最终响应内容综合判断
- 301 永久移动 → 通常不算成功访问原始资源

### 5. 反直觉：多身份都能访问 ≠ Horizontal

像 `/api/admin/user/delete` 这类接口，如果 anonymous + admin + 普通用户全都能成功执行——这是 Unauthorized（认证失效），**不是** Horizontal、也**不是** Vertical。anonymous 能进就触顶最高优先级，所有具体的越权分类都被吸收。

## 误报排除

提交 finding 前先过一遍：

- 静态资源 / 健康检查 / 公共信息（商品列表、文章列表、登录页本身）
- 3xx 重定向到登录页
- 响应虽 200 但 body 是错误消息或空内容
- 单纯"返回不同数据"——是用户看到自己的数据，不是越权

## write_finding 红线

evidence 必须包含：

- **multi-identity replay 矩阵**：每个测试身份的 status + 关键响应字段对比，不要只贴违规身份那一条
- **泄露的敏感字段名**（user_id / email / phone / token 等业务字段）
- **repro_cmd**：用违规身份的最小化 curl 复现命令，别人 copy 就能跑出同结果
- 涉及 anonymous 越权时，违规身份字面值就是 `anonymous`，不要写空串或 null

summary ≤500 字单行，详情进 evidence jsonb。
