# AGENTS.md

面向编码 agent 的项目手册：只记录读代码推不出、或推出代价很高的事实与约束。条目以代码为准写成；发现与代码不符，以代码为准并顺手修正本文件。

先读「验证门」与「领域不变式」：前者是交付标准，后者是评审时最常被打回的地方。

## 项目一览

liusha 是多 agent 协作的自动化 Web 安全测试平台（仅限授权的安全研究与学习）。一次测试的数据流：

```
人工下发 brief ──┐
定时模板到点 ────┼→ assignment → task → agent_run → Asynq 队列 → runner：四 agent 认知循环
代理捕获流量 ────┘                                    （工具经沙盒服务执行）
                                                      → 探索图 → 复现门坐实 → finding（漏洞台账）
```

只有一个程序 `liusha`（`cmd/liusha`），组件是它的子命令，`serve` 在同一进程运行全部组件：

| 组件 | 子命令（实现） | 职责 |
|---|---|---|
| api | `api`（`internal/app/api`，gin，:8580） | HTTP API、控制台前端（`web/dist` 编入程序）、鉴权、LLM 接入首装引导、定时调度器 |
| proxy | `proxy`（`internal/app/proxy`，:8588） | MITM 代理：过滤 / 去重 / 按主机攒批 → 下发 `proxy` 来源任务 |
| runner | `runner`（`internal/app/runner`，:8590） | Asynq 消费者：跑 planner / executor / evaluator / monitor 四 agent；接收沙盒回灌流量；恢复监控与清扫器 |
| sandbox | `sandbox`（`internal/app/sandbox`，:8591） | 执行工具命令的常驻服务（令牌鉴权）；compose 时跑在本地构建的 Kali 镜像里，二进制部署（Linux / macOS / Windows）时工具即本机所装 |
| 存储 | PostgreSQL 17+（pgvector）+ Redis | PG 存业务数据；Redis 承载队列、事件流、凭证库 |

实体层级：assignment（一次下发的批次）→ task（一个目标，绑定一个会话）→ agent_run（一次执行；同一任务可有多个：暂停后继续、停机续跑、恢复重投）。

## 安全边界

- 只对授权目标测试。测试与实测用本地靶机，绑定回环或内网地址，用完即停；不要把真实第三方站点写进测试、剧本或示例。
- 密钥一律走环境变量（compose 与本地开发用仓库根 `.env`，二进制部署用 `liusha setup` 生成的环境变量文件），不进代码、配置文件、日志与提交；CI 跑 gitleaks。LLM 密钥的出站规则见「LLM 密钥」。
- 沙盒执行 LLM 编写的任意命令：命令环境去掉 liusha 的全部密钥（`LIUSHA_*` 与配置声明的密钥变量，见 `internal/sandbox/server` 的 `scrubEnv`）；新增密钥类环境变量时同步该名单。沙盒令牌只在沙盒进程内：任务进程只拿到沙盒本机接口（回环地址 + 本机口令，`localAPI`）的地址，浏览器流量经它由沙盒带令牌转发给 runner，不得为任何需求把令牌注入命令环境。沙盒端口与令牌不得对外暴露。
- `scripts/dev/e2e.sh` 每次执行都会 TRUNCATE 运行时表、FLUSHDB 并清日志——在有需要保留数据的库上不要跑；只想跑剧本用 `make e2e-xss` / `make e2e-full`（不清库）。
- 本地实测创建的任务、定时模板、临时 provider 用完清理（停止 / 删除），不留下会持续触发或消耗 LLM 额度的东西。

## 仓库地图

- `cmd/liusha`：唯一程序入口（子命令分发、`-env-file` / `-config`）；`internal/app/<组件>` 是各组件的装配（`Run(ctx, cfg)`），`internal/app.Serve` 按层编排同进程运行。`cmd/e2e` 是开发用的端到端剧本触发器。
- `internal/<领域>`：按领域分包（`assignment`、`task`、`agentrun`、`cronschedule`、`dispatch`、`explorationgraph`、`finding`、`credential`、`llmconfig`……）。agent 核心在 `planner` / `executor` / `evaluator` / `monitor` / `cognition`，框架层在 `framework`；runner 的任务收尾、恢复监控在 `internal/runner`。
- `db/migrations`：迁移 SQL，经 `migrations.FS` 编进程序，由 `internal/db.Migrator`（golang-migrate 库，`schema_migrations` 表）执行；`0196_baseline` 是压缩后的基线。
- `internal/builtin`：出厂内容，编译进程序——`agents/<code>.md`（四个 agent 的出厂配置 + 章程）、`skills/<分类>/<code>/SKILL.md`（内置技能）。各 agent 的输出契约在 `internal/{planner,executor,evaluator}/contract`。
- `internal/override`：「出厂默认 + 用户自定义」的字段级引擎；`internal/overridestore` 是自定义表的读写协议。
- `internal/config/config.yaml`：编进程序的内置默认配置（列出全部键）；部署时用覆盖文件只写改动的键，或用 `LIUSHA_<键>` 环境变量。密钥一律走环境变量。
- `internal/tools/manifest/tools.yaml`：沙盒 CLI 工具清单（编进程序）；沙盒镜像按它安装，沙盒按它探测已安装工具。
- `internal/sandbox`：沙盒协议与客户端；`server` 是沙盒服务端，`browser` 是编进程序的浏览器服务脚本。
- `web`：Go 包 `web` 在带 `webdist` 构建标签时嵌入 `web/dist`（不带时嵌入占位页，`go build` / `go test` 不依赖前端构建）。`web/src`：`pages/*Page.tsx` 页面编排，`features/<领域>/` 领域实现，`components/` 公共组件（`ui/` 为基础控件），`api/` 后端客户端，`lib/` 跨领域工具，`styles/design-tokens.css` 设计 Token，`index.css` 全局层（弹层层级、动效）。
- 部署：仓库根 `Dockerfile` 构建 `liusha` 镜像；`compose.yaml`；`deployments/sandbox`（沙盒镜像）、`deployments/single`（单镜像）、`deployments/nginx`（compose 用的反向代理配置）。说明见 `deployments/README.md`。`scripts/release` 是发布打包与第三方声明生成。
- `vendor/`：Go 依赖入库，镜像以 `-mod=vendor` 构建。改依赖必须用 `make tidy`（tidy 后同步 vendor）。

