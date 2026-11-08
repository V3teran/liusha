# 阶段 3/6 · legacy-purge 交付报告

基线：c057c1c3（阶段 2 之后）· 分支 pipeline/revamp · 2026-11-08

## 新事实基准（从源码归纳）

| 概念 | 现行事实（源码依据） | 已退役的旧名 |
|---|---|---|
| 下发来源 | `assignment.Source` ∈ {`self` 自主下发, `proxy` 代理模式}，DB `assignment_source_check` | active / passive、manual / auto、cron（从未是来源值） |
| agent 角色 | planner / executor / evaluator / monitor（`agent_run_role_check`、`agents/*.md`） | traffic-analysis（passive 单 agent）、exploitation（active 子 agent） |
| 长期知识 | `corpus.Entry`（`e8a01d20` lesson→corpus） | insight（0188 退役）、lesson 包 |
| 流量 | proxy_traffic / agent_traffic + `list_traffic` / `view_traffic` / `http_request` | flows、`list_flows` / `view_flow` / `replay_flow`、`owner=active_scan` |
| sandbox 抓流 | 仅浏览器 CDP capture → `cmd/runner` ingest；CLI 直连不抓 | 容器内 mitmproxy + `mitm-capture.py`；ingest 挂 cmd/proxy |
| 下发实现 | `*dispatch.Service`（Chat/FollowUp/Dispatch*/Publish） | `scanAdapter`（`cmd/api/adapters.go`） |
| 运行期旋钮 | DB system_setting（前端「系统配置」热改），yaml 只管启动参数 | toolruntime / compaction / prompt_limits / skills 等 yaml 段 |

旧词汇三来源：git 历史中消失的 70+ 个包名与 9 个页面名、`scripts/lint-terminology.sh` 禁用表、`7798736e` 提交说明（明确宣布「去掉已死的 passive 概念」但改动只覆盖了枚举值与前端）。

## 循环记录

| 轮 | 切入点 | 新发现 | 说明 |
|---|---|---|---|
| 1 | 旧词汇逐词全库检索 | ≈40 | 主体清理，见下表 |
| 2 | 目录树 / 文件名 | 0 | 命中项均正当（`.github/workflows`、`openai_compat` 协议名、`react.go`=ReAct、clipboard） |
| 3 | 兼容代码标记 + 零引用导出（deadcode） | 9 | 8 处兼容定性错误的注释 + 1 处存疑；deadcode 不可达项为文档化的测试替身 |
| 4 | md 全量 + 注释路径存在性核验 | 6 | 悬空路径 3 处（`cmd/api/adapters.go`、`internal/llm`、`internal/replay`）+ 由此追出的 `scanAdapter` 引用 |
| 5 | 扩展词汇复检（adapter / mitm / owner / 版本号墓碑） | 7 | |
| 6 | 变更叙述句中的孤立标识符 | 6 | |
| 7 | 合并全部检测器的收敛复扫 | **0** | 退出：零新发现 + 验证门全绿 |

**单调性说明**：序列 40→0→9→6→7→6→0 在第 3、5 轮回升。已逐条核对：回升轮的发现全部位于此前未触碰的文件、均为预先存在的残留，是换切入点暴露的视角盲区，不是前轮改错或新引入（第 6 轮 `severity.ts` 那句为阶段 2 保留下来的原文，非新写）。

## 清除清单

### 标识符 / 接口（改名，零兼容别名）

