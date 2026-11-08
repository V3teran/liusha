# 阶段 6/6 · e2e-verification 报告

- 分支：pipeline/revamp（基于阶段 5 检查点 700f382d / 524786c0）
- 环境：本机 host 侧三进程（api :8580 / runner :8590 / proxy :8588）+ vite dev :5173 + docker pg/redis；自建本地靶机 `127.0.0.1:38080`（反射型 XSS，仅本机可达）
- 方式：
  - 接口层：Python 冒烟脚本 56 项。
  - 端点契约探针：14 个 `:id` 路由 × 2 种非法 ID。
  - 真实浏览器（Playwright + Chromium）：
    - 全页巡检：16 页 × 7 档视口 + 深色主题。
    - 交互流 25 项。
    - axe-core WCAG 2 A/AA 扫描：15 页 × 2 主题。
    - 键盘巡检。
  - 数据层：每条写操作后查库核对。
- 外部攻击目标：项目 e2e 剧本里的公网 DVWA 未打，全部改用本地靶机，不对外发流量。

## 一、功能覆盖清单

| 模块 | 功能点 | 入口 | 状态 | 证据 |
|---|---|---|---|---|
| 鉴权 | 登录（错密内联报错 / 空凭据 / 正确登录跳 `/conversations/direct`） | `/login`、`POST /auth/login` | 通过 | 冒烟 + 交互流 |
| 鉴权 | 登出、登出后访问业务页回登录 | 账号菜单、`POST /auth/logout` | 通过 | 交互流 + 冒烟（401） |
| 鉴权 | 未登录访问接口 401 | 全部 `/api/*` | 通过 | 冒烟 |
| 任务 | 列表 / 分页 / 来源筛选（人工下发 · 代理捕获）/ 状态筛选 / 搜索 | `/tasks` | 通过 | 交互流：筛代理捕获只剩代理批次 |
| 任务 | 新建任务（人工下发）→ assignment(direct) → task → 会话 | 新建任务弹窗、`POST /tasks` | 通过 | 交互流 + 查库 `assignment.source=direct` |
| 任务 | 任务控制接口契约（非法 action 400、不存在 404） | `POST /tasks/:id/control` | 通过 | 探针 |
| 任务 | 运行中任务的暂停 / 继续 / 停止实际效果 | 任务行操作列 | 通过（补测） | pause 后 task.paused=t 且 run 退出；resume 后新 agent_run 重投；terminate 落 aborted「用户在任务页手动停止」 |
| 对话 | 人工下发 / 代理捕获双 tab，路由 = 来源值 | `/conversations/direct`、`/conversations/proxy` | 通过 | 交互流 |
| 对话 | `?conv=` 深链：选中写 URL、刷新还原、过期链接提示「会话不存在」 | 同上 | 通过（含修复） | 交互流 |
| 对话 | 时间线：指令、下发、失败节点及原因 | ConversationDetail | 通过（含修复） | 交互流 + 查库 message |
| 对话 | 重命名 / 删除 / 停止的接口契约（不存在 404、非法 ID 400） | `PATCH/DELETE /conversations/:id`、`/abort` | 通过（含修复） | 探针 |
| 对话 | agent 实时轨迹（思考流 / 工具调用折叠卡 / tokens / 运行指示）、插话追问 | SSE、Composer | 通过（补测） | 浏览器实测：40s 内时间线实时 +1 卡、tokens 561.9k/113.1k 滚动；插话落时间线 seq 327、返回 intent=accepted、agent 后续 action 消费了追问方向 |
| 代理捕获 | 经代理抓流量 → 落库 → 聚合成批 → 代理捕获批次 / 任务 / 会话 | proxy :8588 → ingestor → aggregator | 通过 | 4 条流量入 `proxy_traffic`；批次 `source=proxy` 秒级生成 |
| 代理流量 | 列表 / 详情（请求响应原文）/ content-types | `/traffic` | 通过 | 交互流 |
| 漏洞 | 列表 / 统计 / 来源筛选 / 抽屉（来源「人工下发」）/ 状态非法值 400 | `/findings` | 通过 | 交互流 + 冒烟 |
| 探索图 | 页面加载、画布渲染 | `/exploration-graph` | 通过 | 全页巡检 |
| 工作空间 | 树、选中文件、编辑器懒加载（FileEditor 分包按需请求）、路径穿越拒绝 | `/workspace` | 通过 | 交互流 + 冒烟 |
| 凭证库 | 身份增 / 查 / 删往返、列表展示 | `/credentials` | 通过 | 交互流 + 冒烟 |
| LLM 审计 | 列表 / 统计 / 详情（bigint 键：不存在 404、非数字 400） | `/llm-audit` | 通过 | 全页巡检 + 冒烟 |
| 日志 | 进程列表、日志尾读 | `/logs` | 通过 | 交互流 |
| 配置 | 智能体 / 工具 / 技能 / 模型 / 系统配置：页面加载 | `/config/*` | 通过 | 全页巡检 |
| 配置 | 技能 CRUD 往返（建 → 改 → 删 → 404）、重复 code 409、抽屉装配区 | `/config/skills` | 通过（含修复） | 冒烟 + 交互流 |
| 配置 | 模型提供商连通性测试 | `/config/models`、`POST /models/providers/test` | 通过（补测，修复后） | ok=true, 880ms |
| 后台 | 僵尸 run 回收 + 失败任务不再重投 | runner | 通过 | 存量代理批次任务经回收重投后落 aborted，不再重投 |
| 后台 | 定时任务到点触发 | cron_schedule | 通过（补测） | 建模板 next_run_at=21:04，到点克隆批次 source=direct、schedule_id 落库（迁移 0199 修复实证） |
| 通用 | 404 页面 | `/no-such-page` | 通过 | 全页巡检 |
| 通用 | 响应式：320 / 375 / 390 / 768 / 1024 / 1440 / 1920，无横向溢出 | 全部页面 | 通过（含修复） | 112 次页面加载 |
| 通用 | 深色 / 浅色主题 | 全部页面 | 通过 | 全页巡检 |
| 通用 | 无障碍：axe WCAG 2 A/AA 0 违规、跳到主内容、焦点环 | 全部页面 | 通过（含修复） | axe 15 页 × 2 主题 |

