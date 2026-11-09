# AGENTS.md

面向编码 agent 的项目手册：只记录读代码推不出、或推出代价很高的事实与约束。条目都以代码为准写成；发现与代码不符，以代码为准并顺手修正本文件。

## 项目一览

liusha 是多 agent 协作的自动化 Web 安全测试平台（仅限授权的安全研究与学习）。一次测试的数据流：

```
人工下发 brief ──┐
                 ├→ assignment → Asynq 队列 → runner：四 agent 认知循环（工具在 sandbox 容器里执行）
代理捕获流量 ────┘                              → 探索图 → 复现门坐实 → finding（漏洞台账）
```

| 组件 | 入口 | 职责 |
|---|---|---|
| web | `web/`（React + Vite，生产由 nginx 托管） | 运维控制台；`/api/*` 反代到 api |
| api | `cmd/api`（gin，:8580） | HTTP API、LLM 接入首装引导、鉴权 |
| proxy | `cmd/proxy`（:8588） | MITM 代理：过滤 / 去重 / 按主机攒批 → 下发 `proxy` 来源任务 |
| runner | `cmd/runner`（:8590） | Asynq 消费者；跑 planner / executor / evaluator / monitor 四 agent；接收沙盒回灌流量 |
| sandbox | `cmd/sandbox-server` + `deployments/tool-images/sandbox` | Kali 工具容器；runner 经**宿主 docker** 拉起，warm pool 复用 |
| 存储 | PostgreSQL 17（pgvector）+ Redis | PG 存业务数据；Redis 承载队列、事件流、凭证库 |

## 仓库地图

- `internal/<领域>`：按领域分包（`assignment`、`task`、`explorationgraph`、`finding`、`credential`、`llmconfig`……）。agent 核心在 `planner` / `executor` / `evaluator` / `monitor` / `cognition`，框架层在 `framework`。
- `db/migrations`：golang-migrate 迁移；`0196_baseline` 是压缩后的基线。
- `internal/builtin`：出厂内容，编译进程序——`agents/<code>.md`（四个 agent 的出厂配置 + 章程）、`skills/<分类>/<code>/SKILL.md`（内置技能）。各 agent 的输出契约在 `internal/{planner,executor,evaluator}/contract`。
- `internal/override`：「出厂默认 + 用户自定义」的字段级引擎；`internal/overridestore` 是自定义表的读写协议。
- `config/config.yaml`：部署配置（LLM provider 首装引导、系统设置的部署默认、运行参数）；密钥一律走环境变量。
- `web/src`：`pages/*Page.tsx` 页面编排，`features/<领域>/` 领域实现，`components/` 公共组件（`ui/` 为基础控件），`api/` 后端客户端，`lib/` 跨领域工具，`styles/design-tokens.css` 设计 Token。
- `deployments/`：三种部署形态（本地开发 / 生产全容器 / 单镜像），说明见 `deployments/README.md`。
- `vendor/`：Go 依赖入库，镜像以 `-mod=vendor` 构建。改依赖必须用 `make tidy`（tidy 后同步 vendor）。

## 常用命令

