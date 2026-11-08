# 阶段 4/6 · naming-overhaul 交付报告

基线：e1462878（阶段 3 之后）· 分支 pipeline/revamp · 2026-11-08

## 命名事实基线

- Go：包名小写单词；持久化层统一 `Store` + `Get*/List*/Count*/Create/Update*/Set*` 动词族（实测 19 个 `Get*` 读方法，零混用）；导出常量 `PascalCase`；JSON tag 全量 snake_case（289 个键零例外）。
- 终态词：`completed / aborted / error / failed / exhausted` 为 DB CHECK 约束事实（agent_run、task、exploration_node、assignment 派生态），`done` 已于迁移 0182 退出取值域。
- 前端：页面组件 `*Page` 具名导出 + `router.tsx` lazy 引入；组件文件名 = 导出名（PascalCase）；领域工具模块 camelCase。
- 内部一致性优先于教科书：同一概念全站一个叫法。

## 改名清单（按六判据）

### 1. 误导性

| old → new | 理由 |
|---|---|
| 下发来源取值 `self` → **`direct`**（DB 值 / Go `SourceSelf`→`SourceDirect` / wire `source=` / TS 类型 / CSS `--source-self`→`--source-direct`） | **用户追加要求**。`self` 在工程语境多义（self-hosted / self-signed / 语言关键字），读不出「用户直接给出目标」的含义，且与已存在的路由 `/conversations/direct` 不同名——前端为此在 3 处手写 `self ↔ direct` 映射。`direct` 与 `proxy` 构成对称的「目标从哪来」轴：直接下发 vs 从代理流量派生。中文文案「自主下发」不变 |
| `agentrun.Store.SetDone` → `SetCompleted` | 写入的是 `status='completed'`；`done` 不在 `agent_run_status_check` 取值域，名字说的和做的不是一回事。错误信息 `set done` 同步 |
| 注释中 7 处 `→ done` 状态机描述 → `completed` | 同上，指向不存在的取值；`loop.go` 补全遗漏的 `exhausted` 终态（与代码判断条件核对一致） |
| `TraceMeta.Duration`（json `duration`，`time.Duration` 序列化为纳秒）→ `DurationMs int64`（`duration_ms`） | 键名不带单位而值是纳秒，读 jsonb 的人会按秒/毫秒误读；全库其余耗时键均为 `*_ms` |
| `TraceMeta.TotalCount`（`total_count`）→ `RequestCount`（`request_count`） | 计的是轨迹内 HTTP 请求条数，`total` 不说明总的是什么 |

### 2. 不一致

| old → new | 理由 |
|---|---|
| 任务表/任务详情来源徽章「自主 / 代理」→「自主下发 / 代理模式」 | 同一概念两种长度；新增 `lib/source.ts`（`sourceLabel/sourceColor/SOURCE_OPTIONS/conversationPath`）为单一真相源，5 处渲染 + 3 处筛选下拉 + 3 处会话跳转全部收口 |
| 对话页控制「暂停/继续/停止**扫描**」、时间线「扫描已暂停 / 扫描完成…」→「…**任务**」 | 任务页同一组操作叫「暂停/继续/停止任务」，控制的是同一个 task 状态机；10 处文案 + 测试同步 |
| 接收者 `dispatch.Service` `a`→`s`、`dispatch.Scheduler` `r`→`s`、`evaluator.PromotionGate` `v`→`g`、`planner.Engine` `i`→`e`、`seed.SkillsSeed` `r`→`s`（共 94 处，gopls 带冲突检查） | `a`(adapter)、`v`(verifier)、`i`(intelligent planner)、`r`(runner) 是类型改名前的遗名，与类型名无关联 |

### 3. 偏离惯例

| old → new | 理由 |
|---|---|
| `pages/SystemConfig.tsx` → `SystemConfigPage.tsx`（含测试） | 14 个页面中唯一不带 `Page` 后缀 |
| `components/ui/badge.tsx`、`switch.tsx` → `Badge.tsx`、`Switch.tsx` | 组件文件名 = 导出名 PascalCase，全站其余组件一致 |
| `nodes/ExplorationNode.tsx` → `ExplorationNodeCard.tsx` | 导出名是 `ExplorationNodeCard` |
| `layout/Sidebar.tsx` 导出 `SidebarContent` → `Sidebar`（`SidebarContentProps`→`SidebarProps`） | 文件名 = 导出名 |
| `SkillsPage` / `CredentialsPage` / `LogsPage` 默认导出 → 具名导出 | 全站唯三的默认导出；默认导出可在导入处任意改名，破坏可检索性 |

### 4–6. 语义缺失 / 抽象错位 / 可读性

本轮无独立发现（`TraceMeta` 两字段已归入判据 1）。

## 契约面破坏（单列）

| 面 | 变更 | 交付 |
|---|---|---|
| DB `assignment.source` 取值 + `assignment_source_check` | `self` → `direct` | 迁移 **0198**（先放宽约束再搬数据；down 可回卷）。本地实测 up → down → up 往返成功，存量行正确迁移 |
| HTTP API query / JSON 值 | `GET /conversations?source=`、`GET /tasks?source=`、`GET /findings?source=` 以及各响应中的 `source` 字段：`self` → `direct` | 前后端同波；不保留 `self` 兼容别名 |
| 前端深链 | `/findings?source=self` → `?source=direct`（会话菜单「漏洞」跳转生成）；**旧书签中的 `source=self` 筛选将匹配为空** | 路由 `/conversations/direct` 本身未变 |
| `exploration_node` 观察载荷 jsonb | `execution_trace.meta.duration`(ns) / `total_count` → `duration_ms` / `request_count` | 全库无读取方（planner 显式剔除 execution_trace，前端未消费）；存量行保留旧键，仅审计可见 |
| 防回流 | 术语门禁新增 `SourceSelf`、`source-self` | |

