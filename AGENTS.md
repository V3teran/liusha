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
- 暂停 / 继续 / 停止只写任务行，统一入口 `POST /tasks/:id/control`。运行中的循环每 5 秒轮询任务行（`cognition/control.go`）：看到 `paused` 或终态就停 agents、结束本 run。继续时若上一 run 已退出则重投新 run（从探索图续跑），未退出则原地继续。
- run 收尾按退出原因分流（`runner/task.go` 的 `classifyRunExit`），这张表决定 run 与 task 各落什么状态：

| 退出原因 | run | task |
|---|---|---|
| 自然收敛 | completed | completed |
| 用户停止 / run 超时 | aborted | aborted |
| 暂停 | aborted | 保持 active + paused |
| 进程停机 / asynq 租约丢失（handler ctx 被取消） | aborted | 保持 active，由恢复监控续跑 |
| 开始时沙盒暂不可达（连不上 / 5xx），退避重试 2 分钟仍不可达 | error | 保持 active，由恢复监控续跑（计入重投上限，持续不可达时落 aborted） |
| 执行出错 | error | aborted |

- 一次 run 的归属（LLM 审计、工具调用、agent 流量、坐实漏洞）是投递消息里的 `agent_run_id`，由 runner 注入 `cognition.Deps.AgentRunID`。认知循环不得自行按任务反查 run——同一任务有多个 run，反查会把续跑的产出错记到别的 run。

### 恢复、停机与清扫

- 恢复监控（`runner/run_recovery.go`，30 秒巡检）两条规则：僵尸 run（running 且 LLM / 工具活动停摆超 6 分钟）→ 标 error 并重投；搁浅任务（active、未暂停、无存活 run）→ 重投。重投上限按 error run 计数（默认 4），超限任务落 aborted。暂停中的任务不重投。
- runner 停机顺序（`internal/app/runner` 的 `shutdown`）：停流量入口与后台循环 → 停止取新任务 → 中断在途 run（handler ctx 取消，按停机中断收尾）并等收尾落地（至多 `asynq_shutdown_timeout_seconds`）→ healthz。asynq 自身停机既不通知在途 handler、也不等被取消的 handler 返回，所以中断信号走 asynq 之外的 ctx（`internal/app/runner` 的 `taskServer`）；容器部署的停止宽限须长于该宽限期（compose `stop_grace_period`、单镜像 `--stop-timeout`）。沙盒是独立组件，不随 runner 停；`serve` 中沙盒在底层（`app.Serve` 逆序停层），runner 排空后才停沙盒——在途工具若以「沙盒不可用」失败会作为动作结果写进探索图，续跑时 planner 看到的是假失败。
- 沙盒进程模型（`internal/sandbox/server`）：命令文本写入脚本文件交给 `sandbox.shell`（POSIX sh，全平台同一命令语言）执行，不经命令行传递。沙盒启动的每个进程（一条命令、一个浏览器服务）连同子孙是归属任务的进程组（`procGroup`）：命令超时只结束它的组，任务释放结束任务的全部组。实现按平台：Linux 与 macOS 按 `LIUSHA_TASK_ID` 从进程环境识别（/proc、sysctl `kern.procargs2`；沙盒重启后照样认领上一实例的进程），Windows 用嵌套作业对象（不允许脱离——Windows 上的 sh 运行时会让子进程自动脱离允许脱离的作业）。shell 缺省自动查找（`resolveShell`：PATH 中的 sh，Windows 上再找 Git for Windows）。浏览器服务由沙盒经本机接口按需启动，不由命令派生：它要活过单条命令、又随任务释放结束。交给 agent 的路径一律经 `sandbox.AgentPath`（正斜杠形式）。
- 沙盒资源按任务释放：一次 run 无论因何结束（收敛、终止、暂停、停机中断、出错），runner 都调用沙盒 `POST /tasks/{id}/release`，结束该任务遗留的进程组与浏览器会话；续跑的 run 从探索图恢复，不依赖上一 run 的进程。任务开始时向沙盒查询安装状态，工具可用 = agent 允许 ∩ 沙盒已安装（`cognition.restrictToInstalled`）；沙盒暂不可达先退避等待、等满仍不可达按出错收尾（见上表），沙盒拒绝请求（4xx，如令牌错误）则任务以错误结束。
- 任务级会话身份的回收见「凭证库」。

### 定时下发

- 定时模板（`cron_schedule`）由「新建任务」带 cron 创建，管理入口 `GET / PATCH / DELETE /schedules`。api 内的调度器每 30 秒取到点模板，克隆一个 `direct` 来源的 assignment（`schedule_id` 指回模板），每条 brief 展开一个任务。
- 多副本靠 `MarkFired` 的乐观锁防重复触发；调度器本身是单副本设计，api 多副本部署需另加 leader 选举。
- 停用不动 `next_run_at`；重新启用从当前时刻按 cron 重算，停用期间错过的不补跑。删除模板保留已触发的任务（外键 `ON DELETE SET NULL`），它们不再标为定时触发。

## 领域不变式

改到相关代码时必须守住。代码层没有强制的条目会特别注明，靠评审把关。