## 常用命令

```bash
cp .env.example .env               # 仓库根 .env：compose 与开发脚本共用
make dev-up                        # 起 pg + redis + 沙盒容器并迁移（首次构建沙盒镜像较久）
./scripts/dev/run-svc.sh           # 宿主上起 proxy + api + runner（日志 logs/*.log）
make run-web                       # 前端 dev server（:5173，/api 代理到 :8580）
make migrate                       # 对本地库执行迁移
make e2e-xss / make e2e-full       # 端到端剧本（需 dev 栈已起；不清库）
make build                         # 本机构建带前端的 liusha（webdist 标签）
make up / make single-build        # compose 全量部署 / 构建单镜像

go test -race -short -run TestName ./internal/<包>/                  # 单个单测
go test -race -tags=integration -run TestName ./internal/<包>/        # 单个集成测试（需 Docker）
cd web && pnpm test src/features/<领域>                                # 单个前端目录（经 pnpm test 带上所需的 NODE_OPTIONS）
```

## 验证门

提交前必须全过，与 CI（`.github/workflows/ci.yml`）同口径：

```bash
go build ./... && go vet ./... && go vet -tags=integration ./...
gofmt -l $(find . -name '*.go' -not -path './vendor/*')    # 输出必须为空
golangci-lint run ./...                                     # v2 配置，CI 钉 v2.15.0
scripts/lint-terminology.sh                                 # 术语门禁
go test -race -short ./...
go test -race -tags=integration ./...                       # 需 Docker：testcontainers 拉起真实 pg
cd web && pnpm build && pnpm test                           # build = tsc -b（含测试文件）+ vite build
```

- 集成测试依赖起不来时，明确列出跳过了哪些，禁止静默跳过。
- 改了用户可见行为（页面、交互、API 响应）时，还要在 dev 栈上实测一遍（见「测试 · 浏览器实测」），测试全绿不等于功能可用。

## 运行时机制

改到任务调度、控制、恢复、停机相关代码时先读这一节；这些行为跨进程，单看一个文件推不出来。

### 任务生命周期与控制面

- task 状态只有 `active` / `completed` / `aborted`；暂停是 `active` 的子状态（`paused` 标记），不改 status。agent_run 状态为 `pending` / `running` / `completed` / `aborted` / `error`；每个任务至多一个存活 run（部分唯一索引兜底，并发生产者先建者赢）。
- 暂停 / 继续 / 停止只写任务行，统一入口 `POST /tasks/:id/control`。运行中的循环每 5 秒轮询任务行（`cognition/task_watch.go`）：看到 `paused` 或终态就停 agents、结束本 run。继续时若上一 run 已退出则重投新 run（从探索图续跑），未退出则原地继续。
- run 收尾按退出原因分流（`runner/task.go` 的 `classifyRunExit`），这张表决定 run 与 task 各落什么状态：

| 退出原因 | run | task |
|---|---|---|
| 自然收敛 | completed | completed |
| 自然收敛，但收敛之后目标被调整或又有了待执行动作（`task.ErrStaleConvergence`） | completed | 保持 active，由恢复监控续跑 |
| 用户停止（任务行已由停止方写成终态） | aborted | 不写任务行（停止后紧接着续接会重开任务，迟到的收尾不得再终止它） |
| run 超时 | aborted | aborted |
| 暂停 | aborted | 保持 active + paused |
| 进程停机 / asynq 租约丢失（handler ctx 被取消） | aborted | 保持 active，由恢复监控续跑 |
| 开始时沙盒暂不可达（连不上 / 5xx），退避重试 2 分钟仍不可达 | error | 保持 active，由恢复监控续跑（计入重投上限，持续不可达时落 aborted） |
| 执行出错 | error | aborted |

- 一次 run 的归属（LLM 审计、工具调用、agent 流量、坐实漏洞）是投递消息里的 `agent_run_id`，由 runner 注入 `cognition.Deps.AgentRunID`。认知循环不得自行按任务反查 run——同一任务有多个 run，反查会把续跑的产出错记到别的 run。

### 恢复、停机与清扫

