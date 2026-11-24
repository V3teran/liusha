# AGENTS.md

面向编码 agent 的项目手册：只记录读代码推不出、或推出代价很高的事实与约束。条目以代码为准写成；发现与代码不符，以代码为准并顺手修正本文件。

先读「验证门」与「领域不变式」：前者是交付标准，后者是评审时最常被打回的地方。

## 项目一览

liusha 是多 agent 协作、基于 LLM 的自动化渗透测试平台（仅限授权的安全研究与学习）。核心层（四个 agent、探索图、agent 框架）是领域中立的探索引擎，渗透测试只经任务的「场景」、装配处注册的扩展与产品表层注入（边界见「领域不变式 · 核心层领域中立」）。一次测试的数据流：

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
| proxy | `proxy`（`internal/app/proxy`，:8588） | MITM 代理：过滤后把流量写入 Redis Stream（runner 摄入、按主机攒批下发 `proxy` 来源任务）；管理根证书，上报运行状态（`internal/proxystatus`，控制台「代理接入」读它） |
| runner | `runner`（`internal/app/runner`，:8590） | Asynq 消费者：跑 planner / executor / evaluator / monitor 四 agent；接收沙盒回灌流量；恢复监控与清扫器 |
| sandbox | `sandbox`（`internal/app/sandbox`，:8591） | 执行工具命令的常驻服务（令牌鉴权）；单镜像 / 多镜像时跑在本地构建的 Kali 镜像里，二进制部署（Linux / macOS / Windows）时工具即本机所装 |
| 存储 | PostgreSQL 17+（pgvector）+ Redis | PG 存业务数据；Redis 承载队列、事件流、凭证库 |

实体层级：assignment（一次下发的批次）→ task（一个目标，绑定一个会话）→ agent_run（一次执行；同一任务可有多个：暂停后继续、停机续跑、恢复重投）。

## 安全边界

- 只对授权目标测试。测试与实测用本地靶机，绑定回环或内网地址，用完即停；不要把真实第三方站点写进测试、剧本或示例。
- 密钥一律走环境变量（`.env`：二进制、单镜像、多镜像、本地开发共用模板 `.env.example`；二进制放在程序旁、经 `-env-file` 显式读入），不进代码、配置文件、日志与提交；CI 跑 gitleaks。LLM 密钥的出站规则见「LLM 密钥」。
- 环境变量命名：liusha 的变量一律以 `LIUSHA_` 开头，这个命名空间有四种用途——配置覆盖（名字即配置键 `LIUSHA_<键>`，覆盖 `config.yaml`）、`LIUSHA_CONFIG`（配置文件位置）、部署变量（`.env` 里给容器与 compose 用、liusha 不读，如 `LIUSHA_POSTGRES_PASSWORD`、`LIUSHA_CONSOLE_HOST_PORT`）、沙盒注入给命令的任务变量（`LIUSHA_TASK_ID` 等）；开发工具的参数用 `LIUSHA_E2E_*`。部署变量与开发参数不得与配置键同名（`internal/config` 的 `envnames_test.go` 守住，新增时登记）。新增密钥也是一个配置键（文件里留空），不另起变量名、不在代码里直接读环境变量。
- 沙盒执行 LLM 编写的任意命令：命令环境去掉全部继承的 `LIUSHA_` 变量（liusha 的配置、密钥、部署变量都在这个前缀下，见 `internal/sandbox/server` 的 `scrubEnv`），再注入任务变量；因此 liusha 的任何变量都不得起不带前缀的名字。沙盒令牌只在沙盒进程内：任务进程只拿到沙盒本机接口（回环地址 + 本机口令，`localAPI`）的地址，浏览器流量经它由沙盒带令牌转发给 runner，不得为任何需求把令牌注入命令环境。沙盒端口与令牌不得对外暴露。
- `scripts/dev/e2e.sh` 每次执行都会 TRUNCATE 运行时表、FLUSHDB 并清日志——在有需要保留数据的库上不要跑；只想跑剧本用 `make e2e-xss` / `make e2e-full`（不清库）。
- 本地实测创建的任务、定时模板、临时 provider 用完清理（停止 / 删除），不留下会持续触发或消耗 LLM 额度的东西。

## 仓库地图

- `cmd/liusha`：唯一程序入口（子命令分发、`-env-file` / `-config`；配置文件不指定时取 `LIUSHA_CONFIG`，再取程序所在目录的 `config.yaml`）；`internal/app/<组件>` 是各组件的装配（`Run(ctx, cfg)`），`internal/app.Serve` 按层编排同进程运行。`cmd/e2e` 是开发用的端到端剧本触发器。
- `internal/<领域>`：按领域分包（`assignment`、`task`、`agentrun`、`cronschedule`、`dispatch`、`explorationgraph`、`finding`、`credential`、`llmconfig`……）。agent 核心在 `planner` / `executor` / `evaluator` / `monitor` / `cognition`，框架层在 `framework`；runner 的任务收尾、恢复监控在 `internal/runner`。
- `db/migrations`：迁移 SQL，经 `migrations.FS` 编进程序，由 `internal/db.Migrator`（golang-migrate 库，`schema_migrations` 表）执行；`0196_baseline` 是压缩后的基线。
- `internal/builtin`：出厂内容，编译进程序——`agents/<code>.md`（四个 agent 的出厂配置 + 章程）、`skills/<分类>/<code>/SKILL.md`（内置技能）、`scenarios/<code>.md`（内置场景）。各 agent 的输出契约在 `internal/{planner,executor,evaluator}/contract`。
- `internal/override`：「出厂默认 + 用户自定义」的字段级引擎（agent、系统设置）；`internal/overridestore` 是自定义表的读写协议。`internal/builtinsync`：技能与场景的出厂种子同步（「没改过的跟随新版」判定与执行）。
- `internal/config/config.yaml`：交给部署方的配置文件模板，列出全部键。程序不内置配置、不为缺键补值：读不到文件、缺键、有未知键都拒绝启动（`internal/config` 按结构体推导键表）；新增配置键时同步改它，`internal/config` 的测试直接加载这份。文件只写字面值（不支持 `${VAR}`），`LIUSHA_<键>` 环境变量覆盖文件中的值；密钥一律走环境变量。只放部署方要决定的事（拓扑、容量、可调策略、密钥），组件之间的内部约定（Stream 名、消费组、键前缀、队列）是代码常量；键名以单位结尾（`_seconds` / `_ms` / `_bytes` / `_days`）。持久数据都在 `data_dir` 下（`workspace/`、`logs/`、`certs/`）。二进制部署放在程序旁边（发布压缩包自带），容器部署由宿主 `~/data/liusha/config.yaml` 挂成 `/data/config.yaml`（镜像设 `LIUSHA_CONFIG`）。
- `internal/tools/manifest/tools.yaml`：沙盒 CLI 工具清单（编进程序）；沙盒镜像按它安装，沙盒按它探测已安装工具。
- `internal/sandbox`：沙盒协议与客户端；`server` 是沙盒服务端，`browser` 是编进程序的浏览器服务脚本。
- `web`：Go 包 `web` 在带 `webdist` 构建标签时嵌入 `web/dist`（不带时嵌入占位页，`go build` / `go test` 不依赖前端构建）。`web/src`：`pages/*Page.tsx` 页面编排，`features/<领域>/` 领域实现，`components/` 公共组件（`ui/` 为基础控件），`api/` 后端客户端，`lib/` 跨领域工具，`styles/design-tokens.css` 设计 Token，`index.css` 全局层（弹层层级、动效）。
- 部署：二进制、单镜像、多镜像三种方式。仓库根 `Dockerfile` 构建 `liusha` 镜像；`compose.yaml`（多镜像）；`deployments/sandbox`（沙盒镜像）、`deployments/single`（单镜像）、`deployments/nginx`（多镜像用的反向代理配置）。说明见 `deployments/README.md`。`scripts/release` 是发布打包与第三方声明生成。
- `vendor/`：Go 依赖入库，镜像以 `-mod=vendor` 构建。改依赖必须用 `make tidy`（tidy 后同步 vendor）。

## 常用命令