## 存疑未改

| 项 | 原因 |
|---|---|
| 包名口吃 `dispatch.DispatchBatch` / `DispatchScheduled`、`distill.DistillTask`、`skill.SkillByCode`、`conversation.ConversationID` | 动词或领域名恰与包名同词：`dispatch.Batch` 读不出动作、`distill.Task` 像类型；去口吃后反而更费解，属可读性取舍，不过判据 |
| `configstore` 包实际只缓存 agent + skill 两类配置 | 名字偏宽但不误导（它确是配置存储），与 `settingstore`/`llmstore` 平级；改名收益不足以覆盖 8 处引用的心智迁移，记为观察 |
| `providerForm.tsx` 小写文件名导出组件 | 文件主体是表单常量与校验逻辑，`ProviderFormBody` 是附带组件，按工具模块命名合理 |
| 对话 vs 会话 | 页面级概念统一叫「对话」（21 处），「会话」仅用于登录会话/凭证会话材料等非对话语义（5 处），两词指称不同对象，非不一致 |

## 追加 2：来源中文名与定时任务来源缺陷

**缺陷（从代码确认）**：定时模板的唯一写入方 `DispatchScheduled` 只写用户编写的目标描述，但调度器把克隆批次写成 `source=proxy`。任务列表用 `CASE WHEN schedule_id …` 在显示层改判为 direct，对话列表与漏洞筛选则读库里的 proxy——同一个定时任务在任务表显示人工下发，对话却落在代理 tab、漏洞要按代理来源才能筛出。

**彻底修复**：
- 根因：`Scheduler.fireOne` 克隆批次改写 `SourceDirect`
- 删补丁：`task/store.go` 两处 `CASE` 改判与一处筛选特例删除，各页面统一读真实值
- 存量：迁移 **0199**（`schedule_id IS NOT NULL AND source='proxy'` → direct，可回卷；本地以探针行实测 up→down→up 往返正确）
- 测试：`TestFireDue_BriefItem_ExpandsTaskAndAgent` 新增来源断言（先红后绿）；测试搭建补注入 conversation store——**阶段 3 转介的 dispatch 集成测试 nil panic 随之解决**，该包集成测试首次全过

**中文名「自主下发 / 代理模式」→「人工下发 / 代理捕获」**（判据 1 + 2）：字段在界面上统一叫「来源」，取值须回答「任务从哪来」——人工下发（对话、批量、定时：目标都由人编写）/ 代理捕获（代理抓到流量后自动建任务）。去掉「自主」与 AI 自主运行的歧义，「X 模式」改为与另一侧对称的来源描述。评估过程中排除：目标模式（「目标」与探索图节点撞名、描述的是载荷不是来源）、直连模式（强调网络路径，非真实区别）、主动/被动（代理捕获任务会主动发送攻击载荷，「被动」误导授权边界）、用户下发 / 任务下发（用户语感否决；后者与任务模块撞名）、手动下发（用户确认定时也属人工，「人工」比「手动」更不易被理解为逐次点击）。覆盖 46 个文件：`lib/source.ts`、对话页 tab/副标题/空态、`bac` 技能手册、注释与测试；代码取值 direct/proxy 与路由不变。

**遗留**：调度器 `expandTrafficItem`（host / 流量 id 条目）在全库找不到模板写入方，仅测试可达——转介 quality-governance 判定去留。⚠ `skills/vuln/bac/SKILL.md` 文案变更需 `make reseed` 进已初始化的库。

## 退出依据

| 轮 | 切入点 | 新发现 |
|---|---|---|
| 1 | 目录树 / 文件名 ↔ 导出名 | 8 |
| 2 | 类型声明 / 接收者 / 包名口吃 | 6 |
| 3 | 函数签名（动词族、布尔语义） | 1（SetDone） |
| 4 | 字段 / JSON tag / 常量 / 终态词 | 3 |
| 5 | 前端组件与文案（来源、控制动作） | 2 组 |
| 6 | 合并全部检测器的收敛复扫 | 6（一处来源徽章 + 5 处测试注释 done，均为前轮未覆盖的文件） |
| 追加 | 用户要求：`self` → `direct` 全量替换 | 1 组（契约面） |
| 追加 2 | 用户要求重评中文名 → 「人工下发 / 代理捕获」；评估中发现定时任务来源缺陷 | 1 组文案 + 1 个缺陷 |
| 7 | 复扫 + `\bself\b` 全库散文检索 | **0** |

验证门（最终）：

| 项 | 结果 |
|---|---|
| go build / vet / vet -tags=integration / gofmt / golangci-lint / 术语门禁 | ✅ / ✅ / ✅ / ✅ / 0 issues / ✅ |
| `go test -race -short ./...` | ✅ 48 包 |
| 集成测试（受影响包：assignment / task / conversation / finding / agentrun / explorationgraph / executor） | ✅ 7/7 |
| 前端 tsc / vitest / build | ✅ / 73 文件 594 用例 / ✅ |
| 迁移 0198 往返 | ✅ up → down → up，存量行 `self`↔`direct` 正确 |

未做：真实浏览器回归（本阶段文案/路由改动已由组件测试覆盖，最终功能验证由阶段 6 e2e-verification 承担）。