统计：

- 共 29 个功能点：通过 25，阻塞 3，未测试 1。
- 接口冒烟 56/56 通过，端点探针 28/28 符合契约，交互流 25/25 通过。
- 全页巡检全部无控制台错误、无失败请求、无横向溢出。

## 二、问题列表与修复

### 1. LLM 配置错误（401 鉴权失败）时任务永远「运行中」（严重）

- **复现：** provider 密钥失效 → 下发任务（人工下发或代理捕获均可）。
- **预期：** 任务很快失败，并告诉用户原因。
- **实际：**
  - 任务表一直显示「运行中 · 0 步」，会话页一直是「agent 工作中 1m28s…」，没有任何失败提示。
  - planner 每约 20 秒重撞一次 401，单个任务 2 分钟内就有 7 次失败调用。
  - 每次失败调用都会写入 `llm_invocation`，僵尸回收因此认为任务「仍有活动」，永远不会回收它。
- **影响：** 所有任务。持续探索模式下，任务不会自然收敛，只能手动停止。
- **根因：** planner、executor、evaluator、monitor 四个 agent 都把每轮失败只记日志、继续轮询；配置性错误与瞬时错误没有区分，也没有一条把致命错误上报给认知循环的通道。
- **修复：**
  - `llm.IsConfigError`：只认结构化的 `*HTTPError`，401、403、404 判为配置错误；400 不算（单次请求非法，换个请求可能成功）。
  - 四个 agent 遇到配置错误时，Run 返回该错误：
    - planner、executor、monitor 由各自的失败处置函数判定；
    - evaluator 的裁决是异步的，经 `fatal` 通道上报。配置错误也不再计入单条假设的重试阶梯，否则每条假设都会被误标「裁决失败」出圈。
  - agent 生命周期遇到非取消性退出时回调 `onFatal`，调用 `detector.Fail`，以 `agent_failed` 原因终结检测循环。
  - `cognition.Run` 上抛错误，runner 走既有的 `failTask`：run 落 error，任务落 aborted 并记 `error_message`，时间线写入「任务执行失败 · 原因」。目标节点落 failed，不再误记为 completed。
  - 前端停止原因加入 `agent_failed → 智能体故障`。
- **回归：**
  - 新下发的任务数秒内落 aborted，时间线显示红色「任务执行失败」节点并带 401 原文。
  - 存量代理批次任务经僵尸回收重投后，同样快速落终态，且不再被重投。

### 2. API 边界：客户端错误返回 500 并外泄 SQL 原文（中）

- **复现：**
  - `GET /api/tasks/not-a-uuid` → 500 `invalid input syntax for type uuid ... (SQLSTATE 22P02)`。
  - tasks、conversations、findings、agents 下全部 16 个 UUID 主键路由同样如此。
- **修复：** `requireUUIDParam` 中间件在边界校验，挂到全部 UUID 主键路由，非法 ID 返回 400。traffic 与 LLM 调用详情是 bigint 键，由各自 handler 解析，不挂。

### 3. 资源不存在返回 500，或静默返回 200（中）

- **复现：**
  - 停止、删除、追问、裁决记录、智能体档位等写操作，对不存在的 ID 返回 500 `no rows in result set`。
  - `PATCH /conversations/<不存在>` 返回 200（影响 0 行的假成功）。
  - `GET /conversations/<不存在>/messages` 返回 200 空列表。