```bash
cp .env.example .env               # 仓库根 .env：compose、单镜像与开发脚本共用（scripts/dev/env.sh 按它拼宿主侧连接串）
mkdir -p ~/data/liusha && cp internal/config/config.yaml ~/data/liusha/   # 配置文件：沙盒容器与宿主组件共用
make dev-up                        # 起 pg + redis + 沙盒容器并迁移（首次构建沙盒镜像较久）
./scripts/dev/run-svc.sh           # 宿主上迁移并同步出厂内容，再起 proxy + api + runner（日志 ~/data/liusha/logs/*.log）
make run-web                       # 前端 dev server（:5173，/api 代理到 :8580）
make migrate                       # 对本地库执行迁移
make e2e-xss / make e2e-full       # 端到端剧本（需 dev 栈已起；不清库）
make build                         # 本机构建带前端的 liusha（webdist 标签）
make up / make single-build        # 多镜像全量部署 / 构建单镜像

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

- 一次 run 的归属（模型日志、工具调用、agent 流量、坐实漏洞）是投递消息里的 `agent_run_id`，由 runner 注入 `cognition.Deps.AgentRunID`。认知循环不得自行按任务反查 run——同一任务有多个 run，反查会把续跑的产出错记到别的 run。

### 并发模型

调模型请求的并发分三层，各管一件事，不要把它们的职责搅在一起：

| 层 | 配置 | 缺省 | 管什么 |
|---|---|---|---|
| 任务数 | `runner.concurrency` | 3 | 同时执行多少个任务：进程的容量上限，缺省按一个缺省账户配 |
| 任务宽度 | `runner.action_parallelism` / `runner.verification_parallelism` / `runner.monitor_interval_seconds` | 4 / 3 / 360 | 一个任务想跑多宽：执行者同时跑几个动作、评估者同时验几条假设、监察者多久评估一次 |
| 账户在途上限 | `llm.account_concurrency` | 4 | 同一账户同时在途的请求数——对模型的保护只在这一层 |

- **限流随在途请求数走，不随任务数走**：实测同一账户在途 4 个以内时限流接近零，5 个起陡增，6 个时过半；只跑 1 个任务时，4 路执行 + 3 路验证 + 规划 + 监察同样会超。所以上限设在账户上（按「服务地址 + 密钥」识别，与额度窗口同一口径），不设在任务或 agent 上：同账户的各 provider、各任务、各 agent 共用一份名额，先来先得。
- **名额只在一个请求真实在途期间被占用**（`framework/llm` 的 `inflight.go`：重试层每次发请求前占、结束即归，流式请求到流结束）：退避、等额度窗口、排名额都不占。一次调用里没有嵌套的模型调用，不存在「占着名额等别的名额」。
- **排不到名额**：至多排掉本次调用剩余预算的一半，另一半留给请求本身；没排到就以本地节流交还（`ErrInflightBusy`，分类 `ClassBusy`）——对重试层是「到此为止」，不再按退避表空转；对调用方与限流同类（瞬时故障：交还动作、不计失败）；审计与限流同列（`rate_limited`），没发出去的调用不留提示词。有空位就直接发，不受预算约束。排队值得尽量等：放弃的代价是调用方交还动作、从头重做，重新排队还丢掉先来后到的位置。调用方的预算已经用尽时原样交还它的错误，不报成 `ErrInflightBusy`：预算不足是调用方的事，不因这一刻有没有空位而得到两种错误（规划者的自适应预算对「预算在本轮内用尽」与「排到了预算的一半仍没排到」同样放大下一轮预算）。
- **任务数跟着账户的在途上限配**：实测一个任务平均占 3–4 个在途名额（执行者约 2 路、评估者约 1 路、规划者不足 1 路同时在途），缺省账户跑 3 个任务即占满。按排队规则仿真，同一账户上 2 个任务零放弃；3 个任务时 2.5% 的调用排到预算的一半仍拿不到名额、5% 的已完成步数因动作重做而作废；4 个任务时是 19% 与 25%，10 个任务时是 72% 与 40%——吞吐不变，多做的是重复功。所以缺省 3，配比约「任务数 ≈ 账户在途上限 × 3/4」（上限 8 配 6 个，16 配 12 个），多个账户按在途上限之和估算。
- **名额登记是进程内的**：api 与 runner 各有自己的 Router，各限各的。任务宽度不必迁就账户上限，超出的调用排队，不会造成限流风暴；任务数超出账户能同时服务的数量时，则如上一条所说的做重复功。
- **一次调用（含排队、退避、等额度窗口）始终受调用方给的预算约束**，智能体的调用即单步看门狗 `runner.step_llm_timeout_seconds`：排队至多占其一半，额度窗口内的静默等待至多一分钟。不要在调用内引入超出这个预算的等待。对话侧（api 进程）两种等待都不做（`Router.FailFast`）：它没有外层重认领循环，等下去只会把一句清楚的原因拖成连接超时。
- **规划一整轮有自己的预算**（`cognition` 的 `planningRoundBudget`：单步 LLM 上限的 2 倍——一轮规划常见两次调用，读一遍图面、给出规划，每次调用各自受单步看门狗约束）。它不能比单次调用的上限还短：那样调用慢一点整轮就被掐断、下一轮从头再来，被掐断的生成上游照样计费。预算不足型失败（本轮内用尽、或为名额排队排到了预算的一半）后翻倍、封顶基准 3 倍，任一次成功回落（`planner` 的 `adaptBudget`）。
- **上下文压缩优先取得名额**（`llm.Request.Priority`）：压缩链跑在一步推理内部；账户满载时普通调用要排队，蒸馏排不到名额就作废，作废意味着被裁掉的历史不留纪要。优先的调用排在普通调用之前（不打断在途的请求），只给「稀少、短、失败代价高」的调用用，目前只有压缩的蒸馏。看门狗的停摆窗口直接计入压缩链的超时（`runtime.ChainTimeout`），蒸馏的时限先于链超时到期——`internal/executor` 与 `internal/framework/runtime` 的测试守住这两层关系。
- 任务宽度只表达「想跑多宽」。目标不容忍并发访问（顶掉登录态、对并发断连）时把 `runner.action_parallelism` 降到 1–2；账户的并发额度高（官方高阶额度、自建模型）时调大 `llm.account_concurrency`（0 = 不限），再按需调大任务宽度。缺省值是实测结论，`internal/config` 的测试把它们钉住，改动必须是有意为之。

### 调用审计（`internal/llminvocation`、`internal/toolinvocation`）

- LLM 调用与工具调用的记录是失败现场的第一证据，写入不依赖被审计调用的 ctx：调用超时或被取消时它的 ctx 已经结束，写入若随之作废，丢掉的偏偏是失败现场。LLM 调用记录经 channel 入队（入队不看 ctx，缓冲满丢行并告警）；工具调用记录同步写库，用去掉取消的 ctx，另设时间上限。
- 失败类别取记录携带的分类（`llm.UsageRecord.ErrClass`，框架层 `Classify` 对真实错误的结论），落库一方不拿错误文本重判：哨兵错误只存在于错误链上，文本重判会把本地的在途名额节流判成「其它错误」。

### 恢复、停机与清扫

- 动作的执行生命周期（`explorationgraph/action_lifecycle.go`）：认领记下 run 归属（`claimed_by`），结束与交还都以「仍由本 run 认领」为前置——新旧 run 重叠时旧 run 迟到的结果落空。中断不是失败：run 被暂停、停机或超时打断时，在途动作交还 open 由下一个 run 重做；新 run 开始时回收本任务仍 running 的动作。动作之间没有执行依赖，先后由增量规划表达。
- 动作停摆由执行者看门狗处理（`executor/watchdog.go`）：工具调用结束与推理步完成都记作进展，在途动作超过「压缩链超时 + 单步 LLM 上限 + 工具超时上限 + 30 秒」无进展即取消并按失败结束（窗口按一步推理的最长合法耗时推导，见 `executor.StallTimeout`；压缩链见「并发模型」）。监察者的否决只做策略判断（重复、方向作废、偏离目标），不按运行时长判断，也不能否决用户指派的动作。
- 恢复监控（`runner/run_recovery.go`，30 秒巡检）是任务存活的唯一判定方，三条规则：僵尸 run（running 且存活心跳超过 `runner.run_recovery_timeout_seconds` 没有续约）→ 标 error 并重投；孤儿 pending run（建行超过同一阈值且队列里已没有它，按 run ID 查 asynq，`worker.Client.RunQueued`）→ 标 aborted、不计重投次数；搁浅任务（active、未暂停、无存活 run）→ 重投。重投上限按 error run 计数（默认 4），超限任务落 aborted，并在对话时间线落带原因的终止节点（与用户停止同一口径）。暂停中的任务不重投。
- 存活心跳（`runner/heartbeat.go`）是恢复监控判定 run 是否还在被推进的唯一输入，不是另一个判定方：由持有这轮 run 的进程续约（每 30 秒一拍），并且只在「有新的模型调用、工具调用开始，或有未到期的在途操作」时续约——进程被硬杀、或活着但卡住了都会停，而在等一个有超时的长操作时不会。模型调用的截止时间由调用链登记（须在单步看门狗之内），工具调用的超时由遥测拦截器登记。不要改回按调用记录判定：记录在操作结束时才写，长操作在途时是空白，单路执行一个要跑十分钟的命令会在第 6 分钟被回收重投、从头再跑（实测）。也不要另加判死机制：整套部署停机后重启，它会与恢复监控抢着处置同一批任务（曾有的任务心跳回收器因此删除）。
- runner 停机顺序（`internal/app/runner` 的 `shutdown`）：停流量入口与后台循环 → 停止取新任务 → 中断在途 run（handler ctx 取消，按停机中断收尾）并等收尾落地（至多 `runner.drain_timeout_seconds`）→ HTTP 服务。asynq 自身停机既不通知在途 handler、也不等被取消的 handler 返回，所以中断信号走 asynq 之外的 ctx（`internal/app/runner` 的 `taskServer`）；容器部署的停止宽限须长于该宽限期（compose `stop_grace_period`、单镜像 `--stop-timeout`）。沙盒是独立组件，不随 runner 停；`serve` 中沙盒在底层（`app.Serve` 逆序停层），runner 排空后才停沙盒——在途工具若以「沙盒不可用」失败会作为动作结果写进探索图，续跑时 planner 看到的是假失败。
- 沙盒进程模型（`internal/sandbox/server`）：命令文本写入脚本文件交给 `sandbox.shell`（POSIX sh，全平台同一命令语言）执行，不经命令行传递。沙盒启动的每个进程（一条命令、一个浏览器服务）连同子孙是归属任务的进程组（`procGroup`）：命令超时只结束它的组，任务释放结束任务的全部组。实现按平台：Linux 与 macOS 按 `LIUSHA_TASK_ID` 从进程环境识别（/proc、sysctl `kern.procargs2`；沙盒重启后照样认领上一实例的进程），Windows 用嵌套作业对象（不允许脱离——Windows 上的 sh 运行时会让子进程自动脱离允许脱离的作业）。shell 缺省自动查找（`resolveShell`：PATH 中的 sh，Windows 上再找 Git for Windows）。浏览器服务由沙盒经本机接口按需启动，不由命令派生：它要活过单条命令、又随任务释放结束。浏览器身份是任务内的身份名（`default`、`admin`……），agent、工具与流量记录都只见身份名；任务隔离由沙盒按命令所属任务完成（端点放在 `<端点目录>/<任务ID>/<身份名>.json`），不要把任务 ID 拼进身份名。交给 agent 的路径一律经 `sandbox.AgentPath`（正斜杠形式）。
- 沙盒资源按任务释放：一次 run 无论因何结束（收敛、终止、暂停、停机中断、出错），runner 都调用沙盒 `POST /tasks/{id}/release`，结束该任务遗留的进程组与浏览器会话；续跑的 run 从探索图恢复，不依赖上一 run 的进程。任务开始时向沙盒查询安装状态，工具可用 = agent 允许 ∩ 沙盒已安装（`cognition.restrictToInstalled`）；沙盒暂不可达先退避等待、等满仍不可达按出错收尾（见上表），沙盒拒绝请求（4xx，如令牌错误）则任务以错误结束。
- 任务级会话身份的回收见「凭证库」。

### 运行中引导

- 人在任务运行中修正方向、不停任务，与生命周期控制（跑不跑）是两回事：`PATCH /tasks/:id {goal}` 调整目标、`POST /tasks/:id/actions {instruction, priority}` 指派动作、`POST /tasks/:id/actions/:action_id/reject {reason}` 否决动作（`dispatch/steering.go`）。只作用于进行中（含暂停中，暂停保持）的任务，已结束 409（在对话里续接）；动作已结束 409、不存在 404。
- 每个操作一个事务：锁任务行（`task.LockActive` / `SetGoalTx`）→ 写任务行与探索图 → 写时间线回执（`conversation.AppendMessageTx`，metadata kind=steering）→ 提交后才推送回执。引导写入与收敛完成（`CompleteConverged`）都先锁任务行，彼此串行。
- 调整目标：撤回规划者排好、尚未开始的动作（用户指派与执行中的不动）；任务行监视发现目标变化即通知规划者重规划、监察者复核在途动作（`monitor.Agent.RequestReview`）；在途的那一轮规划当场取消——它的结论以旧目标为依据，落图时注定被拒，等它跑完只是白等。正在进行的那次规划因目标依据前置不成立而整批作废。通知只是加速，正确性靠图上的依据。
- 对话里被判定为「要执行」的消息以「补充要求：…（含附件清单）」追加到目标末尾（`appendRequirement`，同调整目标语义）：任务进行中（含暂停中）原地追加；已结束则在同一事务里重开任务并追加，提交后起新一轮 run。
- 指派的动作来源 user、缺省优先级 high：执行者按来源装配「按指令执行、不扩大范围」的指导，规划视野里标「用户指派」，监察者不能否决它。

### 定时下发

- 定时模板（`cron_schedule`）由「新建任务」带 cron 创建，管理入口 `GET / PATCH / DELETE /schedules`。api 内的调度器每 30 秒取到点模板，克隆一个 `direct` 来源的 assignment（`schedule_id` 指回模板），每条 brief 展开一个任务。
- 多副本靠 `MarkFired` 的乐观锁防重复触发；调度器本身是单副本设计，api 多副本部署需另加 leader 选举。
- 停用不动 `next_run_at`；重新启用从当前时刻按 cron 重算，停用期间错过的不补跑。删除模板保留已触发的任务（外键 `ON DELETE SET NULL`），它们不再标为定时触发。

## 领域不变式

改到相关代码时必须守住。代码层没有强制的条目会特别注明，靠评审把关。

### 核心层领域中立

liusha 的产品定位是渗透测试，但核心层是一台通用的「目标驱动探索」引擎：围绕目标规划动作、执行取得观察、复现坐实结果、全程监察纠偏。渗透测试只是这次部署注入的领域；核心层保持中立，换一个领域时只换注入的部分、不动核心。

| 层 | 范围 | 领域属性 |
|---|---|---|
| 核心层 | 四个 agent（`planner` / `executor` / `evaluator` / `monitor`）、探索图（`explorationgraph`）、agent 框架（`framework`、`cognition`、`reasoning`）、知识链路（`distill`、`skill`、`corpus`、中立工具 `run_command` / `read_skill` / `search_corpus`）、复现信封（`repro`） | 中立 |
| 注入点 | 场景（按任务选择、整份拼在各 agent 系统提示词末尾的情境说明，见「场景」）；在 `cognition` 装配处注册的扩展——复现形状（`repro.Kind` 的实现，如 `httprepro`）、领域专用工具（`http_request`、`drive_browser`、凭证、流量）、交付管道（`finding.ClaimSink`） | 承载领域 |
| 产品表层 | 闲聊、问答、意图分类、标题生成、漏洞台账、前端文案、工具清单 | 渗透测试定位；不中立化，也不为「将来换领域」做成可配置 |

- **判据**：一段进入 LLM 的文本、一个概念或一个枚举能否放进核心层，看它在另一个领域（代码审计、故障排查、资料调研……）的部署里是否依然成立。成立才进核心层，否则放进场景或扩展。核心层只用探索图的词汇表达：目标、动作、观察（假设）、结果、主张、配方、证据。
- **落位**：
  - 章程整份写角色、方法、判断准则，用中立词汇；阶段划分、示例、判据、类别词表、领域工具的用法只写在场景里。换领域部署只换 / 改场景（「场景」页），不动章程。
  - 输出契约不可自定义，因此必须中立，示例也用中立对象。
  - 核心包代码里进入 LLM 的文本（提示词组装、工具描述与 schema、回给 LLM 的报错、压缩与蒸馏话术）只陈述数据与机制，不含领域词，也不含行为指导（见「出厂默认与用户自定义」的提示词分层）。
  - 领域能力只经扩展接入、在 `cognition` 装配处注册；核心包不 import 具体实现，也不按领域分支。
- **概念不借领域的名**：核心概念按自身语义命名，不与业务领域共用一个词。反例：复现配方的形状曾叫 `domain`（`web` / `generic`），与「业务领域」撞名，把「一个 HTTP 请求的形状」误当成「Web 领域」；现在叫 `kind`（`http` / `steps`）。
- **方法论也是领域内容**：固定的阶段词表、分类体系即使写成中性的英文枚举，也是领域方法论（动作曾有的 `type` = reconnaissance / analysis / testing 因此删除）。核心层的结构只表达探索本身（目标、依据、因果、状态）。
- **守门**：`internal/builtin/neutrality_test.go` 用领域词表扫描整份章程、三份契约与核心包的字符串字面量（日志调用除外）。新增会产出 LLM 文本的核心包或源码时登记进 `coreSources`；词表只收不会出现在中立表述里的词。守门只能拦词，拦不住改头换面的方法论与命名，这两类靠评审按上面的判据把关。

### 场景（`internal/scenariostore`、`internal/scenario`）

- 场景 = 任务所处情境的说明（名称 + 说明 + 正文），领域内容（可交付主张的标准、类别词表、验证模式、领域工具要点、示例）都在这里。出厂 `pentest`（渗透测试）与 `ctf`（CTF）。
- 归属任务：下发入口（`POST /tasks` 含定时、`POST /chat`、对话跟进中纯聊天升级为任务）的请求字段 `scenario`（`*string`，区分不带与显式空串）——不带 → 缺省场景 `pentest`，它被停用或删除时即无场景（`configstore.DefaultScenario`，用户的操作决定，不另找兜底）；`""` → 无场景；点名的场景未知或已停用 → 400（`scenariostore.ErrUnavailable`），不悄悄换场景。代理捕获的任务同样取缺省场景；读缺省场景失败（库故障）时放弃本次下发、流量保持未消费。定时模板不指定场景时记下创建时的缺省场景。缺省在入口层落定，不靠数据库默认值。
- 建任务时快照到任务行（`task.scenario` / `scenario_name` / `scenario_body`，同空同有），建后不改；每个 run（续跑、恢复重投）都读快照，runner 按快照渲染（`scenario.Render`），注入路径不查场景表。场景之后被改、停用、删除都不影响已建任务；任务摘要带 `scenario_deleted` 标注。
- 注入：`<scenario name="…">正文</scenario>` 经 `cognition.Deps.Scenario`（与章程并列的独立输入）拼在五处系统提示词末尾（执行者、评估者、规划者常规与结果分析、监察者）；无场景时什么都不加。渲染只陈述数据，不写指导语。
- 定时模板引用活场景（`cron_schedule.scenario`，到点按当时内容快照），不变式：**模板只引用存在且启用的场景**。场景的修改（含停用）、删除、出厂同步与模板的新建都先锁场景行（`SELECT … FOR UPDATE`）再检查，彼此串行；被模板（含停用中的）引用时删除与停用都 409 并列出模板。
- 删配置不停任务：停任务只经控制入口，删 / 停用场景对已建任务零影响。

### 实体关系

- 任务是中心：探索图、漏洞、运行、LLM / 工具调用、工作区目录都按 `task_id` 挂在任务下；批次（assignment）是任务的分组；对话是人与任务交互的通道，一个任务至多一个对话（`conversation_one_per_task` 唯一索引），对话删除后任务仍在，纯聊天对话没有任务。
- 任务名下的数据一律按 `task_id` 取（`/tasks/:id/exploration/*`、`/tasks/:id/usage`、`/tasks/:id/run`），不经对话换出任务再查；对话只承载消息与时间线，它显示的状态、用量、控制都取自任务。列表要带出对话 ID 时联表一次取出，不逐行反查。
- 对话里直接发起的 LLM 调用（问答、意图分类、标题生成）没有任务，记 `llm_invocation.conversation_id`；智能体运行的调用记任务、经任务找对话。
- 不保留没有写入方的关联列：界面会据此提供实际查不到的关系（如曾有的漏洞来源流量列）。
- 跨页跳转只看被点击元素对应的内容（规则只在 `web/src/features/relations/taskJumps.ts` 定义）：任务和对话是同一件工作的两个入口，从对话出发落点按对话限定（`?conversation=`，显示「对话：标题」），从任务及漏洞、流量、LLM 调用、探索图出发按任务限定（`?task=`，显示「任务：目标」）。任务页、漏洞页、探索图页都接受这对参数且服务端直接按它过滤；漏洞跳探索图时再带 `?selected=` 打开坐实它的结果节点（`finding.result_node_id`）；对话页以 `?conversation=` 打开对话。没有对话不放「对话」，没有漏洞不放「漏洞」；按钮放在它真实所属的位置——漏洞属于任务，LLM 调用详情不放「漏洞」，流量详情的「漏洞」放在消费任务名下。

### 下发来源

- 取值只有 `direct`（人工下发，含定时模板克隆）和 `proxy`（代理捕获）。DB CHECK、API、Go、TS、前端路由段、中文文案「人工下发 / 代理捕获」全层同名。
- 事实源：`internal/assignment/model.go`、`web/src/lib/source.ts`。不留兼容别名；非法 `source` 参数返回 400。

### 任务目标

- 目标只有一处事实源：`task.goal`（智能体看到的目标全文：人工下发为 brief 加附件清单，代理捕获为合成的分析说明）与根目标描述由 `task.Store` 在同一事务内写入——任务创建时一同建根目标（`Create`），改写只经 `SetGoal` / `SetGoalTx`（内容不变不写，已结束的任务拒绝）。`target_host` 是目标的派生（第一个目标地址的主机：URL 优先，其次裸 IPv4（端口可选）与含点带端口的主机名），随目标一同写入。
- run 不携带输入（`agent_run` 无 input 列，投递消息只有 run / 任务 / 会话 ID）：认知循环从图上读根目标，runner 开跑时从任务行取目标主机——续跑、恢复重投、目标调整后都以最新目标为准。
- 「brief」只指下发入口的输入（`POST /chat`、`POST /tasks`、定时模板、assignment 条目）；目标长度契约是 `task.MaxGoalRunes`。

### 探索图（`internal/explorationgraph`）

节点类型为 objective / action / observation / result。创建职责：

| 角色 | 可做的写操作 |
|---|---|
| planner | 建 action；停止探索时收回自己排出、还没做完的动作（`StopExploration`：open / running → aborted，以规划依据为前置，用户指派的不动）；结果分析的结论一律经 `RevisePlan` 落图——提出新派生目标、宣告达成（`completed`）、新目标吸收旧目标（`supersedes` 边 + 旧目标 `superseded`），并在被分析的 result 上记分析水位（`analyzed`）；结果分析不排动作 |
| executor | 建 observation；CAS 认领 / 终结 action；写 action 的阻塞原因 |
| evaluator | 只经 `PromotionGate` 建 result；回写假设（observation）的验证状态 |
| monitor | 否决 action（`RejectAction`，否决方 monitor；用户指派的动作不可否决）；重规划请求经 `planner.Agent.RequestReplan` 直达规划者，不写图。决策须基于本轮经 `get_global_state` 亲见的态势，指明针对的功能障碍类别并引用本任务的证据节点；重规划请求带上监察者所见的图面（`explorationgraph.Surface`），规划者处理时若目标已改写、出现新的派生目标或新结果即作废（新增动作不算——原地打转时动作照样增长） |
| 认知循环（cognition） | 新 run 开始时回收本任务上一 run 留下的在途动作（交还 open） |
| 任务目标（`task.Store`） | 建根 objective（随任务创建，`Create` 同事务，每任务一个，唯一索引兜底）、改写目标（`SetGoal`：任务行与根目标描述同事务） |
| 运行中引导（`dispatch` 的 `AdjustGoal` / `AssignAction` / `RejectAction`） | 调整目标时撤回规划者排好、尚未开始的动作（→ aborted）；指派 action（来源 user，挂根目标）；否决 action（否决方 user）。与任务行锁、时间线回执同事务 |
| 任务状态迁移（`task.Store`） | 与任务状态迁移同事务让图跟随（`explorationgraph.SyncWithTask`）：根目标跟随任务状态（active→open、completed、aborted）；任务结束时仍 open 的派生目标落 `exhausted`（完成）或 aborted（终止），未结束的动作落 aborted |

- 动作的两种「没做完」不可混用：`aborted`（已中止）是没做完就被收回、不含对错判断（任务收口、目标调整撤回、规划者停止探索），规划者可按当前目标重提；`rejected`（已否决）是监察者或用户判定这一步不该做，附原因与否决方（`rejected_by`），规划者不得重提等效动作。执行方以「仍由本 run 持有」（running 且认领者是本 run）为执行前提，被收回即放弃结果（`executor/ownership.go`）。

- Store 不校验调用方角色，这张表是约定，靠评审守住。
- 新增坐实结果只能经过复现门晋升，禁止绕过 `PromotionGate` 直接建 result。
- 复放明细写 `exploration_verification` 审计链，不为复放新建 observation 节点。
- 目标分两类：根目标（来源 user，代表任务本身，只随任务终态收敛；`RootObjective` 取最早创建的那个）与派生目标（来源 planner，`IsDerivedObjective`）。规划层只修订派生目标；根目标的达成由整体探索收敛决定，不由单次分析宣告。派生目标的 `completed` 专指有结果证据的达成。动作统一挂在根目标下（常规规划与用户指派同一口径），派生目标是规划视野里的方向，不拥有动作。
- 节点 metadata 承载 task_id / source_type / priority 等身份字段：任何更新都是浅合并（`GraphStore.UpdateNode`、变更集批注），不得整体替换。
- 规划结论都以「所依据的目标」为前置（`explorationgraph.GoalBasis`：根目标及其版本，版本随目标改写与任务状态迁移递增）：常规规划与结果分析同走 `RevisePlan`（变更集的版本前置），规划期间目标被改写即整批作废（`ErrGoalChanged`）、立刻按新目标重做；收敛同样带着依据，任务落 completed 的事务在锁住任务行后核对依据仍成立（`task.Store.CompleteConverged`）。依据在调用 LLM 之前读取。
- 常规规划的「停止探索」（契约 `should_continue=false`）是对任务的结论，不是「本轮没有新动作」：规划者收回自己还没做完的动作，余下的用户指派动作、待验证假设与待分析结果落定后收敛。「本轮不再新增动作」的结论（停止探索或等待）在规划者进程内按图面缓存（`planner/agent_conclusion.go`），图面不变不再调用 LLM：停止探索只在目标改写、出现新目标 / 动作 / 结果时失效（收回的探索留下的验证结论不重开规划），等待在图面任何进展时失效；强制重规划不看缓存。
- 动作只由常规规划排出：它的输入是完整的状态摘要（事实、已做与在途的动作、待验证与已证伪的假设），结果分析的输入只有目标视野与新结果，看不到这些——由分析排动作会重复已做过的事，并因图上有了待执行动作而挤掉同一轮的常规规划。分析修订方向后，同一轮的常规规划随即按修订后的方向排动作（`planRound`）。
- 一次结果分析的结论（新目标、达成、吸收）连同分析水位是一个规划决策，经 `RevisePlan` 在一个事务内落图（`GraphStore.ApplyChangeSet`）：要么全落、要么全不落。水位在图上而不在进程内存里，且是一次性批注（并发消费同一结果只有一次修订能提交）：暂停后继续、崩溃重投、新旧 run 短暂重叠都不会重复分析，重做也不会重复写入；未分析的结果与非终态动作、待验证假设一样阻止任务收敛（planner `pendingWork`，常规规划与强制重规划同一口径）。LLM 给出的失效引用只剔除并记日志，不拖垮修订。`superseded` 状态与指向它的 `supersedes` 边是同一事实：只能由修订一同写入，`TransitionState` 不进不出。吸收是范围转交而非放弃，不动任何动作。

### 复现配方与坐实交付

- 配方是信封 `{kind, recipe, assert}`，`kind` 是配方的形状（怎样复现），不是业务领域。`internal/repro` 只定义信封与配方形状注册表（`Registry.Normalize` 是归一化的唯一入口，内置无机器通道的 `steps` 形状：自然语言步骤 + 判据描述），其余形状的 recipe / assert、矫正与机器复现通道归各自的包（一个 HTTP 请求的 `http` 形状在 `internal/httprepro`），在 `cognition` 装配时注册。复现门按信封的 kind 选复现通道；没有通道的形状回显配方、由评估者自主取证（验证档位 `judge_only`）。新增形状实现 `repro.Kind`（与可选的 `evaluator.Replayer`）并在装配处注册，同时在执行者契约里写明它的配方、在评估者契约里写明它的证据（`httprepro` 的测试守住两份契约与实现一致）。配方只在 executor 落图时归一化这一次；评估者只从图上的假设节点读配方（唯一入口是轮询待验证假设），不得私自改写配方（降级、补字段）。
- 复现门是终局：http 配方（`httprepro/replayer.go`）断言没有任何机器谓词即为空断言，不可坐实。
- 断言鉴别力的分工：机器侧（写入侧归一化与复现门同一口径）只拒 `body_contains` 短于 `httprepro.MinBodyContainsLen` 的子串；常规状态码、页面常态特征等是否有鉴别力，由 LLM 评估者结合 baseline 差分终判。不要只在一侧加机器规则：写入侧报错会让配方跳过形状矫正，被复现门以形状错误判死。
- 验证失败的分流（`evaluator/agent.go` 的 `recordVerifyFailure`）：模型配置错误上报致命、不进单条阶梯；LLM 侧瞬时故障不计入失败上限；目标侧不可达（复现通道包装为 `evaluator.ErrTargetUnreachable`）计入上限、耗尽按 `not_reproducible` 出圈——不能与 LLM 瞬时故障混同，否则目标宕机时验证无限重试、任务永不收敛；任务停止导致的重放取消不计入；确定性配方缺陷（复现通道包装为 `evaluator.ErrInvalidRecipe`，损坏的假设同此）立即出圈。分类只认哨兵错误，不按错误文本匹配。出圈原因决定 planner 能否原方向重提，新增原因须同步两处：原因含义（`planner/prompt_builders.go` 的 `refuteReasonHint`）与处置（规划者章程，`internal/builtin` 的测试守住）。
- 坐实顺序（`evaluator/verifier.go`）：写审计 → 建 result → 交付（尽力而为：失败只告警，不回滚验证结论）→ 标记假设已验证。验证结论以审计链与图为准；交付是按结果节点幂等的投影，台账缺口由 runner 的周期补交付（`RedeliverUndeliveredFindings`，启动一次 + 每 10 分钟）收账——交付故障不得按验证失败处理，否则复现成功的主张会被重跑复现、按不可复现错驳（实测：交付反复失败曾令坐实成果整批出圈）。
- 一个坐实结果一条 finding：`finding.result_node_id` 指向它的结果节点，交付按结果节点幂等（`result_node_id` 唯一），重试与重放交付不会重复或改写已有记录。主张的语义去重只在复现门里按主张指纹做（`evaluator.ClaimFingerprint`：类别 + 对象 + 作用点，评估者按判定契约给出；同一主张的再次确认折叠进同一结果节点），台账不另设去重键——曾经的「主机 + CWE + 路径 + 方法 + 参数」生成列因 CWE 与参数无写入方，退化成按接口去重，同一接口上先交付的低危主张吞掉了后坐实的高危主张。
- 发现类型（`finding.class`）是封闭词表（`internal/finding/class.go`：取值、展示名、CWE）之一：评估者坐实时在主张指纹里给出类别（内置场景向它列出同一份词表，`internal/finding` 的测试守住两边一致），晋升门把它单独记在结果节点 metadata 的 `claim_class`，交付时收进词表——词表之外的与未给出的归入 `other`。CWE 按类型固定映射、不由 LLM 给出（LLM 常编造编号）。台账接口带出展示名与 CWE，前端不另存映射；产品按渗透测试定位，词表固定在台账层，用户改写场景新增的类别归入「其他」。

### 记录表留存（`internal/runner` 的 `retention.go`）

- 记录表留存只覆盖「运行过程记录」（诊断/流水）：`llm_invocation`（`llm.invocation_retention_days`）、`tool_invocation`（`tools.invocation_retention_days`）、两张流量表（`traffic.retention_days` 一键管两表，关联行 `traffic_task` 随捕获行删）。任务证据与用户数据（消息、探索图、漏洞、知识）与审计链 `audit_log` 不按时间清理；`0 = 永久`。
- runner 每日一次统一留存循环（`StartRetentionSweep`，runner 单实例天然单例）：每表批删循环到删完（单批 5000，防首次开启时大 DELETE 长事务持锁）；`llm_invocation` 删除前先 Flush（唯一有内存写缓冲的表）。任务工作区按 `sandbox.workspace_retention_days` 由沙盒按目录 mtime 清理，不在此循环（机制不同）。

### 凭证库（`internal/credential`）

- Redis 是唯一事实源。禁止任何过期机制：不用 key EXPIRE / HEXPIRE，也不在 value 里自管过期时间。
- 键是规范化后的 `host[:port]`（去 scheme、小写），端口参与隔离：同主机不同端口不共享凭证，显式 `:443` 与无端口也是不同键。读写一律经 `CanonicalHost`。
- `username` 必须是目标应用的真实登录名；同一账号更新既有条目，不另起代号。这一条只靠提示词约束。
- 任务级会话身份（`task:{id}:session`）按事件回收：run 收尾落定任务状态后、任务确已结束时由 runner 回收（收敛依据失效、停止后已续接的不回收）（暂停与停机中断不回收——续跑的 run 复用同一登录态），runner 启动时及每 10 分钟由清扫器兜底，另有人工删除。清扫只删「确认终态或确认不存在」的任务的会话，状态查询失败一律保留。持久身份不自动回收。

### 知识库（`internal/corpus`、`internal/distill`）

- 跨任务的长期知识，两个写入方：任务自然收敛后的经验沉淀（`distill`），人工在前端「知识库」页录入或导入 Markdown。没有命令行导入；读取方只有规划者的 `search_corpus` 与页面的「检索测试」，两者走同一个 `corpus.Service.Search`；收尾整合找近似条目走不设门槛的 `Neighbors`（给读者要精确，找重复要召回）。
- 检索方式由检索模型配置决定（前端「模型配置」页「检索模型」分区，单行表 `retrieval_model`：服务地址、向量与重排模型名、一把密钥；与 provider 密钥同一套加密；事实源 `internal/llmconfig/retrieval.go`）：配了密钥是语义检索（向量近邻与词元两路召回 → 重排 → 相关度门槛），否则是字面检索（词元召回 → IDF 加权覆盖率门槛）。两种都宁可返回空，不拿不相关的条目凑数；门槛用库内条目与出厂模型标定，依据写在 `search.go`。配置字段空 = 出厂默认（Jina）；向量维度绑 `vector(1024)` 列——保存不拦，实测与运行期校验并给出明确报错；**更换向量模型即作废全部旧向量并按新模型重嵌**（不同模型的向量不可混在一个空间），换门槛需复核召回质量。密钥与配置热改即时生效：corpus.Service 的模型来源每次操作现取（`llmstore.RetrievalModels`，缓存写后广播失效），保存后 api 立即收口（必要时作废向量、写锁内补齐），runner 每 10 分钟的巡检兜底。语义检索运行期出故障退回字面检索，并在结果里注明（`Result.Fallback`），不悄悄换口径；未配置与不可用都不阻塞对话与任务。语义调用失败还会上报降级记录（`retrievalstatus`，Redis 键带 TTL，api 与 runner 谁失败谁写、跨进程可见）：知识库页的检索方式标识显示「语义检索已降级」并带原因与时间；恢复后第一次成功调用清除记录，无人调用时记录到时淡出。清除密钥只清密钥，保留地址与模型名。
- 词元只有一处实现：数据库函数 `corpus_terms`（英文按词、中文按相邻两字），写入时生成 `terms` 生成列，查询用同一个函数切分查询文本。不要在 Go 里另写一份分词。
- 每条知识归属一个场景，空 = 通用：任务只看得到本场景与通用的知识，无场景的任务只看得到通用知识。沉淀时由蒸馏判定每条经验是否依赖场景的规定（交付标准、类别词表等），依赖的归本场景。
- `source` 是当前内容的作者：`agent` 任务沉淀，`expert` 人工。人工改了标题或正文即成为 `expert`（只改场景不变）；收尾整合只能印证人工内容，不能改写。
- 经验的门槛：经验只能来自付出过代价的地方——材料按编号列出任务过程，弯路（证伪的主张、失败或被否决的动作、做完却没有任何产出的动作）带标注；被中止的动作不算弯路（收口或目标调整时被收回，不含对错判断）。没有弯路的任务不调用 LLM；每条经验必须引用至少一条弯路材料，引用不成立的由代码丢弃（`distill.admit`）；每个任务至多 3 条。常识、本目标的解法复述、关于执行系统自身工具与流程的观察都不是经验——后者是待修的产品问题，要去修工具或说明，不要让知识库记住绕法。
- 沉淀分两步：提炼候选，再为每条候选检索近似的已有条目，由 LLM 判定新增、印证、修订，或把彼此重复的已有条目合成一条（合并只限任务沉淀、适用范围相同的条目，被并入的条目的印证转给保留的那条）。整个整合在知识库写锁（PG advisory lock，`Service.Exclusive`）内进行，并发的两次整合不会各写一条重复知识；修订与合并以读到的 `updated_at` 为前置，其间被人改过或是人工内容就改为新增。正文一字不差的写入由唯一约束收敛为印证。
- 「印证」是任务与知识的关系（`corpus_confirmation`）：沉淀出它或整合时判定一致的任务，同一任务重复收敛只计一次。不统计「被检索到几次」——检索次数会让常被检索的条目自我强化。
- 改了文本的条目清掉旧向量（旧文本的向量对新文本是错的），连同写入时向量化失败的条目，由 runner 周期补齐（`StartCorpusIndexLoop`，启动一次 + 每 10 分钟）。
- 经验沉淀是持久化的队列任务（`corpus.distill`，独立的 `distill` 队列，权重低于 agent run，按任务幂等）：任务自然收敛后投递，由 runner 消费；它要调用两次 LLM、耗时分钟级，停机中断或调用失败都留在队列里重试（至多 3 次）。不要改回进程内协程——停机宽限期远短于一次沉淀，进程内的沉淀遇重启即丢。

### LLM 密钥

- 只有一条路径：在前端「模型配置」页填写，`llm_provider.encrypted_api_key` 与检索模型密钥（`retrieval_model` 单行表）都以 AES-GCM 落库（加密密钥是配置 `llm.encryption_key`）。配置文件不涉及模型接入，不从环境变量取模型密钥。事实源：`internal/llmconfig/apikey.go`、`internal/llmconfig/retrieval.go`。
- 缺少模型配置不阻止启动：任务下发会被拒绝，并提示到前端配置。检索模型密钥缺失不拒绝任何东西：知识库自动退字面检索（语义故障退回见「知识库」）——与对话模型「未配置即拒任务」的语义正好相反。
- 档位解析（`framework/llm/router.go`）：显式档 → 就近降级到相邻档。provider 停用、缺钥都按「未配置」处理并继续降级；全部候选不可用时保留 `ErrNotConfigured`，任务据此干净终结。
- 限流的等待以上游告知的恢复时刻为准（`framework/llm` 的 `retry_at.go`、`cooldown.go`）：标准的 `Retry-After` 头优先，其次是错误体里写明的重置时间（不带时区的按北京时间读），拿不到才按退避表 `llm.retry` 盲等。等待在一分钟以内是瞬时限流，按它给的时长等完在本次调用内接着发；超过一分钟是额度窗口（额度按小时、按天重置），登记到 Router 持有的额度窗口登记处——按「服务地址 + 密钥」识别账户，同账户的各 provider、各调用方共用一份。窗口内不发请求：智能体的调用在调用预算内静默等待至多一分钟后把限流原样交还（调用方交还动作后立即重新认领，即刻返回就是空转）；对话侧（api 进程）没有这种循环，`Router.FailFast` 让它立即交还。窗口内每 5 分钟放行一次探测，额度提前到账就当场解除。没发出去的调用在审计里不留提示词；预算在等待中用尽如实交还限流，不记成网络超时。调用的预算约束与在途名额见「运行时机制 · 并发模型」。
- provider 只认协议、不认服务：官方、各家兼容端点、中转网关都按 `openai_compat` / `anthropic` 接入，代码里不出现任何服务名单。服务间的差异由「请求画像」承载——`auth_scheme`（`bearer` / `x-api-key`，与协议正交）与 `output_limit_param`（`max_tokens` / `max_completion_tokens`）——由连接诊断实测后记录；运行期 OpenAI 兼容生成器遇上游明确拒绝某个可选字段时也会自适应改写重发（`framework/llm/openai_compat_adapt.go`）。
- 出站凭据只有一个出口：`ClientPool` 的传输层清掉全部凭据头（含 SDK 从环境变量注入的外来值）后按 `auth_scheme` 只写一个；只跟随同源重定向，跨源重定向不得带出密钥。
- provider 配置只有一处口径：`llmconfig.ProviderParams.Normalize` + `Validate`（HTTP 写入、落库共用）；前端 `providerRules.ts` 与之一一对应。provider 的 code 是 UUID，界面一律按显示名称展示：显示名称必填、不区分大小写唯一（DB 约束兜底，冲突 409）。Base URL 按协议归一：去误粘的操作路径，Anthropic 再去末尾 `/v1`。连接诊断（`internal/llmprobe`）经运行期同一套 ClientPool + Generator 发请求，请求地址展示用 `framework/llm.OperationURL`，不手拼路径；可用性只以对话实测为准，模型列表是辅助——Anthropic 兼容端点（地址末段含 anthropic）不提供列表时，从同主机上一级路径的 OpenAI 兼容端点取可选模型；未填模型而拿到列表时，自动选用列表第一个按它完整实测（`auto_model`），可用才回填为默认模型。

### HTTP API 契约（`internal/httpapi`）

- 错误体统一为 `{"error": "<消息>"}`。面向操作者的消息用中文，可附额外字段（如 `task_active`）。
- 路径 UUID 格式非法返回 400（路由组挂 `requireUUIDParam`）；资源不存在返回 404；唯一约束冲突返回 409；对终态任务做控制返回 409。
- handler 透传 error 值时必须经 `respondErr`：它记结构化日志，并把客户端错误收敛为 4xx——`pgx.ErrNoRows` → 404，LLM 未配置 → 400，`override.ErrInvalid`（提交的配置不合法）→ 400，技能 / 场景标识冲突与对象类型不适用的操作（恢复自建的出厂内容、删除或停用被定时模板引用的场景，`scenariostore.IsConflict`）→ 409；点名的场景不可用（`scenariostore.ErrUnavailable`）→ 400。store 层必须用 `%w` 包装 `pgx.ErrNoRows`，否则 404 收敛失效。
- 用户输入在边界校验：消息类请求文本 TrimSpace 后为空且无附件即 400（空白消息会建空会话、白付一次 LLM 调用）；请求体用指针字段区分「缺失」与「零值」（如 `{"enabled": false}`）。
- 新增路由在 `route_groups.go` 注册，依赖经 `Deps` 窄接口注入（nil 即不注册）；handler 测试用内存 fake 覆盖 400 / 404 / 成功路径。

### 出厂默认与用户自定义

按「这条记录是谁让它存在的」与存放方式分三类：

- **产品定义的、字段级用户层**（agent、系统设置）：出厂值属于代码——agent 编译在 `internal/builtin`，系统设置默认值 = 代码 + `config.yaml` 部署值。数据库只存用户改过的字段（`agent_override` / `setting_override`），生效值在读取时合成（`internal/override`），三级缓存只缓存用户层。改出厂内容即随版本生效，不需要任何同步命令。
- **出厂种子 + 用户数据同表**（技能 `skill`、场景 `scenario`）：一张表一行一个资源，内置与自建同表（`is_builtin`）。出厂内容编译在 `internal/builtin`，是种子：`liusha migrate`（`serve` 启动前同样执行，`internal/app/migrate`）迁移后由出厂同步（`internal/builtinsync`）写入——库里没有就插入；`seed_hash` 记最近一次写入的出厂内容的哈希，当前内容（名称 ␟ 说明 ␟ 正文，启停不参与）的哈希与它相等即没改过，跟随新版（保留启停），改过的不动；编辑后恰与当前出厂相同即重记基线（不算自定义）；同步与用户编辑都先锁行，彼此串行；下线的内置行改过的转自建、没改过的删除（场景仍被定时模板引用时转自建）。粒度是整份内容：改过任一字段，此后出厂更新需用户「恢复出厂」取用。迁移 SQL 计算哈希与 `builtinsync.Hash` 同一算式（`internal/builtinsync` 与 0235 迁移测试守住）。缓存键带出厂内容摘要（`builtinsync.Digest`），出厂更新后不读旧版本留在 L2 的副本。内置与自建都可编辑、停用、删除：自建删除即删行；内置删除记删除标记（`deleted_at`），列表、选择框与运行时都看不到，出厂同步不补回、不更新，用户可在「已删除的内置」里恢复成当前出厂内容（`POST /…/:code/restore`）；出厂也下线时删除标记随之清掉。同 code 时只有一行，自建占用了某内置 code 则内置被遮蔽，新建自建拒绝与内置同名。
- **用户定义的**（LLM 接入、定时模板、管理员账号、知识库）：用户数据，在前端维护；`.env` 只在首装时提供初始管理员密码（`admin.initial_password`）。
- 自定义规则（agent、系统设置）：整值字段记录值与基线（写入时依据的出厂原文）；名单字段以 `{on, off}` 提交、记录增删，生效值 = 出厂 ∪ 新增 − 删除，「全部」(`*`) 中取消某项按全集展开。**保存时与出厂相同的值不算自定义**。各资源的写入一律经资源级锁事务（`overridestore`，事务级 advisory lock，含清空即删行），并发写不丢更新；需要与其它写入原子落定时用 `UpdateTx` 在调用方事务内执行。
- agent 系统提示词 = 章程（可自定义）+ 输出契约（程序解析依赖的格式，随解析代码放在各 agent 的 `contract` 子包，不可自定义）。改输出格式只改契约与解析代码；**不要把程序依赖的约定写进章程**（`internal/builtin` 的测试会拦下章程里的契约章节）。规划者有常规规划与结果分析两次调用，共用一份章程、各有一份契约。
- 提示词分层，每类内容只有一处：行为指导（角色、方法、判断准则）只在章程；格式只在契约；代码组装的输入（objective、状态摘要）只陈述数据，不写「应当 / 不要」这类指导；工具的用法只在工具自己的描述与 schema 里，章程不复述参数。代码里藏着的指导用户看不到也改不了，还会与自定义后的章程互相矛盾。
- 领域内容在提示词里只有一处：任务的场景；章程、契约与核心代码组装的文本保持中立（见「核心层领域中立」）。
- 删除技能（内置与自建）同时清理各 agent 对它的引用（名单成员，去掉一项是范围明确的收窄）；场景是定时模板的唯一情境，被引用时拒绝删除（409），不级联。
- 新增可自定义字段：在资源的配置结构体（`agent.Config` / 各组设置结构体）加 json 字段即可，字段表由结构体推导（切片为名单、其余为整值）；删除字段后残留的自定义自动失效，不需要迁移。
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

- 颜色、间距、圆角取 `styles/design-tokens.css` 的 Token，不写裸色值；字号只用其中定义的阶梯（`text-2xs` / `xs` / `sm` / `base` / `lg` / `2xl`），不写 `text-[Npx]`。领域枚举的展示文案与颜色只在一处定义（如来源在 `lib/source.ts`、任务状态在 `lib/taskStatus.ts`）。
- 避免不必要的辅助标签与说明文字，让布局本身发挥解释作用——结构、分组与留白先行，补充文案是最后手段；只约束可见文案，可访问性要求不放松（纯图标按钮照常带 `aria-label`）。页头只有标题（`PageHeader` 不接受副标题）：页面用途靠结构与控件说明，概念说明放在用到它的字段或空态里。
- 每个筛选栏都要能一键清空（`FilterBar` 的 `canReset` / `onReset` 必填，含搜索词）；栏内的搜索框不再自带清空钮，独立使用的 `SearchInput` 有内容时自带——同一处只有一种清空方式。
- 页面文件命名为 `*Page.tsx`，只做编排；实现放进 `features/<领域>/`，后端调用放 `api/`，错误文案经 `errorText` 取后端中文原因。
- 分页列表的搜索与筛选在服务端执行，与分页、总数、统计同一口径；不要在已分页的单页结果上再过滤（其它页搜不到，条数对不上），筛选项的选项也不从当前页的行推导（由服务端分面接口给，每个维度不受自身筛选影响）。服务端检索的搜索框经 `hooks/useDebouncedSearch` 防抖。
- 选取值会持续增长的实体（任务、对话）用 `components/ui/EntityPicker`（服务端按关键词检索、限量、提示缩小范围）；取值少而固定的枚举用 `Select`。不要让用户手输实体 ID。
- 实体之间的跳转按钮经 `features/relations`（`taskJumps` 规则 + `JumpLinks` / `TaskJumpLinks` 展示），落点的范围用 `scope.ts` 的 `task` / `conversation` 参数并以 `ScopeChip` 或选择框显示、可清除；不要在使用点手拼跳转链接。
- 页面状态进 URL 并遵守同一套历史规则：列表页打开的条目（详情面板 / 抽屉）与探索图打开的节点是 `?selected=`（`hooks/useSelectedParam`），对话页打开的对话是 `?conversation=`，窄屏「列表 / 详情」由它们决定（返回列表 = 清除它，与浏览器后退一致）。用户主动的导航（打开、切换、关闭条目，改筛选、翻页、清除范围、换选任务）新增一条历史；系统自动的定位（进页打开最新对话、定位最新任务、新对话建立后落到它、删除当前对话）、键盘逐行移动与搜索框输入用 `replace`。
- 服务端数据一律走 TanStack Query（`features/<领域>/use*Queries.ts`）：取消过期请求、写操作后失效重取，不手写序号防过期，不把服务端数据复制进全局 store。React 状态更新函数必须是纯函数（StrictMode 会重放，原地修改 prev 会重复追加）。
- 弹层层级只有一个事实源 `index.css`：遮罩写 `className="app-overlay"`，对话框 / 抽屉内容带 `tac-modal` 或 `app-drawer`，锚定浮层带 `app-popover` / `app-menu`；使用点不写定位与 `z-*`（`components/ui/dialogLayering.test.ts` 按源码守住）。菜单、弹层用 Radix 原语，不手写 portal——模态打开时 body 为 `pointer-events:none`，手写浮层会点不到。
- 不可逆操作经确认（事件处理里 `await confirmAction(...)`，受控场景用 `ConfirmDialog`，`danger`），确认按钮文案用具体动词（「确认删除」）；不用原生 `window.confirm` / `alert`。
- 反馈分三层，各有一个组件：区域加载 / 加载失败用 `LoadingState` / `ErrorState`（能重试就给 `onRetry`），表单提交这类就地失败用 `InlineError` 贴在触发处，触发点不在原位的操作结果（行内按钮、菜单项、抽屉关闭后的回写）用 `toast`。
- 主题只经 `hooks/useTheme` 读写（全局共享，一处切换全部调用点同步）：用户选过的 > 系统深浅偏好 > 深色，未选过时随系统实时变化。`index.html` 的首屏内联脚本与它同口径，改一处必须同步另一处。登录页不放主题切换（业界惯例：跟随系统，偏好设置在登录后）。
- 可访问性是功能的一部分：以 state 打开的弹层（没有 Radix `Trigger`）把 `hooks/useReturnFocus` 接到 `Dialog.Content` 的 `onCloseAutoFocus`，关闭后焦点回到打开处；表单控件经 `htmlFor` / `useId` 关联标签，纯图标按钮带 `aria-label`，可滚动区域可聚焦，tab 触发器指向真实的 tabpanel，可见内容不放进 `aria-hidden`；动效走 `motion-safe:`，不对文字做透明度脉冲。

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
- 不保留冗余的列、键与索引：库侧生成的第二个标识、与其它列重复或恒等的信息、没有读取方的派生数据删掉，或接到真正要用它的地方；jsonb 元数据同理。记录产出来源的归属列（各记录表的 `agent_run_id`，见「一次 run 的归属」）是审计链，没有界面读取也保留。索引按实际查询形态建：被其它索引前缀覆盖的、谓词用不上的不建；外键引用列建索引（父行删除时的级联检查按它查找）。
- 挂在任务下的数据都以外键指向 task：任务的组成部分（运行、探索图、审计链、发现、流量……）随任务删除，独立于任务存在的对话与 LLM 调用记录置空保留；封闭词表的取值（状态、严重度、类型……）在库里有 CHECK 约束，与代码词表一致（改词表就用迁移同时改约束，集成测试守住两边一致）。

## 提交与发布

- 提交信息按 `.gitmessage` 模板：`<type>(<scope>): <中文主题>`，正文先写根因或动机，再列行为变化。类型与 scope 列表见模板。主题写用户能感知的变化，不写实现，也不带「——原因」（原因写正文）：`feat` / `fix` 的主题原样进 CHANGELOG 与 Release 页面，不用代码标识符与内部用语；建议 50 显示列以内，硬上限 72（中文按 2 列计，CI 的 `commits` 作业经 `scripts/lint-commit-subjects.sh` 检查 PR 内提交）。类型只能用模板列出的：release-please 不识别的类型（如 `pipeline`）不会进 CHANGELOG 和发布说明。
- 一个提交一个主题：修复与新功能分开提交，便于回滚与生成发布说明。
- PR 以 merge commit 合并，合并提交只留 GitHub 生成的标题、正文留空（`gh pr merge --merge --body ""`）：GitHub 缺省把 PR 标题写进合并提交正文，PR 标题若是 `fix(...)` 这类格式，release-please 会把它当成又一条提交，发布说明里同一项出现两次。
- 跨过一次发版仍未合并的 PR，合并前先 rebase 到最新 main：release-please 按提交时间倒序遍历历史、走到上次发版的提交即停，PR 里提交时间早于上次发版的提交排在它后面，会被漏出发布说明（#44 曾因此漏收）。已漏收时在该 PR 正文末尾加 `BEGIN_COMMIT_OVERRIDE` / `END_COMMIT_OVERRIDE` 包住的提交主题，再重跑 release-please 补回。
- 版本号遵循 SemVer，0.x 内测期整体右移一位（SemVer §4 与 Cargo / npm 的 0.x 惯例）：次版本位 = 破坏性变更，补丁位 = 新功能与修复；配置为 `release-please-config.json` 的 `bump-minor-pre-major` + `bump-patch-for-minor-pre-major`。1.0 起自动回到标准语义（主版本 = 破坏性、次版本 = 新功能、补丁 = 修复），这两项随之失效。已发布的标签不改写。git 标签带 `v`（`v0.9.1`，Go 生态要求），镜像标签不带（`0.9.1`、浮动的 `0.9`、`latest`）。
- 破坏性变更 = 部署方或调用方必须采取行动、否则会出错的变更：删除或改名 API 端点与字段、改名配置项 / 环境变量 / Redis 键、需要人工操作或会改写、丢失存量数据的迁移。自动执行且无需任何操作的迁移（加列、加表、改注释）、纯新增的端点或字段不算。破坏性变更在主题的类型后加 `!`，正文末尾用一条 `BREAKING CHANGE: <什么变了>，<部署方 / 调用方要做什么>` 脚注写明，详细步骤写进升级文档。一个提交只写一条脚注：写多条时 release-please 只收录最后一条，多项变更在这一条里用分号分开；脚注不折行（release-please 把折行拼成空格，中文中间会多出空格）；`<data_dir>` 这类占位与成对的 `*` 包在反引号里，否则发布页会把它们当成 HTML 标签吞掉或渲染成斜体。release-please 只收录主题行与该脚注，写在正文其它位置的升级说明不会进 CHANGELOG。只在程序内部使用的格式（如 agent 的输出契约）不是对外接口，改它不算破坏性变更。不要为了「醒目」把非破坏性变更标成 `!`：它会升次版本、让浮动标签失去意义。
- 发布由 release-please 驱动：提交推到 main 后它维护 Release PR（标签 `autorelease: pending`），合并 Release PR（merge commit）即发版。Release PR 会累积后续提交，攒到一个有意义的批次（如一个功能收口、或按周）再合并，紧急修复随时合并；不必推一次发一次。只有 `ci` / `docs` / `chore` 等不进版本的改动时不会产生 Release PR。带 `x-release-please-version` 标记的行（`compose.yaml` 镜像 tag、README 中的版本号）由它改写，不要手改。
- 发版触发 `release`：构建 `ghcr.io/v3teran/liusha` 镜像（amd64 + arm64，标签 X.Y.Z / X.Y / latest），并把 Linux / macOS / Windows 各两个架构的二进制压缩包（`liusha` + `config.yaml` + `.env.example` + LICENSE + THIRD_PARTY_NOTICES）上传到 Release（`scripts/release/package.sh`）。沙盒镜像与单镜像不发布，由部署方本地构建；沙盒 Dockerfile 或工具清单变更时 `sandbox` workflow 双架构构建并用沙盒自己的 `/tools` 验证工具齐全，不推送。
- 发版后核对：`liusha` 镜像的版本标签存在、双平台齐全，且未登录也能拉取（ghcr 包须为公开）；Release 附件含六个压缩包（GitHub 发布页对每个附件展示 sha256）。
- 有破坏性变更或大功能的版本，发布后在 Release 页面正文顶部补「概要」（3–5 行）与「升级须知」（关键动作 + 升级文档链接），自动生成的清单留在下面；CHANGELOG.md 保持生成内容。

## 全仓体检分波

全仓质量治理按以下三波划分，每波结束都跑一遍验证门：

1. agent 核心（认知循环、复现与验证、会话 LLM 路径、出厂内容）：`internal/{planner,executor,evaluator,monitor,cognition,framework,registry,tools,skill,builtin,corpus,embedding,explorationgraph,repro,httprepro,httpreplay,distill,reasoning,intent,chat,qa}`
2. 平台服务：`internal/` 下其余全部包（持久化、调度、接入、配置、基础设施），以及 `cmd/`。
3. 前端：`web/src`（pages 编排 / features 领域 / api 客户端 / styles Token）。
