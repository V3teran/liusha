---
name: bac
category: web
description: 访问控制失效（Broken Access Control）—— 未授权访问 / 水平越权 / 垂直越权。核心方法是多身份重放对比，身份来源与模式无关（passive 注入预置 token / active 登录 mint 会话）。给挖掘方向与判定原则，不框定具体工具与步骤。
---

# 访问控制失效（BAC）

## 漏洞本质

用户能否访问/操作不属于自己的资源。三种形态：

- **未授权访问 Unauthorized**：anonymous（无凭证）能拿到需要认证的资源
- **垂直越权 Vertical**：低权限角色能拿到高权限专属的资源
- **水平越权 Horizontal**：同级用户能拿到别人的私有资源

## 挖掘方向（思路，不是步骤）

**唯一铁律**：开测前先用 `lstoken` 替换所有凭证位置跑一次 anonymous 重放，看响应。判定原则 1 决定了 anonymous 能访问 → 必判 Unauthorized（触顶所有越权分类），**跳过 anonymous 直接下 vertical/horizontal 结论就是漏判**——这是 BAC 测试最常见的假阴性源。除此之外，怎么挖、用几个身份、按什么顺序——你自己根据目标请求判断。

BAC 的核心动作只有一个：**多身份重放对比**——同一个目标请求换不同身份的凭证重发、对比响应。下面是倾向性的判断，不是规则：

- 目标请求本身不需要任何凭证 → 多半是公开接口，BAC 通常不适用
- 写操作（create / update / delete）的 BAC 危险度通常 > 读操作
- 路径或 body 里出现资源标识符（任何 ID、文件名、用户名）→ 多半要测水平越权
- 路径或角色语义指向特权域（管理后台、系统设置、批量操作）→ 多半要测垂直越权

### 身份从哪来——与模式无关（关键）

"身份" = 一个你能认证进去的权限上下文，**不等于 `read_credentials` 的行**。来源有三类，能凑出几个算几个：

1. **`read_credentials` 已录入的 token**（passive 常态：tracker 启动前已 seed）
2. **brief 直接给的登录凭据对**（active 常态：commander 把"admin/password 高权、gordonb/abc123 低权"透传到 brief）→ 你登录把它**变成**可用会话
3. **recon / 探测中发现的登录入口**（任何模式：看到登录表单就能注册一个新身份）

**身份数按"可获得数"算，不是按 `read_credentials` 行数算**——active 模式 `read_credentials` 常返空 `[]`，但 brief 给了 admin + gordonb 两组账密 → **可获得身份数 = 2**，足以展开垂直越权。把空 `read_credentials` 误读成"0 身份不能测越权"是 active BAC 最大的假阴性。

**怎么把登录凭据变成可注入的会话**（active 模式优先级：字典里有的请求走 replay_flow，没有的才 curl 手拼）：
- **浏览器登录 + 流量字典（active 重放首选）**：`browser_use` 以 `identity=用户名` 登录并访问受保护资源（见 system_prompt「identity 命名铁律」）→ 该身份真实已认证请求被 CDP 抓入 http_flow（source=internal，**owner 作用域 = 整个 active run**，commander 登的 admin 请求 striker 也查得到）。`list_flows` 找关键 endpoint → `view_flow` 读**真实请求结构 + 全部凭证位置**（httpOnly cookie 也在里面，header / body / query 多处一次看全，不靠猜）→ `replay_flow(id, modifications)` 换身份 / 改字段重放，原请求所有字段自动继承。**字典里有的请求一律走这条**——它同时解掉「httpOnly 抠不出」和「请求结构靠编」两个老痛点，重放矩阵每行都是真流量改出来的。垂直越权黄金链路：commander 抓的 admin 请求 → striker `view_flow` 读结构 → `replay_flow` 把凭证换成自己低权身份的值。
- **curl 登录（字典里没有的全新 endpoint 才手拼）**：浏览器没导航过、`list_flows` 查不到的 endpoint → `GET 登录页` 抽 CSRF token（如 DVWA 的 `user_token`）→ `POST` 账密 + token → 捕获 `Set-Cookie` session，塞进 `curl -H "Cookie:"`。凭证从 `read_credentials` 拿。

身份数（含可获得的）决定能测哪几类，硬约束：

- **可获得非匿名身份数 = 0**：只能测 Unauthorized（anonymous vs 目标自带身份）
- **可获得非匿名身份数 = 1**：仍然**只能测 Unauthorized**——vertical 越权需要 ≥1 高 + ≥1 低权限，horizontal 越权需要 ≥2 个同级用户，单一非匿名身份**两类都凑不出**
- **可获得非匿名身份数 ≥ 2**：才能展开 vertical / horizontal 测试

## 重要前提：anonymous 是 LLM 临时构造的测试概念

`read_credentials` 返回的列表里**不含 anonymous**——anonymous 不是预录入的"半成品身份"，而是你在挖洞时按需构造的测试请求形态。每个 Identity 带一个 `credentials` 数组，可以同时有多条 `{type, key, value}`（如 Cookie + Authorization Bearer + X-CSRF-Token 三件套并存）。

### 怎么真实跑出 anonymous（curl 路径用 lstoken / 浏览器路径用空 jar）

anonymous **必须真发一次请求看响应**——禁止凭"受保护页应该会跳 login"脑补一行塞进矩阵（脑补的 anonymous 行 = 假 evidence，污染整个判定，违反 write_finding 红线）。按你用 curl 还是浏览器分两条执行路径。