- 动作的执行生命周期（`explorationgraph/action_lifecycle.go`）：认领记下 run 归属（`claimed_by`），结束与交还都以「仍由本 run 认领」为前置——新旧 run 重叠时旧 run 迟到的结果落空。中断不是失败：run 被暂停、停机或超时打断时，在途动作交还 open 由下一个 run 重做；新 run 开始时回收本任务仍 running 的动作。动作之间没有执行依赖，先后由增量规划表达。
- 动作停摆由执行者看门狗处理（`executor/watchdog.go`）：工具调用结束与推理步完成都记作进展，在途动作超过「单步 LLM 上限 + 工具超时上限 + 1 分钟」无进展即取消并按失败结束。监察者的否决只做策略判断（重复、方向作废、偏离目标），不按运行时长判断，也不能否决用户指派的动作。
- 恢复监控（`runner/run_recovery.go`，30 秒巡检）两条规则：僵尸 run（running 且 LLM / 工具活动停摆超 6 分钟）→ 标 error 并重投；搁浅任务（active、未暂停、无存活 run）→ 重投。重投上限按 error run 计数（默认 4），超限任务落 aborted，并在对话时间线落带原因的终止节点（与用户停止、心跳回收同一口径）。暂停中的任务不重投。
- runner 停机顺序（`internal/app/runner` 的 `shutdown`）：停流量入口与后台循环 → 停止取新任务 → 中断在途 run（handler ctx 取消，按停机中断收尾）并等收尾落地（至多 `asynq_shutdown_timeout_seconds`）→ healthz。asynq 自身停机既不通知在途 handler、也不等被取消的 handler 返回，所以中断信号走 asynq 之外的 ctx（`internal/app/runner` 的 `taskServer`）；容器部署的停止宽限须长于该宽限期（compose `stop_grace_period`、单镜像 `--stop-timeout`）。沙盒是独立组件，不随 runner 停；`serve` 中沙盒在底层（`app.Serve` 逆序停层），runner 排空后才停沙盒——在途工具若以「沙盒不可用」失败会作为动作结果写进探索图，续跑时 planner 看到的是假失败。
- 沙盒进程模型（`internal/sandbox/server`）：命令文本写入脚本文件交给 `sandbox.shell`（POSIX sh，全平台同一命令语言）执行，不经命令行传递。沙盒启动的每个进程（一条命令、一个浏览器服务）连同子孙是归属任务的进程组（`procGroup`）：命令超时只结束它的组，任务释放结束任务的全部组。实现按平台：Linux 与 macOS 按 `LIUSHA_TASK_ID` 从进程环境识别（/proc、sysctl `kern.procargs2`；沙盒重启后照样认领上一实例的进程），Windows 用嵌套作业对象（不允许脱离——Windows 上的 sh 运行时会让子进程自动脱离允许脱离的作业）。shell 缺省自动查找（`resolveShell`：PATH 中的 sh，Windows 上再找 Git for Windows）。浏览器服务由沙盒经本机接口按需启动，不由命令派生：它要活过单条命令、又随任务释放结束。浏览器身份是任务内的身份名（`default`、`admin`……），agent、工具与流量记录都只见身份名；任务隔离由沙盒按命令所属任务完成（端点放在 `<端点目录>/<任务ID>/<身份名>.json`），不要把任务 ID 拼进身份名。交给 agent 的路径一律经 `sandbox.AgentPath`（正斜杠形式）。
- 沙盒资源按任务释放：一次 run 无论因何结束（收敛、终止、暂停、停机中断、出错），runner 都调用沙盒 `POST /tasks/{id}/release`，结束该任务遗留的进程组与浏览器会话；续跑的 run 从探索图恢复，不依赖上一 run 的进程。任务开始时向沙盒查询安装状态，工具可用 = agent 允许 ∩ 沙盒已安装（`cognition.restrictToInstalled`）；沙盒暂不可达先退避等待、等满仍不可达按出错收尾（见上表），沙盒拒绝请求（4xx，如令牌错误）则任务以错误结束。
- 任务级会话身份的回收见「凭证库」。

### 运行中引导

- 人在任务运行中修正方向、不停任务，与生命周期控制（跑不跑）是两回事：`PATCH /tasks/:id {goal}` 调整目标、`POST /tasks/:id/actions {instruction, priority}` 指派动作、`POST /tasks/:id/actions/:action_id/reject {reason}` 否决动作（`dispatch/steering.go`）。只作用于进行中（含暂停中，暂停保持）的任务，已结束 409（在对话里续接）；动作已结束 409、不存在 404。
- 每个操作一个事务：锁任务行（`task.LockActive` / `SetGoalTx`）→ 写任务行与探索图 → 写时间线回执（`conversation.AppendMessageTx`，metadata kind=steering）→ 提交后才推送回执。引导写入与收敛完成（`CompleteConverged`）都先锁任务行，彼此串行。
- 调整目标：撤回规划者排好、尚未开始的动作（用户指派与执行中的不动）；任务行监视发现目标变化即通知规划者重规划、监察者复核在途动作（`monitor.Agent.RequestReview`）。正在进行的那次规划因目标依据前置不成立而整批作废。通知只是加速，正确性靠图上的依据。
- 对话里被判定为「要执行」的消息以「补充要求：…（含附件清单）」追加到目标末尾（`appendRequirement`，同调整目标语义）：任务进行中（含暂停中）原地追加；已结束则在同一事务里重开任务并追加，提交后起新一轮 run。
- 指派的动作来源 user、缺省优先级 high：执行者按来源装配「按指令执行、不扩大范围」的指导，规划视野里标「用户指派」，监察者不能否决它。

### 定时下发

- 定时模板（`cron_schedule`）由「新建任务」带 cron 创建，管理入口 `GET / PATCH / DELETE /schedules`。api 内的调度器每 30 秒取到点模板，克隆一个 `direct` 来源的 assignment（`schedule_id` 指回模板），每条 brief 展开一个任务。
- 多副本靠 `MarkFired` 的乐观锁防重复触发；调度器本身是单副本设计，api 多副本部署需另加 leader 选举。
- 停用不动 `next_run_at`；重新启用从当前时刻按 cron 重算，停用期间错过的不补跑。删除模板保留已触发的任务（外键 `ON DELETE SET NULL`），它们不再标为定时触发。

## 领域不变式

改到相关代码时必须守住。代码层没有强制的条目会特别注明，靠评审把关。

### 实体关系