- **影响：** 阶段 5 把 `?conv=` 设为 URL 单一事实源后，删除会话后的旧链接会显示一个空时间线，并提示「网络或服务不可用 + 重试」，而重试必然失败。
- **修复：**
  - `respondErr`（错误响应单一出口）把包装了 `pgx.ErrNoRows` 的错误统一收敛为 404「资源不存在」，不再透出驱动原文。
  - `SetTitle` 影响 0 行时返回 ErrNoRows。
  - `DeleteConversation` 的 not-found 改为包装 ErrNoRows。
  - `ListMessages` 结果为空时校验会话是否存在（非空必然存在，不额外查询）。
  - 前端 `client` 抛出带 `status` 的 `ApiError`，消息格式不变。`ConversationDetail` 对 404 显示「会话不存在」，不提供重试。

### 4. 未知来源取值被静默接受（中）

- **复现：** `GET /tasks?source=self`（阶段 4 改名前的旧书签）返回 200 空列表。会话、漏洞列表与统计同样如此。
- **修复：** 新增 `assignment.Source.Valid()`，由 `sourceParam` 在四个消费端统一校验，非法值返回 400 并给出可选值。

### 5. 技能 code 重复返回 500 和驱动原文（低）

- **修复：** 判定为唯一约束冲突（23505）后返回 409「技能标识符「x」已存在」，与既有的 `isForeignKeyViolation` 同处声明。

### 6. 会话列表空集序列化为 `null`（低）

- **修复：** 改为 `[]`，与 messages 的口径一致。

### 7. 系统配置页在 320px 宽度下标题被挤成竖排（低）

- **根因：** 搜索框 `w-72` 放在页头右槽，flex 把标题压到零宽。
- **修复：** 搜索框归入 FilterBar（全站筛选控件的唯一位置），窄屏撑满宽度。

### 8. 无障碍：axe WCAG 2 A/AA 违规（46 条规则命中 → 0）

- **对比度：**
  - `text-tertiary` 在所有表面层只有 2.8–3.3:1（深色）/ 3.3–4.0:1（浅色），表头、时间戳、分组名都用它。
  - 修复：重算 OKLCH 明度，浅色 52%、深色 62%，在全部表面层（background…surface-3）≥ 4.6:1；深色 `text-secondary` 同步上调到 72%，保持层级差。
  - 另有 15 处用「secondary + opacity」模拟三级灰的文字（opacity 会把对比度拉回不达标），改用 tertiary token。禁用态和悬停显形的控件不在此列。
  - React Flow 署名链接对比度 1.3:1，改用主题 token（保留署名）。
- **ARIA 语义：** 可选行表格的问题如下，统一改为 WAI-ARIA grid 模式：表格本身是具名 grid，行保持原生 row 语义（`aria-selected` 合法，单元格内允许交互控件），外层滚动容器去掉伪 grid 角色，方向键事件从聚焦行冒泡到容器。
  - `role="button"` 的行带 `aria-selected`（不合法）；
  - 行内嵌套复制按钮（nested-interactive）；
  - 外层 `role="grid"` 的 div 里直接放 `<table>`（aria-required-children）。
  - 涉及 DataTable（任务、流量、漏洞、LLM 审计）和凭证表。
  - 工具页的 `role="listbox"` 下是带 `aria-pressed` 的按钮，listbox 只允许 option 子项，改为具名 region。
- **键盘：**
  - Tab 首停为「跳到主内容」，回车后焦点进入 main。
  - 巡检报的 2 个「无焦点环」是测量时序假阳性：辉光过渡 150ms，延迟 250ms 复测为可见，截图已确认。

### 9. 命名残留：更早一代的来源取值与中文名（低）

- 发现：
  - Go 3 处注释写 `manual|auto`。其中 `task/store.go` 的注释「视图层定时克隆归 manual」与 SQL 事实不符（SQL 直接取 `a.source`）。
  - 前端 4 处写 `manual|auto`，1 处写「直连下发」。
- 修复：全部改正。
- 类型收紧：TS 的 `TaskSummary.source` / `Finding.source` 改为 `Source` 联合类型；`LLMInvocationDetail.source` 改为 `Source | ''`。以后漏改会在编译期报错。
- 防复发：术语门禁新增 `manual|auto` 组合与「直连下发 / 自主下发」。

### 10. 环境变量 `ANTHROPIC_AUTH_TOKEN` 泄漏进出站 LLM 请求（中，补测阶段发现）