```bash
cp .env.example .env               # 仓库根 .env 是唯一环境文件（compose 与脚本共用）
make dev-up                        # 起 pg + redis 并执行迁移
make build-sandbox                 # 本地构建沙盒镜像（首次 / 工具变更后）
./scripts/dev/run-svc.sh           # host 侧起 proxy + api + runner（日志 logs/*.log）
make run-web                       # 前端 dev server
make migrate                       # 对本地库执行迁移
make e2e-xss / make e2e-full       # 端到端剧本（需 dev 栈已起）
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

集成测试依赖起不来时，明确列出跳过了哪些，禁止静默跳过。

## 领域不变式

改到相关代码时必须守住。代码层没有强制的条目会特别注明，靠评审把关。

### 下发来源

- 取值只有 `direct`（人工下发，含定时模板克隆）和 `proxy`（代理捕获）。DB CHECK、API、Go、TS、前端路由段、中文文案「人工下发 / 代理捕获」全层同名。
- 事实源：`internal/assignment/model.go`、`web/src/lib/source.ts`。不留兼容别名；非法 `source` 参数返回 400。

### 探索图（`internal/explorationgraph`）

节点类型为 objective / action / observation / result。创建职责：

| 角色 | 可做的写操作 |
|---|---|
| planner | 建 objective 与 action；清理 objective 上的 `monitor_request` |
| executor | 建 observation；CAS 认领 / 终结 action；写 action 的阻塞原因 |
| evaluator | 只经 `PromotionGate` 建 result；回写假设（observation）的裁决元数据 |
| monitor | 中止 action；在 objective 上写 `monitor_request` |
| 控制面（runner / cognition 控制） | 建根 objective、用户追加的 objective / action；目标终态流转 |

- Store 不校验调用方角色，这张表是约定，靠评审守住。
- 新增坐实结果只能经过复现门晋升，禁止绕过 `PromotionGate` 直接建 result。
- 复放明细写 `exploration_verification` 审计链，不为复放新建 observation 节点。

### 复现配方与坐实交付

- 配方是域信封 `{domain, recipe, assert}`，`internal/repro.NormalizeReproEnvelope` 是形状契约的唯一权威。executor 落图前、evaluator 的事件入口与轮询入口都经它归一化，各入口不得私自改写配方（降级、补字段）。
- 复现门（`executor/replayer.go`）是终局：web 域断言没有任何机器谓词即为空断言，不可坐实。
- 断言鉴别力的分工：机器侧（写入侧归一化与复现门同一口径）只拒 `body_contains` 短于 `repro.MinBodyContainsLen` 的子串；常规状态码、页面常态特征等是否有鉴别力，由 LLM 裁决官结合 baseline 差分终判。不要只在一侧加机器规则：写入侧报错会让配方跳过形状矫正，被复现门以形状错误判死。
- 坐实顺序（`evaluator/verifier.go`）：写审计 → 建 result → 交付 → 标记假设已验证。交付成功前不得标记；任一步失败，假设保持 unverified，下轮重试。
- finding 以 `(task_id, dedup_key)` 幂等，`dedup_key` 是 DB 生成列（host + cwe + 规范化路径 + method + parameter）。重试与重放交付不会重复或改写已有记录。

### 凭证库（`internal/credential`）

- Redis 是唯一事实源。禁止任何过期机制：不用 key EXPIRE / HEXPIRE，也不在 value 里自管过期时间。
- 键是规范化后的 `host[:port]`（去 scheme、小写），端口参与隔离：同主机不同端口不共享凭证，显式 `:443` 与无端口也是不同键。读写一律经 `CanonicalHost`。
- `username` 必须是目标应用的真实登录名；同一账号更新既有条目，不另起代号。这一条只靠提示词约束。
- 任务级会话身份（`task:{id}:session`）按事件回收：任务终态时由 runner 回收（暂停不回收），runner 启动时及每 10 分钟由清扫器兜底，另有人工删除。清扫只删「确认终态或确认不存在」的任务的会话，状态查询失败一律保留。持久身份不自动回收。

### LLM 密钥

- 两条路径，前端优先：`llm_provider.encrypted_api_key`（前端直填，AES-GCM 落库）；为空时读 `api_key_env` 指定的环境变量（部署期路径：`config.yaml` providers 块加 `.env` 同名变量）。事实源：`internal/llmconfig/apikey.go`。
- 缺少 LLM 配置不阻止启动：任务下发会被拒绝，并提示到前端配置。
- anthropic 协议出站统一用 `x-api-key`；SDK 自动读取的 ambient `ANTHROPIC_AUTH_TOKEN` 必须擦除（`framework/llm/client_pool.go`）。

### HTTP API 错误契约（`internal/httpapi`）

- 错误体统一为 `{"error": "<消息>"}`。面向操作者的消息用中文，可附额外字段（如 `task_active`）。
- 路径 UUID 格式非法返回 400（路由组挂 `requireUUIDParam`）；资源不存在返回 404；唯一约束冲突返回 409。
- handler 透传 error 值时必须经 `respondErr`：它记结构化日志，并把客户端错误收敛为 4xx——`pgx.ErrNoRows` → 404，LLM 未配置 → 400，`override.ErrInvalid`（提交的配置不合法）→ 400，技能标识冲突与对象类型不适用的操作（删除内置技能、恢复自建技能）→ 409。store 层必须用 `%w` 包装 `pgx.ErrNoRows`，否则 404 收敛失效。

### 出厂默认与用户自定义

按「这条记录是谁让它存在的」分两类：

- **产品定义的**（agent、内置技能、系统设置）：出厂值属于代码——agent 与内置技能编译在 `internal/builtin`，系统设置默认值 = 代码 + `config.yaml` 部署值。数据库只存用户改过的字段（`agent_override` / `skill_override` / `setting_override`），生效值在读取时合成（`internal/override`），三级缓存只缓存用户层。改出厂内容即随版本生效，不需要任何同步命令。
- **用户定义的**（LLM 接入、自建技能 `custom_skill`、管理员账号、知识库）：用户数据；`config.yaml` / `.env` 只在首装时引导一次。
- 自定义规则：整值字段记录值与基线（写入时依据的出厂原文）；名单字段以 `{on, off}` 提交、记录增删，生效值 = 出厂 ∪ 新增 − 删除，「全部」(`*`) 中取消某项按全集展开。**保存时与出厂相同的值不算自定义**。各资源的写入一律经锁行事务（`overridestore`），并发写不丢更新。
- agent 系统提示词 = 章程（可自定义）+ 输出契约（程序解析依赖的格式，随解析代码放在各 agent 的 `contract` 子包，不可自定义）。改输出格式只改契约与解析代码；**不要把程序依赖的约定写进章程**（`internal/builtin` 的测试会拦下章程里的契约章节）。
- 内置技能同 code 时自建技能优先；内置技能下线时，用户自定义过的按 `last_saved` 转为同 code 自建技能（api 启动执行）；删除自建技能同时清理各 agent 对它的引用。
- 新增可自定义字段：在资源的配置结构体（`agent.Config` / `skillstore.Content` / 各组设置结构体）加 json 字段即可，字段表由结构体推导（切片为名单、其余为整值）；删除字段后残留的自定义自动失效，不需要迁移。
- agent 名单（`function_tools` / `cli_tools` / `skills`）的成员判定只经 `registry.Allows`：nil = 拒绝，空 = 空集，含 `*` = 全部（含以后新增）。运行时装配与页面上的「已装配」状态同用这一个判定，不要另写——技能索引与 CLI 工具目录都曾因各写一份漏掉 `*` 而恒为空。

### 事件总线（`internal/bus`）

- 按 task 广播：每个订阅者持有独立 channel，收到全部事件拷贝；channel 满即丢弃，背压不级联。
- `SubscribeTask` 与 `Cancel` 都同步生效：订阅返回后即可收到事件，`Cancel` 返回后不再投递。

## 数据库迁移

- 文件名 `db/migrations/NNNN_<描述>.up.sql` / `.down.sql`，编号四位递增。
- 已发布的迁移不可修改，改 schema 只能新增迁移。生产形态随 `up` 自动执行，只进不退。
- 取值改名或语义变更：在同一个迁移里改写存量数据并更新 CHECK 约束，不留兼容别名。
- 集成测试（`internal/dbtest`）会自动应用全部迁移，迁移写错会直接体现为集成测试失败。

## 编码约定

- 不留兼容别名和旧命名：概念改名就全层替换（DB、API、代码、前端、文案、提示词），并把旧词加入 `scripts/lint-terminology.sh` 的 `FORBIDDEN` 数组防回流。枚举取值与路由段同名。
- Go：gofmt；错误用 `%w` 包装并带上下文；注释、日志与面向用户的消息用中文，标识符用英文。
- 前端：颜色、间距、圆角取 `styles/design-tokens.css` 的 Token，不写裸色值。领域枚举的展示文案与颜色只在一处定义（如来源在 `lib/source.ts`）。页面文件命名为 `*Page.tsx`，只做编排，实现放进 `features/<领域>/`。
- 不要相信注释，改代码前先读实现；发现注释与代码不符，一并修正。

## 测试

- 单元测试必须能在 `-short` 下无外部依赖运行；依赖 PG / Docker 的测试文件加 `//go:build integration`。
- 修 bug 先写能复现的失败测试，再修实现。
- 并发代码一律带 `-race` 跑。偶发失败按真实竞态处理：找到根因修掉，不加 sleep，也不靠重跑放行。
- 前端用 vitest。浏览器实测用 `/tmp` 下的一次性 Playwright 脚本；全量巡检覆盖 320 / 375 / 390 / 768 / 1024 / 1440 / 1920 视口、深浅两套主题，并跑 axe-core（WCAG 2 AA）。