- 任务是中心：探索图、漏洞、运行、LLM / 工具调用、工作区目录都按 `task_id` 挂在任务下；批次（assignment）是任务的分组；对话是人与任务交互的通道，一个任务至多一个对话（`conversation_one_per_task` 唯一索引），对话删除后任务仍在，纯聊天对话没有任务。
- 任务名下的数据一律按 `task_id` 取（`/tasks/:id/exploration/*`、`/tasks/:id/usage`），不经对话换出任务再查；对话只承载消息与时间线，它显示的状态、用量、控制都取自任务。列表要带出对话 ID 时联表一次取出，不逐行反查。
- 对话里直接发起的 LLM 调用（问答、意图分类、标题生成）没有任务，记 `llm_invocation.conversation_id`；智能体运行的调用记任务、经任务找对话。
- 不保留没有写入方的关联列：界面会据此提供实际查不到的关系（如曾有的漏洞来源流量列）。
- 跨页跳转只看被点击元素对应的内容（规则只在 `web/src/features/relations/taskJumps.ts` 定义）：任务和对话是同一件工作的两个入口，从对话出发落点按对话限定（`?conversation=`，显示「对话：标题」），从任务及漏洞、流量、LLM 调用、探索图出发按任务限定（`?task=`，显示「任务：目标」）。任务页、漏洞页、探索图页都接受这对参数且服务端直接按它过滤；漏洞跳探索图时再带 `?selected=` 打开坐实它的结果节点（`finding.result_node_id`）；对话页以 `?conversation=` 打开对话。没有对话不放「对话」，没有漏洞不放「漏洞」；按钮放在它真实所属的位置——漏洞属于任务，LLM 调用详情不放「漏洞」，流量详情的「漏洞」放在消费任务名下。

### 下发来源

- 取值只有 `direct`（人工下发，含定时模板克隆）和 `proxy`（代理捕获）。DB CHECK、API、Go、TS、前端路由段、中文文案「人工下发 / 代理捕获」全层同名。
- 事实源：`internal/assignment/model.go`、`web/src/lib/source.ts`。不留兼容别名；非法 `source` 参数返回 400。

### 任务目标

- 目标只有一处事实源：`task.goal`（智能体看到的目标全文：人工下发为 brief 加附件清单，代理捕获为合成的分析说明）与根目标描述由 `task.Store` 在同一事务内写入——任务创建时一同建根目标（`Create`），改写只经 `SetGoal` / `SetGoalTx`（内容不变不写，已结束的任务拒绝）。`target_host` 是目标的派生（第一个 http(s) 目标的主机），随目标一同写入。
- run 不携带输入（`agent_run` 无 input 列，投递消息只有 run / 任务 / 会话 ID）：认知循环从图上读根目标，runner 开跑时从任务行取目标主机——续跑、恢复重投、目标调整后都以最新目标为准。
- 「brief」只指下发入口的输入（`POST /chat`、`POST /tasks`、定时模板、assignment 条目）；目标长度契约是 `task.MaxGoalRunes`。

### 探索图（`internal/explorationgraph`）

节点类型为 objective / action / observation / result。创建职责：

| 角色 | 可做的写操作 |
|---|---|
| planner | 建 action；结果分析的结论一律经 `RevisePlan` 落图——提出新派生目标、宣告达成（`completed`）、新目标吸收旧目标（`supersedes` 边 + 旧目标 `superseded`）、追加延续动作，并在被分析的 result 上记分析水位（`analyzed`） |
| executor | 建 observation；CAS 认领 / 终结 action；写 action 的阻塞原因 |
| evaluator | 只经 `PromotionGate` 建 result；回写假设（observation）的验证状态 |
| monitor | 否决 action（`RejectAction`，否决方 monitor；用户指派的动作不可否决）；重规划请求经 `planner.Agent.RequestReplan` 直达规划者，不写图 |
| 认知循环（cognition） | 新 run 开始时回收本任务上一 run 留下的在途动作（交还 open） |
| 任务目标（`task.Store`） | 建根 objective（随任务创建，`Create` 同事务，每任务一个，唯一索引兜底）、改写目标（`SetGoal`：任务行与根目标描述同事务） |
| 运行中引导（`dispatch` 的 `AdjustGoal` / `AssignAction` / `RejectAction`） | 调整目标时撤回规划者排好、尚未开始的动作（→ aborted）；指派 action（来源 user，挂根目标）；否决 action（否决方 user）。与任务行锁、时间线回执同事务 |
| 任务状态迁移（`task.Store`） | 与任务状态迁移同事务让图跟随（`explorationgraph.SyncWithTask`）：根目标跟随任务状态（active→open、completed、aborted）；任务结束时仍 open 的派生目标落 `exhausted`（完成）或 aborted（终止），未结束的动作落 aborted |

- 动作的两种「没做完」不可混用：`aborted`（已中止）是没做完就被收回、不含对错判断（任务收口、目标调整撤回），规划者可按当前目标重提；`rejected`（已否决）是监察者或用户判定这一步不该做，附原因与否决方（`rejected_by`），规划者不得重提等效动作。执行方以「仍由本 run 持有」（running 且认领者是本 run）为执行前提，被收回即放弃结果（`executor/ownership.go`）。