**curl / HTTP 重放路径——lstoken 替换**，再按手上有没有凭证模板分 A/B：

**分支 A：手上有 ≥1 个真实身份的凭证**（`read_credentials` 返回的、或你登录拿到的、或目标请求自带的）→ **拿任一份 `credentials` 作模板**

例如 `admin.credentials = [{type:headers, key:Cookie, value:"session=admin_xyz"}, {type:headers, key:Authorization, value:"Bearer abc..."}]`，照这个结构构造 anonymous：

- 同样的 `type` + `key`
- 每条 `value` **整段替换为 `lstoken`**（不保留 `name=` 前缀，不保留 `Bearer ` 前缀）
- 即 anonymous 重放请求里 `Cookie: lstoken` / `Authorization: lstoken`

**全部位置都要注入**，漏一个就是假阳性（残留旧身份的认证还能进，得到的不是"匿名能进"的结论）。

**分支 B：手上没有任何真实身份凭证模板**（`read_credentials` 空、且你还没登录拿到会话）→ **从目标请求自己识别凭证位置**

看目标请求里哪些字段是认证性质：

- `headers` 的 Cookie / Authorization / X-Token / X-Auth-Token / X-Api-Key 等
- `query` 的 token / api_key / access_token / key 等
- `body` 的 password / credentials / token 等

把识别到的**所有**认证位置整段替换为 `lstoken`，构造 anonymous 重放请求。

**浏览器路径——空 jar，不套 lstoken**：用一个**从没登录过的 identity**（如 `identity:"anon"`）`open` 受保护 URL。该 identity 的 cookie jar 是空的 → 服务端按未认证处理，返回什么（302→login / 401 / 登录页本身）就是**真**的 anonymous 结果。浏览器没有可替换的 header，**别套 lstoken**——lstoken 只是 curl 路径的"token 校验失败"占位符；浏览器路径靠"这个 identity 从没登录过、jar 为空"来表达匿名。**必须真 `open` 看渲染，不能因为"DVWA 八成跳 login"就脑补一行。**

### 为什么用 `lstoken` 而不是直接删凭证

`lstoken` 精确触发服务端"token 校验失败"分支（比完全无凭证走"未登录"分支更接近真实未授权场景，有些应用对这两个分支处理不一样，只测后者会漏真实认证缺陷）。

### 判定时的注意

**判定 anonymous 是否成功只看响应**（status + body），不看请求自己带了什么凭证字段值——`Cookie: lstoken` / `Authorization: lstoken` / `?token=lstoken` 是占位符，出现在请求里不代表认证成功。**浏览器路径同理**：空 jar identity `open` 出来是登录页 / 302 就是匿名被拒，渲染出业务面板才是匿名能进——只看渲染结果，不看 identity 叫什么名。

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

**vertical 还是 horizontal，看「行为身份」与「够到的资源/功能所属权限层级」的关系，不是 URL 字样**：低权身份够到高权身份专属的资源或功能（改别人的高权数据、执行只有高权能做的操作）→ vertical；同级身份够到另一个同级用户的私有资源 → horizontal。路径或页面里的 "admin" 之类字样是应用自带的提示、真实目标里通常没有——别据此定方向，据实测出的各身份权限差异定（哪个身份能做什么、够到的是谁的东西）。

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
- **操作成功 ≠ 利用成功**：state-changing 请求返 2xx 只证明"调用被接受"，不证明你声称的下游影响成立。若 finding 的影响依赖"使用"你拿到的东西（新建的账号、提升的角色、签发的 token、改写后的数据），就**真去用一次**证明它可用，再下"提权 / 接管 / 篡改生效"结论；没验证就把结论收敛到**确实证明了的那部分**（如"未授权写入成功"），别夸大成 escalation。**警惕响应回显了你没提交的值**（你传 A、响应却是 B / 固定值）——这是输入被忽略或归一化的信号，说明操作未必如响应表面那样生效，下结论前重验

## write_finding 红线

evidence 必须包含：

- **multi-identity replay 矩阵**：每个测试身份的 status + 关键响应字段对比，不要只贴违规身份那一条；矩阵要明示**每个身份注入的所有凭证位置**（如果有 Cookie + Authorization 双位置，两条都要列），证明替换是完整的而不是只换了一条。**browser 原生访问对比**（httpOnly 抠不出 cookie，走浏览器重放那条）则列**每个 identity（=各自账号）各自 `open` 同一受保护 URL 的渲染结果对比**，矩阵的"身份"列就是 identity 名而非注入的凭证位置
- **泄露的敏感字段名**（user_id / email / phone / token 等业务字段）
- **repro_cmd**：违规身份的最小化复现——curl 路径给最小化 curl 命令（别人 copy 就跑出同结果）；httpOnly 抠不出 cookie 时走浏览器那条，给「以 identity X `open` 该受保护 URL」的最小化步骤
- 涉及 anonymous 时，矩阵里的 anonymous 行**必须来自真实重放**（curl 带 lstoken 的响应、或空 jar 浏览器 `open` 的渲染）——**禁止凭"应该会 302"脑补**，脑补行是假 evidence；违规身份字面值就是 `anonymous`，不要写空串或 null

summary ≤500 字单行，详情进 evidence jsonb。