### 下发来源

- 取值只有 `direct`（人工下发，含定时模板克隆）和 `proxy`（代理捕获）。DB CHECK、API、Go、TS、前端路由段、中文文案「人工下发 / 代理捕获」全层同名。
- 事实源：`internal/assignment/model.go`、`web/src/lib/source.ts`。不留兼容别名；非法 `source` 参数返回 400。

### 探索图（`internal/explorationgraph`）

节点类型为 objective / action / observation / result。创建职责：

| 角色 | 可做的写操作 |
|---|---|
| planner | 建 action；清理 objective 上的 `monitor_request`；结果分析的结论一律经 `RevisePlan` 落图——提出新派生目标、宣告达成（`completed`）、新目标吸收旧目标（`supersedes` 边 + 旧目标 `superseded`）、追加延续动作，并在被分析的 result 上记分析水位（`analyzed`） |
| executor | 建 observation；CAS 认领 / 终结 action；写 action 的阻塞原因 |
| evaluator | 只经 `PromotionGate` 建 result；回写假设（observation）的裁决元数据 |
| monitor | 中止 action；在 objective 上写 `monitor_request` |
| 控制面（runner / cognition 控制） | 建根 objective、用户追加的 objective / action（挂根目标）；任务终态时收口仍 open 的目标：根目标随任务结局（completed/aborted/failed），未达成的派生目标在正常结束时落 `exhausted`、随终止/失败同为 aborted/failed |

- Store 不校验调用方角色，这张表是约定，靠评审守住。
- 新增坐实结果只能经过复现门晋升，禁止绕过 `PromotionGate` 直接建 result。
- 复放明细写 `exploration_verification` 审计链，不为复放新建 observation 节点。
- 目标分两类：根目标（来源 user，代表任务本身，只随任务终态收敛；`RootObjective` 取最早创建的那个）与派生目标（来源 planner，`IsDerivedObjective`）。规划层只修订派生目标；根目标的达成由整体探索收敛决定，不由单次分析宣告。派生目标的 `completed` 专指有结果证据的达成。动作统一挂在根目标下（规划、结果分析、人工注入三个入口同一口径），派生目标是规划视野里的方向，不拥有动作。
- 节点 metadata 承载 task_id / source_type / priority 等身份字段：任何更新都是浅合并（`GraphStore.UpdateNode`、变更集批注），不得整体替换。
- 一次结果分析的结论（新目标、达成、吸收、延续动作）连同分析水位是一个规划决策，经 `RevisePlan` 在一个事务内落图（`GraphStore.ApplyChangeSet`）：要么全落、要么全不落。水位在图上而不在进程内存里，且是一次性批注（并发消费同一结果只有一次修订能提交）：暂停后继续、崩溃重投、新旧 run 短暂重叠都不会重复分析，重做也不会重复写入；未分析的结果与非终态动作、未裁决观察一样阻止任务收敛（planner `pendingWork`，常规规划与强制重规划同一口径）。LLM 给出的失效引用只剔除并记日志，不拖垮修订。`superseded` 状态与指向它的 `supersedes` 边是同一事实：只能由修订一同写入，`TransitionState` 不进不出。吸收是范围转交而非放弃，不动任何动作。

### 复现配方与坐实交付

- 配方是域信封 `{domain, recipe, assert}`，`internal/repro.NormalizeReproEnvelope` 是形状契约的唯一权威。executor 落图前、evaluator 的事件入口与轮询入口都经它归一化，各入口不得私自改写配方（降级、补字段）。
- 复现门（`executor/replayer.go`）是终局：web 域断言没有任何机器谓词即为空断言，不可坐实。
- 断言鉴别力的分工：机器侧（写入侧归一化与复现门同一口径）只拒 `body_contains` 短于 `repro.MinBodyContainsLen` 的子串；常规状态码、页面常态特征等是否有鉴别力，由 LLM 裁决官结合 baseline 差分终判。不要只在一侧加机器规则：写入侧报错会让配方跳过形状矫正，被复现门以形状错误判死。
- 验证失败的分流（`evaluator/agent.go` 的 `recordVerifyFailure`）：LLM 配置错误上报致命、不进单条阶梯；LLM 侧瞬时故障不计入失败上限；目标侧不可达（重放没拿到响应，`httpreplay.ErrTransport`）计入上限、耗尽按 `not_reproducible` 出圈——不能与 LLM 瞬时故障混同，否则目标宕机时验证无限重试、任务永不收敛；任务停止导致的重放取消不计入；确定性配方缺陷立即出圈。出圈原因决定 planner 能否原方向重提，新增原因须同步 planner 的证伪提示（`planner/prompt_builders.go`）。
- 坐实顺序（`evaluator/verifier.go`）：写审计 → 建 result → 交付 → 标记假设已验证。交付成功前不得标记；任一步失败，假设保持 unverified，下轮重试。
- finding 以 `(task_id, dedup_key)` 幂等，`dedup_key` 是 DB 生成列（host + cwe + 规范化路径 + method + parameter）。重试与重放交付不会重复或改写已有记录。

### 凭证库（`internal/credential`）