- Store 不校验调用方角色，这张表是约定，靠评审守住。
- 新增坐实结果只能经过复现门晋升，禁止绕过 `PromotionGate` 直接建 result。
- 复放明细写 `exploration_verification` 审计链，不为复放新建 observation 节点。
- 目标分两类：根目标（来源 user，代表任务本身，只随任务终态收敛；`RootObjective` 取最早创建的那个）与派生目标（来源 planner，`IsDerivedObjective`）。规划层只修订派生目标；根目标的达成由整体探索收敛决定，不由单次分析宣告。派生目标的 `completed` 专指有结果证据的达成。动作统一挂在根目标下（规划、结果分析、用户指派同一口径），派生目标是规划视野里的方向，不拥有动作。
- 节点 metadata 承载 task_id / source_type / priority 等身份字段：任何更新都是浅合并（`GraphStore.UpdateNode`、变更集批注），不得整体替换。
- 规划结论都以「所依据的目标」为前置（`explorationgraph.GoalBasis`：根目标及其版本，版本随目标改写与任务状态迁移递增）：常规规划与结果分析同走 `RevisePlan`（变更集的版本前置），规划期间目标被改写即整批作废（`ErrGoalChanged`）、立刻按新目标重做；收敛同样带着依据，任务落 completed 的事务在锁住任务行后核对依据仍成立（`task.Store.CompleteConverged`）。依据在调用 LLM 之前读取。
- 一次结果分析的结论（新目标、达成、吸收、延续动作）连同分析水位是一个规划决策，经 `RevisePlan` 在一个事务内落图（`GraphStore.ApplyChangeSet`）：要么全落、要么全不落。水位在图上而不在进程内存里，且是一次性批注（并发消费同一结果只有一次修订能提交）：暂停后继续、崩溃重投、新旧 run 短暂重叠都不会重复分析，重做也不会重复写入；未分析的结果与非终态动作、待验证假设一样阻止任务收敛（planner `pendingWork`，常规规划与强制重规划同一口径）。LLM 给出的失效引用只剔除并记日志，不拖垮修订。`superseded` 状态与指向它的 `supersedes` 边是同一事实：只能由修订一同写入，`TransitionState` 不进不出。吸收是范围转交而非放弃，不动任何动作。

### 复现配方与坐实交付

- 配方是域信封 `{domain, recipe, assert}`。核心领域中立：`internal/repro` 只定义信封与领域注册表（`Registry.Normalize` 是归一化的唯一入口，内置无机器通道的 `generic` 领域），recipe / assert 的形状、矫正与机器复现通道归各领域包（Web 是 `internal/webrepro`），在 `cognition` 装配时注册。复现门按信封的 domain 选复现通道；没有通道的领域回显配方、由评估者自主取证（验证档位 `judge_only`）。新增领域实现 `repro.Domain`（与可选的 `evaluator.Replayer`）并在装配处注册，核心不出现领域字面量。配方只在 executor 落图时归一化这一次；评估者只从图上的假设节点读配方（唯一入口是轮询待验证假设），不得私自改写配方（降级、补字段）。
- 复现门是终局：web 域（`webrepro/replayer.go`）断言没有任何机器谓词即为空断言，不可坐实。
- 断言鉴别力的分工：机器侧（写入侧归一化与复现门同一口径）只拒 `body_contains` 短于 `webrepro.MinBodyContainsLen` 的子串；常规状态码、页面常态特征等是否有鉴别力，由 LLM 评估者结合 baseline 差分终判。不要只在一侧加机器规则：写入侧报错会让配方跳过形状矫正，被复现门以形状错误判死。
- 验证失败的分流（`evaluator/agent.go` 的 `recordVerifyFailure`）：LLM 配置错误上报致命、不进单条阶梯；LLM 侧瞬时故障不计入失败上限；目标侧不可达（复现通道包装为 `evaluator.ErrTargetUnreachable`）计入上限、耗尽按 `not_reproducible` 出圈——不能与 LLM 瞬时故障混同，否则目标宕机时验证无限重试、任务永不收敛；任务停止导致的重放取消不计入；确定性配方缺陷（复现通道包装为 `evaluator.ErrInvalidRecipe`，损坏的假设同此）立即出圈。分类只认哨兵错误，不按错误文本匹配。出圈原因决定 planner 能否原方向重提，新增原因须同步 planner 的证伪提示（`planner/prompt_builders.go`）。
- 坐实顺序（`evaluator/verifier.go`）：写审计 → 建 result → 交付 → 标记假设已验证。交付成功前不得标记；任一步失败，假设保持 pending，下轮重试。
- 一个坐实结果一条 finding：`finding.result_node_id` 指向它的结果节点，交付以 `(task_id, result_node_id)` 幂等，重试与重放交付不会重复或改写已有记录。主张的语义去重只在复现门里按主张指纹做（同一主张的再次确认折叠进同一结果节点），台账不另设去重键——曾经的「主机 + CWE + 路径 + 方法 + 参数」生成列因 CWE 与参数无写入方，退化成按接口去重，同一接口上先交付的低危主张吞掉了后坐实的高危主张。

### 凭证库（`internal/credential`）

- Redis 是唯一事实源。禁止任何过期机制：不用 key EXPIRE / HEXPIRE，也不在 value 里自管过期时间。
- 键是规范化后的 `host[:port]`（去 scheme、小写），端口参与隔离：同主机不同端口不共享凭证，显式 `:443` 与无端口也是不同键。读写一律经 `CanonicalHost`。
- `username` 必须是目标应用的真实登录名；同一账号更新既有条目，不另起代号。这一条只靠提示词约束。
- 任务级会话身份（`task:{id}:session`）按事件回收：run 收尾落定任务状态后、任务确已结束时由 runner 回收（收敛依据失效、停止后已续接的不回收）（暂停与停机中断不回收——续跑的 run 复用同一登录态），runner 启动时及每 10 分钟由清扫器兜底，另有人工删除。清扫只删「确认终态或确认不存在」的任务的会话，状态查询失败一律保留。持久身份不自动回收。

### LLM 密钥