## 提交与发布

- 提交信息按 `.gitmessage` 模板：`<type>(<scope>): <中文主题>`，正文先写根因或动机，再列行为变化。类型与 scope 列表见模板。类型只能用模板列出的：release-please 不识别的类型（如 `pipeline`）不会进 CHANGELOG 和发布说明。
- 改 API 契约、需要迁移的变更是破坏性变更：主题的类型后加 `!`，正文末尾用 `BREAKING CHANGE: <升级注意>` 脚注写明升级步骤。release-please 只收录主题行与该脚注，写在正文其它位置的升级说明不会进 CHANGELOG。1.0 之前破坏性变更只升次版本（`bump-minor-pre-major`）。
- 发布由 release-please 驱动：合并 Release PR 即发版。带 `x-release-please-version` 标记的行（`compose.yaml` 镜像 tag、README 克隆 tag）由它改写，不要手改。
- 发版触发 `release-images`：构建 api / proxy / runner / nginx 四个镜像；sandbox 不重建，而是找出与本版本沙盒源码一致的已构建镜像晋升为同版本 tag（找不到即失败，不发过期沙盒）。sandbox 在 main 上沙盒源码变更时由 `build-sandbox` 构建，以提交 sha 打标签。沙盒源码由 Dockerfile 的 COPY 推导（`scripts/ci/sandbox-inputs.sh`），构建判定与发版查找同源，改 Dockerfile 不需要同步任何路径清单。合并发版 PR 不用等沙盒构建完成：发版会等它。

## 全仓体检分波

全仓质量治理按以下三波划分，每波结束都跑一遍验证门：

1. agent 核心（认知循环、复现与裁决、会话 LLM 路径、出厂内容）：`internal/{planner,executor,evaluator,monitor,cognition,framework,registry,tools,skill,builtin,corpus,embedding,explorationgraph,repro,httpreplay,distill,reasoning,intent,chat,qa}`
2. 平台服务：`internal/` 下其余全部包（持久化、调度、接入、配置、基础设施），以及 `cmd/`。
3. 前端：`web/src`（pages 编排 / features 领域 / api 客户端 / styles Token）。