- Redis 是唯一事实源。禁止任何过期机制：不用 key EXPIRE / HEXPIRE，也不在 value 里自管过期时间。
- 键是规范化后的 `host[:port]`（去 scheme、小写），端口参与隔离：同主机不同端口不共享凭证，显式 `:443` 与无端口也是不同键。读写一律经 `CanonicalHost`。
- `username` 必须是目标应用的真实登录名；同一账号更新既有条目，不另起代号。这一条只靠提示词约束。
- 任务级会话身份（`task:{id}:session`）按事件回收：任务终态时由 runner 回收（暂停与停机中断不回收——续跑的 run 复用同一登录态），runner 启动时及每 10 分钟由清扫器兜底，另有人工删除。清扫只删「确认终态或确认不存在」的任务的会话，状态查询失败一律保留。持久身份不自动回收。

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
- 接口在使用方定义、保持窄小（handler 的 `Deps` 字段、`cognition.Deps` 都是这种形态）；依赖经构造函数或 Deps 注入，不在业务代码里自行查询本该由调用方给出的上下文。

### 前端

- 颜色、间距、圆角取 `styles/design-tokens.css` 的 Token，不写裸色值。领域枚举的展示文案与颜色只在一处定义（如来源在 `lib/source.ts`、任务状态在 `lib/taskStatus.ts`）。
- 页面文件命名为 `*Page.tsx`，只做编排；实现放进 `features/<领域>/`，后端调用放 `api/`，错误文案经 `errorText` 取后端中文原因。
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

- 提交信息按 `.gitmessage` 模板：`<type>(<scope>): <中文主题>`，正文先写根因或动机，再列行为变化。类型与 scope 列表见模板。类型只能用模板列出的：release-please 不识别的类型（如 `pipeline`）不会进 CHANGELOG 和发布说明。
- 一个提交一个主题：修复与新功能分开提交，便于回滚与生成发布说明。
- 版本号遵循 SemVer，0.x 内测期整体右移一位（SemVer §4 与 Cargo / npm 的 0.x 惯例）：次版本位 = 破坏性变更，补丁位 = 新功能与修复；配置为 `release-please-config.json` 的 `bump-minor-pre-major` + `bump-patch-for-minor-pre-major`。1.0 起自动回到标准语义（主版本 = 破坏性、次版本 = 新功能、补丁 = 修复），这两项随之失效。已发布的标签不改写。git 标签带 `v`（`v0.9.1`，Go 生态要求），镜像标签不带（`0.9.1`、浮动的 `0.9`、`latest`）。
- 破坏性变更 = 部署方或调用方必须采取行动、否则会出错的变更：删除或改名 API 端点与字段、改名配置项 / 环境变量 / Redis 键、需要人工操作或会改写、丢失存量数据的迁移。自动执行且无需任何操作的迁移（加列、加表、改注释）、纯新增的端点或字段不算。破坏性变更在主题的类型后加 `!`，正文末尾用 `BREAKING CHANGE: <升级注意>` 脚注写明升级步骤；release-please 只收录主题行与该脚注，写在正文其它位置的升级说明不会进 CHANGELOG。不要为了「醒目」把非破坏性变更标成 `!`：它会升次版本、让浮动标签失去意义。
- 发布由 release-please 驱动：提交推到 main 后它维护 Release PR（标签 `autorelease: pending`），合并 Release PR（merge commit）即发版。Release PR 会累积后续提交，攒到一个有意义的批次（如一个功能收口、或按周）再合并，紧急修复随时合并；不必推一次发一次。只有 `ci` / `docs` / `chore` 等不进版本的改动时不会产生 Release PR。带 `x-release-please-version` 标记的行（`compose.yaml` 镜像 tag、README 中的版本号）由它改写，不要手改。
- 发版触发 `release`：构建 `ghcr.io/v3teran/liusha` 镜像（amd64 + arm64，标签 X.Y.Z / X.Y / latest），并把两个架构的二进制压缩包（`liusha` + LICENSE + NOTICE + THIRD_PARTY_NOTICES）与 `checksums.txt` 上传到 Release（`scripts/release/package.sh`）。沙盒镜像与单镜像不发布，由部署方本地构建；沙盒 Dockerfile 或工具清单变更时 `sandbox` workflow 双架构构建并用沙盒自己的 `/tools` 验证工具齐全，不推送。
- 发版后核对：`liusha` 镜像的版本标签存在、双平台齐全；Release 附件含两个压缩包与 `checksums.txt`，校验和匹配。

## 全仓体检分波

全仓质量治理按以下三波划分，每波结束都跑一遍验证门：

1. agent 核心（认知循环、复现与裁决、会话 LLM 路径、出厂内容）：`internal/{planner,executor,evaluator,monitor,cognition,framework,registry,tools,skill,builtin,corpus,embedding,explorationgraph,repro,httpreplay,distill,reasoning,intent,chat,qa}`
2. 平台服务：`internal/` 下其余全部包（持久化、调度、接入、配置、基础设施），以及 `cmd/`。
3. 前端：`web/src`（pages 编排 / features 领域 / api 客户端 / styles Token）。