- 两条路径，前端优先：`llm_provider.encrypted_api_key`（前端直填，AES-GCM 落库）；为空时读 `api_key_env` 指定的环境变量（部署期路径：`config.yaml` providers 块加 `.env` 同名变量）。事实源：`internal/llmconfig/apikey.go`。
- 缺少 LLM 配置不阻止启动：任务下发会被拒绝，并提示到前端配置。
- 档位解析（`framework/llm/router.go`）：显式档 → 就近降级到相邻档。provider 停用、缺钥都按「未配置」处理并继续降级；全部候选不可用时保留 `ErrNotConfigured`，任务据此干净终结。
- provider 只认协议、不认服务：官方、各家兼容端点、中转网关都按 `openai_compat` / `anthropic` 接入，代码里不出现任何服务名单。服务间的差异由「请求画像」承载——`auth_scheme`（`bearer` / `x-api-key`，与协议正交）与 `output_limit_param`（`max_tokens` / `max_completion_tokens`）——由连接诊断实测后记录；运行期 OpenAI 兼容生成器遇上游明确拒绝某个可选字段时也会自适应改写重发（`framework/llm/openai_compat_adapt.go`）。
- 出站凭据只有一个出口：`ClientPool` 的传输层清掉全部凭据头（含 SDK 从环境变量注入的外来值）后按 `auth_scheme` 只写一个；只跟随同源重定向，跨源重定向不得带出密钥。
- provider 配置只有一处口径：`llmconfig.ProviderParams.Normalize` + `Validate`（HTTP 写入、启动种子、落库共用）；前端 `providerRules.ts` 与之一一对应。provider 的 code 是 UUID，界面一律按显示名称展示：显示名称必填、不区分大小写唯一（DB 约束兜底，冲突 409）。Base URL 按协议归一：去误粘的操作路径，Anthropic 再去末尾 `/v1`。连接诊断（`internal/llmprobe`）经运行期同一套 ClientPool + Generator 发请求，请求地址展示用 `framework/llm.OperationURL`，不手拼路径；可用性只以对话实测为准，模型列表是辅助——Anthropic 兼容端点（地址末段含 anthropic）不提供列表时，从同主机上一级路径的 OpenAI 兼容端点取可选模型；未填模型而拿到列表时，自动选用列表第一个按它完整实测（`auto_model`），可用才回填为默认模型。

### HTTP API 契约（`internal/httpapi`）

- 错误体统一为 `{"error": "<消息>"}`。面向操作者的消息用中文，可附额外字段（如 `task_active`）。
- 路径 UUID 格式非法返回 400（路由组挂 `requireUUIDParam`）；资源不存在返回 404；唯一约束冲突返回 409；对终态任务做控制返回 409。
- handler 透传 error 值时必须经 `respondErr`：它记结构化日志，并把客户端错误收敛为 4xx——`pgx.ErrNoRows` → 404，LLM 未配置 → 400，`override.ErrInvalid`（提交的配置不合法）→ 400，技能标识冲突与对象类型不适用的操作（删除内置技能、恢复自建技能）→ 409。store 层必须用 `%w` 包装 `pgx.ErrNoRows`，否则 404 收敛失效。
- 用户输入在边界校验：消息类请求文本 TrimSpace 后为空且无附件即 400（空白消息会建空会话、白付一次 LLM 调用）；请求体用指针字段区分「缺失」与「零值」（如 `{"enabled": false}`）。
- 新增路由在 `route_groups.go` 注册，依赖经 `Deps` 窄接口注入（nil 即不注册）；handler 测试用内存 fake 覆盖 400 / 404 / 成功路径。

### 出厂默认与用户自定义

按「这条记录是谁让它存在的」分两类：

- **产品定义的**（agent、内置技能、系统设置）：出厂值属于代码——agent 与内置技能编译在 `internal/builtin`，系统设置默认值 = 代码 + `config.yaml` 部署值。数据库只存用户改过的字段（`agent_override` / `skill_override` / `setting_override`），生效值在读取时合成（`internal/override`），三级缓存只缓存用户层。改出厂内容即随版本生效，不需要任何同步命令。
- **用户定义的**（LLM 接入、自建技能 `custom_skill`、定时模板、管理员账号、知识库）：用户数据；`config.yaml` / `.env` 只在首装时引导一次。
- 自定义规则：整值字段记录值与基线（写入时依据的出厂原文）；名单字段以 `{on, off}` 提交、记录增删，生效值 = 出厂 ∪ 新增 − 删除，「全部」(`*`) 中取消某项按全集展开。**保存时与出厂相同的值不算自定义**。各资源的写入一律经资源级锁事务（`overridestore`，事务级 advisory lock，含清空即删行），并发写不丢更新；需要与其它写入原子落定时用 `UpdateTx` 在调用方事务内执行。
- agent 系统提示词 = 章程（可自定义）+ 输出契约（程序解析依赖的格式，随解析代码放在各 agent 的 `contract` 子包，不可自定义）。改输出格式只改契约与解析代码；**不要把程序依赖的约定写进章程**（`internal/builtin` 的测试会拦下章程里的契约章节）。
- 内置技能同 code 时自建技能优先；内置技能下线时，用户自定义过的按 `last_saved` 转为同 code 自建技能（api 启动执行）；删除自建技能同时清理各 agent 对它的引用。
- 新增可自定义字段：在资源的配置结构体（`agent.Config` / `skillstore.Content` / 各组设置结构体）加 json 字段即可，字段表由结构体推导（切片为名单、其余为整值）；删除字段后残留的自定义自动失效，不需要迁移。
- agent 名单（`function_tools` / `cli_tools` / `skills`）的成员判定只经 `registry.Allows`：nil = 拒绝，空 = 空集，含 `*` = 全部（含以后新增）。运行时装配与页面上的「已装配」状态同用这一个判定，不要另写——技能索引与 CLI 工具目录都曾因各写一份漏掉 `*` 而恒为空。

### 事件总线（`internal/bus`）

- 按 task 广播：每个订阅者持有独立 channel，收到全部事件拷贝；channel 满即丢弃，背压不级联。
- `SubscribeTask` 与 `Cancel` 都同步生效：订阅返回后即可收到事件，`Cancel` 返回后不再投递。

## 编码约定

### 通用