| 旧 | 新 | 依据 |
|---|---|---|
| `PassiveBrief` / `passiveBriefMaxList` / `spawnPassiveTask` / `enqueuePassive` / `passiveCronClaimLimit` | `ProxyTaskBrief` / `proxyTaskBriefMaxList` / `spawnProxyTask` / `enqueueProxyTask` / `proxyCronClaimLimit` | 来源已收口 proxy |
| `proxy.RunPassiveSanitizer` | `proxy.RunExternalSanitizer` | 跑在 external listener；包内避免 proxy 口吃 |
| 测试 `newPassiveTask`、`TestFireDue_ActiveSchedule_*` / `_PassiveSchedule_*` | `newProxyTask`、`TestFireDue_BriefItem_*` / `_HostItem_*` | 定时克隆一律 source=proxy，场景差异在条目类型（brief vs host），按真实区别命名 |
| 路由 `/internal/v1/flows/ingest` | `/internal/v1/traffic/ingest` | flows 概念已退役；两端（runner 注册 + launcher 注入 URL）同仓同步 |
| cmd/e2e CLI `active:<name>`、类型 `activeProfile`、Make 目标 `e2e-active` | `<name>`、`profile`、`e2e-xss` | 前缀只为区分已删的 passive；新增 `profiles_test.go`（TDD） |
| 日志组件名 `llmcall.store` | `llminvocation.store` | 旧包名写进每条日志 |
| `cmd/api` 变量 `adapter` | `dispatchService` | 旧 scanAdapter 遗名 |

### 运行时数据 / 面向 LLM 的文本

- 代理模式 task brief「分析 %s 的**被动**流量」与会话首条「**被动**流量分析：…」→「代理流量」（落库 + 界面可见 + 喂 agent）
- `list_traffic` 工具 schema：`agent=主动扫描流量，proxy=被动代理抓包` → `agent=sandbox 内工具自产流量，proxy=代理模式抓包流量`
- `skills/vuln/bac/SKILL.md`（6 处 passive/active 模式、`traffic-analysis`）、`skills/tool/browser-use/SKILL.md`（reconnaissance/exploitation agent 名、「旧版有」）
- ⚠ SKILL.md 为 insert-only 种子：**已初始化的库需 `make reseed` 才生效**

### Schema 注释（新增迁移 0197，仅改注释、可回卷）

`agent_run.role`（列出不存在的角色，与 CHECK 约束矛盾）、`task.brief`（称 passive 时为空——实际 `task.Create` 恒要求非空）、`task.target_host`、`traffic_task`。本地已经 `make migrate` 实跑至 197。

### 死代码 / 失效行为

- `scripts/dev/e2e.sh`：清理 `session-store/` 死代码；用法说明整段失效（列出不存在的 bac/sqli… passive profile 与 `passive:` 语法）重写
- **修复由残留引出的真缺陷**：`e2e.sh` 文档承诺不带参跑全套，但 `cmd/e2e` 空参直接 Fatal——现空参按字典序跑全部剧本
- `browser-svc.py`：为已拆除 mitmproxy 服务的代理 env 清理（launcher 从不注入 HTTP_PROXY，全部署形态下空操作）
- `config.yaml`：空壳 `heuristic` 段、错挂在 asynq_concurrency 下的工作区孤儿注释、拼在 callback_host 行尾的 default_image 注释（归位）、悬空 `docs/…` 链接
- `assignment/store_integration_test.go`：`SourceManual` → `SourceSelf`（阶段 1 转介：`-tags=integration` 编译失败）

### 注释 / 文档改写为现状（约 60 处）

来源旧名叙述（30+ 文件）、墓碑注释（`config.yaml` 7、`types.go` 4、cmd/proxy、dbtest、e2e.sh、Credentials 测试）、悬空包/文件引用（scanagent、einotools、activescan+passivesession「合并原…」、sitemap、internal/llm、internal/replay、external.RunCommand、mitm-capture.py、newIngestHandler、cmd/api/adapters.go、scanAdapter×5、publisher.Publish、dispatcher）、版本号变更叙述（v34+、v0100+、v0024、c78fb6ec）、兼容定性错误（skills 全量「兼容旧用法」实为现行调用、ingestor nil「向后兼容」实为单测装配、provider 空类型「与运行期一致」事实相反、repro「历史形状」实为 LLM 笔误容错）、`compose.yaml` / `deployments/README.md` 的 compose.dev.yaml、Makefile 验收门槛（5 → 实际 3）。

### 测试夹具（旧值作测试数据）