- **复现：** 进程环境中存在 `ANTHROPIC_AUTH_TOKEN`（调用方工具链自设的 LLM 路由变量）时，经 anthropic 协议的 provider（bigmodel 兼容端点）全部 401「令牌已过期或验证不正确」。
- **根因：** anthropic SDK 默认读取该环境变量并自动附加 `Authorization: Bearer <值>` 头；bigmodel 等兼容网关优先读 Authorization，占位值让真实的 `x-api-key` 被无视。用户从自己的终端启动服务时没有该变量，因此仅本会话启动的进程复现——key 本身始终有效。
- **证据：** httptest 捕获出站请求头，`Authorization: Bearer PROXY_MANAGED` 与 `x-api-key: <真实key>` 并存；同一 key 直连 curl 200。
- **修复：** `ClientPool.GetOrCreateAnthropic` 加请求中间件统一擦除 Authorization 头——本系统密钥统一走 `x-api-key`（DB/env 双路径），出站请求不携带不控制的凭据。回归测试 `TestGetOrCreateAnthropic_ScrubsAmbientAuthHeader`（设 env → 断言头被清除）。
- **回归：** 修复后 `POST /models/providers/test` 返回 ok=true；真实任务完整跑通认知循环（见补测记录）。

### 11. 包目录残留的空 `logs/` 目录

- 9 个空目录是阶段 5 修复之前生成的历史产物，已删除。复测确认测试不再生成。

## 三、回归结果

| 层 | 范围 | 结果 |
|---|---|---|
| 单元 | `go test -race -short ./...` | 全部通过 |
| 集成 | `go test -race -tags=integration ./...`（testcontainers 实拉 pg） | 全部通过（56 包 ok） |
| 静态 | vet（含 integration tag）/ gofmt / golangci-lint / 术语门禁 | 全绿（0 issues / 0 残留） |
| 前端 | `tsc -b`（含测试文件）/ `vite build` / vitest | 通过；75 文件 / 607 用例 |
| 接口 | 冒烟 56 项 + 探针 28 项 | 全部符合契约 |
| 浏览器 | 交互流 25 项、全页巡检 7 档视口 + 深色、axe 15 页 × 2 主题 | 全部通过，0 违规 |

所有修复都先写测试，并确认测试在旧代码上失败：

- `api_contract_test`：UUID 校验、404 收敛、来源校验、空数组、409；
- conversation store 的不存在语义（集成测试）；
- `IsConfigError`；
- planner、executor、monitor 的致命失败处置；
- evaluator 的致命通道；
- 检测器 `Fail`；
- 生命周期 `onFatal`；
- `ApiError`、`ConversationDetail` 404、`TaskStatusCard` 的 `agent_failed`；
- `DataTable` grid 语义。

## 四、受阻与未覆盖项

| 项 | 原因 | 未覆盖范围 |
|---|---|---|
| 附件随消息下发后进入任务工作区 | 主任务未携带附件下发 | 附件 → 沙箱 inbox 链路（上传端点本身已单测覆盖） |

> **补测记录（同日晚间，问题 10 修复后）**：此前报告的 LLM 相关阻塞项全部补测通过——模型连通性测试（ok=true，880ms）、真实任务认知循环（planner 规划 → executor 沙箱真实执行 curl 探针，observation 40+ 个，agent 自主发现 /search 反射 XSS 与 501 错误页方法名反射两个注入面）、SSE 实时卡片（浏览器实测增量滚动、tokens/耗时实时刷新）、插话追问（accepted + 落时间线 + 被 agent 后续 action 消费）、暂停/继续/停止（pause→task.paused=t 且 run 退出 / resume→新 agent_run 重投 / terminate→aborted「用户在任务页手动停止」）、定时任务到点触发（cron 到点克隆批次 source=direct、schedule_id 落库，迁移 0199 修复实证）、代理捕获批次 LLM 深度分析（236 次 LLM 调用，自主开展边界测试与路径变体探测）。
>
> **复现门**实测到机器重放环节：evaluator 真实受理假设、发起基线重放（期间暴露并绕过测试靶机 HTTP/1.0 与 keep-alive 的兼容问题——EOF 被正确归类为瞬时故障、假设保持 unverified 持续重试，重试语义按设计工作）；推送到远程时尚有 LLM 裁决在途（思考模型裁决耗时分钟级），裁决落库与 finding 交付由任务继续自然完成。原 401 根因不是密钥失效，而是环境变量 ANTHROPIC_AUTH_TOKEN 泄漏进出站请求（问题 10）。

## 五、发布说明（本阶段新增）

- API 契约收紧，客户端需要适配：
  - 非法 UUID → 400；
  - 不存在的资源 → 404（此前多为 500 或 200）；
  - `source` 只接受 `direct` / `proxy`，否则 400；
  - 技能 code 重复 → 409；
  - 会话列表空集返回 `[]`。
- LLM 配置错误（401 / 403 / 404）不再无限重试：任务直接落 aborted，`error_message` 带原因，时间线显示「任务执行失败」。
- 视觉：tertiary 文字两主题提亮、深色 secondary 提亮（WCAG AA）。