- 不留兼容别名和旧命名：概念改名就全层替换（DB、API、代码、前端、文案、提示词），并把旧词加入 `scripts/lint-terminology.sh` 的 `FORBIDDEN` 数组防回流。枚举取值与路由段同名，取业界惯用、无歧义的词，同组取值构成对称的轴（如 `direct` / `proxy`）。
- 注释、日志与面向用户的消息用中文，标识符用英文。注释写「为什么」和边界语义，不复述代码。
- 不要相信注释，改代码前先读实现；发现注释与代码不符，一并修正。
- 错误不得静默吞掉：解析失败等可降级的情况要记日志再降级。

### Go

- gofmt；错误用 `%w` 包装并带上下文。
- 用户输入进 LIKE / ILIKE 模式一律经 `db.EscapeLike` / `db.ContainsPattern`（`%`、`_`、`\` 按字面匹配）。
- 接口在使用方定义、保持窄小（handler 的 `Deps` 字段、`cognition.Deps` 都是这种形态）；依赖经构造函数或 Deps 注入，不在业务代码里自行查询本该由调用方给出的上下文。

### 前端

- 颜色、间距、圆角取 `styles/design-tokens.css` 的 Token，不写裸色值。领域枚举的展示文案与颜色只在一处定义（如来源在 `lib/source.ts`、任务状态在 `lib/taskStatus.ts`）。
- 页面文件命名为 `*Page.tsx`，只做编排；实现放进 `features/<领域>/`，后端调用放 `api/`，错误文案经 `errorText` 取后端中文原因。
- 分页列表的搜索与筛选在服务端执行，与分页、总数、统计同一口径；不要在已分页的单页结果上再过滤（其它页搜不到，条数对不上），筛选项的选项也不从当前页的行推导（由服务端分面接口给，每个维度不受自身筛选影响）。服务端检索的搜索框经 `hooks/useDebouncedSearch` 防抖。
- 选取值会持续增长的实体（任务、对话）用 `components/ui/EntityPicker`（服务端按关键词检索、限量、提示缩小范围）；取值少而固定的枚举用 `Select`。不要让用户手输实体 ID。
- 实体之间的跳转按钮经 `features/relations`（`taskJumps` 规则 + `JumpLinks` / `TaskJumpLinks` 展示），落点的范围用 `scope.ts` 的 `task` / `conversation` 参数并以 `ScopeChip` 或选择框显示、可清除；不要在使用点手拼跳转链接。
- 页面状态进 URL 并遵守同一套历史规则：列表页打开的条目（详情面板 / 抽屉）与探索图打开的节点是 `?selected=`（`hooks/useSelectedParam`），对话页打开的对话是 `?conversation=`，窄屏「列表 / 详情」由它们决定（返回列表 = 清除它，与浏览器后退一致）。用户主动的导航（打开、切换、关闭条目，改筛选、翻页、清除范围、换选任务）新增一条历史；系统自动的定位（进页打开最新对话、定位最新任务、新对话建立后落到它、删除当前对话）、键盘逐行移动与搜索框输入用 `replace`。
- 服务端数据一律走 TanStack Query（`features/<领域>/use*Queries.ts`）：取消过期请求、写操作后失效重取，不手写序号防过期，不把服务端数据复制进全局 store。React 状态更新函数必须是纯函数（StrictMode 会重放，原地修改 prev 会重复追加）。
- 弹层层级只有一个事实源 `index.css`：遮罩写 `className="app-overlay"`，对话框 / 抽屉内容带 `tac-modal` 或 `app-drawer`，锚定浮层带 `app-popover` / `app-menu`；使用点不写定位与 `z-*`（`components/ui/dialogLayering.test.ts` 按源码守住）。菜单、弹层用 Radix 原语，不手写 portal——模态打开时 body 为 `pointer-events:none`，手写浮层会点不到。
- 不可逆操作经 `ConfirmDialog`（`danger`）确认，确认按钮文案用具体动词（「确认删除」）。
- 主题只经 `hooks/useTheme` 读写（全局共享，一处切换全部调用点同步）：用户选过的 > 系统深浅偏好 > 深色，未选过时随系统实时变化。`index.html` 的首屏内联脚本与它同口径，改一处必须同步另一处。登录页不放主题切换（业界惯例：跟随系统，偏好设置在登录后）。
- 可访问性是功能的一部分：表单控件经 `htmlFor` / `useId` 关联标签，纯图标按钮带 `aria-label`，可滚动区域可聚焦，tab 触发器指向真实的 tabpanel，可见内容不放进 `aria-hidden`；动效走 `motion-safe:`，不对文字做透明度脉冲。

## 测试

- 修 bug 先写能复现的失败测试（确认红），再修实现（确认绿）。
- 单元测试必须能在 `-short` 下无外部依赖运行；依赖 PG / Docker 的测试文件加 `//go:build integration`，用 `internal/dbtest` 拉起真实库（自动应用全部迁移）。SQL 语义、约束、外键行为写集成测试，不用 mock 糊过。
- 并发代码一律带 `-race` 跑。偶发失败按真实竞态处理：找到根因修掉，不加 sleep，也不靠重跑放行。
- 前端用 vitest + Testing Library，按角色与可访问名称查询元素（`getByRole('button', { name })`、`getByLabelText`），这同时校验了可访问性。
- 浏览器实测：用 `/tmp` 下的一次性 Playwright 脚本（不入库）驱动 dev 栈，每步同时核对页面与库内数据。全量巡检覆盖 320 / 375 / 390 / 768 / 1024 / 1440 / 1920 视口、深浅两套主题，采集 pageerror / console.error / 4xx·5xx / 横向溢出，并跑 axe-core（WCAG 2 AA）要求 0 违规；弹层逐个验证遮罩、层级、控件可点与 Esc 关闭。跨浏览器至少覆盖 Chrome，条件允许补 Firefox / Safari，跑不了的如实记为阻塞。
- 涉及控制面、恢复、停机、定时、沙盒的改动要实机验证跨进程行为（如重启 runner 看续跑、等真实 cron 到点、释放后沙盒内无该任务残留进程），单测覆盖不到进程边界。