前端 7 个测试文件的 `manual`/`auto` → `self`/`proxy`、`traffic-analysis` → `executor`、退役工具 `read_insights`/`write_insight`（及其已不存在的 `findings` 分类）→ `read_credentials`/`write_credential`（`credentials`）；Go `conversation_handler_test` 同。

### 防回流

`scripts/lint-terminology.sh` FORBIDDEN 新增：passive、activescan、passivesession、SourceManual、SourceAuto、traffic-analysis、llmcall、einotools、flows/ingest（附语义备忘）。未加 exploitation（渗透领域通用词）与 scanagent（误伤 `scanAgent`）。

## 存疑未改

| 项 | 证据 | 为何不改 / 建议 |
|---|---|---|
| `executor/replayer.go` `LegacyBodyAbsent`（`body_absent` 输入别名） | `b27e926e`（10-31）前 `body_absent` 是 Go 侧正式 JSON 键，刷新配方时会以此键回写落库；现行 schema/提示词只教 `body_not_contains`；本地库全部 JSON/文本列（含 llm_invocation 全部模型输出）零命中，但本地数据均产生于 10-31 之后，不代表生产 | 直接删会令存量配方复验时静默丢断言谓词。彻底方案：数据迁移把存量 jsonb 中的 `"body_absent":` 改写为 `"body_not_contains":`（旧代码只产单键，无同对象双键冲突），之后删别名。涉及生产数据不可逆改写，**需你决策**。注释已改为写明真实原因 |

## 查过、非残留（下轮免检）

`distill.Lesson`（2026-11-03 新写于 corpus 架构之后，「教训」是 corpus 内容类型，与 `search_corpus` 描述一致）、`evaluator/verifier.go`（现行复现晋升门）、`reactRuntime`、`.gitleaks.toml` 的 `scenario/hunter`（全历史扫描所需，旧提交确含该路径）、`tools.yaml` 的 `passive DNS` / 被动 URL 发现 / `exploitation` 分类（安全领域术语）、`kube-hunter`、缓存「被动失效」、`openai_compat`、`@codemirror/legacy-modes`、内存版 GraphStore（文档化的测试替身）、`llm.NewRouter`（测试便捷构造）、repro 扁平形状包装（LLM 输出容错）。

## 验证门

| 项 | 结果 |
|---|---|
| `go build` / `go vet ./...` / **`go vet -tags=integration ./...`** | ✅ / ✅ / ✅（阶段 1 起的基线编译失败已修复） |
| gofmt / golangci-lint / 术语门禁（含新增 9 词） | ✅ / 0 issues / ✅ |
| `go test -race -short ./...` | ✅ 48 包（+cmd/e2e） |
| 前端 tsc / vitest / build | ✅ / 72 文件 588 用例 / ✅ |
| 脚本语法、YAML（config / compose×2）、browser-svc.py 编译 | ✅ |
| **全量集成测试** `go test -tags=integration ./...`（Docker pgvector） | 55 包 ✅；`internal/dispatch` ✗ ——`TestFireDue_BriefItem_ExpandsTaskAndAgent` nil 指针 panic（测试搭建未注入 conversation store），**已在阶段开始前的 c057c1c3 临时 worktree 复现，基线即有**；panic 中断该包后续用例，`HostItem_*` 两例未执行 |
| 迁移 0197 | 本地 `make migrate` → version 197 clean |

## 升级注意

- ingest 路由更名：升级瞬间仍在运行的旧 sandbox 容器持有旧 URL，所属任务结束前其抓流请求 404（按纪律不保留双路由）。
- SKILL.md 文本更新需 `make reseed` 进已初始化的库。
- `make e2e-active` 已更名 `make e2e-xss`；`cmd/e2e`/`e2e.sh` 参数改为裸剧本名。

## 转介

| 项 | 转介 |
|---|---|
| `dispatch` 集成测试搭建缺 conversation store（基线即有，CI 不编译集成测试的同一根因） | quality-governance |
| `dispatch.Service` 方法接收者仍名 `a`（adapter 遗名） | naming-overhaul |
| `body_absent` 存量数据迁移 | 用户决策 |