## 数据库迁移

- 文件名 `db/migrations/NNNN_<描述>.up.sql` / `.down.sql`，编号四位递增。
- 已发布的迁移不可修改，改 schema 只能新增迁移。生产形态随 `up` 自动执行，只进不退。
- 取值改名或语义变更：在同一个迁移里改写存量数据并更新 CHECK 约束，不留兼容别名。
- 集成测试会自动应用全部迁移，迁移写错会直接体现为集成测试失败。

## 提交与发布

- 提交信息按 `.gitmessage` 模板：`<type>(<scope>): <中文主题>`，正文先写根因或动机，再列行为变化。类型与 scope 列表见模板。主题写用户能感知的变化，不写实现，也不带「——原因」（原因写正文）：`feat` / `fix` 的主题原样进 CHANGELOG 与 Release 页面，不用代码标识符与内部用语；建议 50 显示列以内，硬上限 72（中文按 2 列计，CI 的 `commits` 作业经 `scripts/lint-commit-subjects.sh` 检查 PR 内提交）。类型只能用模板列出的：release-please 不识别的类型（如 `pipeline`）不会进 CHANGELOG 和发布说明。
- 一个提交一个主题：修复与新功能分开提交，便于回滚与生成发布说明。
- PR 以 merge commit 合并，合并提交只留 GitHub 生成的标题、正文留空（`gh pr merge --merge --body ""`）：GitHub 缺省把 PR 标题写进合并提交正文，PR 标题若是 `fix(...)` 这类格式，release-please 会把它当成又一条提交，发布说明里同一项出现两次。
- 版本号遵循 SemVer，0.x 内测期整体右移一位（SemVer §4 与 Cargo / npm 的 0.x 惯例）：次版本位 = 破坏性变更，补丁位 = 新功能与修复；配置为 `release-please-config.json` 的 `bump-minor-pre-major` + `bump-patch-for-minor-pre-major`。1.0 起自动回到标准语义（主版本 = 破坏性、次版本 = 新功能、补丁 = 修复），这两项随之失效。已发布的标签不改写。git 标签带 `v`（`v0.9.1`，Go 生态要求），镜像标签不带（`0.9.1`、浮动的 `0.9`、`latest`）。
- 破坏性变更 = 部署方或调用方必须采取行动、否则会出错的变更：删除或改名 API 端点与字段、改名配置项 / 环境变量 / Redis 键、需要人工操作或会改写、丢失存量数据的迁移。自动执行且无需任何操作的迁移（加列、加表、改注释）、纯新增的端点或字段不算。破坏性变更在主题的类型后加 `!`，正文末尾用 `BREAKING CHANGE: <什么变了>，<部署方 / 调用方要做什么>` 脚注写明，一条一行、不折行（release-please 把折行拼成空格，中文中间会多出空格），详细步骤写进升级文档；release-please 只收录主题行与该脚注，写在正文其它位置的升级说明不会进 CHANGELOG。只在程序内部使用的格式（如 agent 的输出契约）不是对外接口，改它不算破坏性变更。不要为了「醒目」把非破坏性变更标成 `!`：它会升次版本、让浮动标签失去意义。
- 发布由 release-please 驱动：提交推到 main 后它维护 Release PR（标签 `autorelease: pending`），合并 Release PR（merge commit）即发版。Release PR 会累积后续提交，攒到一个有意义的批次（如一个功能收口、或按周）再合并，紧急修复随时合并；不必推一次发一次。只有 `ci` / `docs` / `chore` 等不进版本的改动时不会产生 Release PR。带 `x-release-please-version` 标记的行（`compose.yaml` 镜像 tag、README 中的版本号）由它改写，不要手改。
- 发版触发 `release`：构建 `ghcr.io/v3teran/liusha` 镜像（amd64 + arm64，标签 X.Y.Z / X.Y / latest），并把 Linux / macOS / Windows 各两个架构的二进制压缩包（`liusha` + LICENSE + NOTICE + THIRD_PARTY_NOTICES）与 `checksums.txt` 上传到 Release（`scripts/release/package.sh`）。沙盒镜像与单镜像不发布，由部署方本地构建；沙盒 Dockerfile 或工具清单变更时 `sandbox` workflow 双架构构建并用沙盒自己的 `/tools` 验证工具齐全，不推送。
- 发版后核对：`liusha` 镜像的版本标签存在、双平台齐全，且未登录也能拉取（ghcr 包须为公开）；Release 附件含六个压缩包与 `checksums.txt`，校验和匹配。
- 有破坏性变更或大功能的版本，发布后在 Release 页面正文顶部补「概要」（3–5 行）与「升级须知」（关键动作 + 升级文档链接），自动生成的清单留在下面；CHANGELOG.md 保持生成内容。

## 全仓体检分波

全仓质量治理按以下三波划分，每波结束都跑一遍验证门：

1. agent 核心（认知循环、复现与验证、会话 LLM 路径、出厂内容）：`internal/{planner,executor,evaluator,monitor,cognition,framework,registry,tools,skill,builtin,corpus,embedding,explorationgraph,repro,webrepro,httpreplay,distill,reasoning,intent,chat,qa}`
2. 平台服务：`internal/` 下其余全部包（持久化、调度、接入、配置、基础设施），以及 `cmd/`。
3. 前端：`web/src`（pages 编排 / features 领域 / api 客户端 / styles Token）。
