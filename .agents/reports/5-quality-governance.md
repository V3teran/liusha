# 阶段 5/6 · quality-governance 报告

- 分支：pipeline/revamp（基于阶段 4 追加检查点 55e53de4）
- 范围：全仓库（Go 后端 `cmd/` + `internal/`，前端 `web/src`，CI 与 lint 配置），不限最近改动
- 方式：按域分波，三路独立通读（agent 核心 / 平台服务 / 前端），逐条验证后直接修复；每条修复都配测试，并在旧代码上确认测试失败（`git stash` 回放）

## 一、事实基线

| 模块 | 职责（以源码为准） |
|---|---|
| `cmd/api` | HTTP API + 调度器 + 下发服务的装配入口 |
| `cmd/runner` | 消费任务队列：planner → executor → evaluator 认知循环 |
| `cmd/proxy` | 代理捕获：MITM 转发，快照异步入库（`/internal/v1/traffic/ingest`） |
| `cmd/sandbox-server` | 沙箱容器内的工具执行端（浏览器 / shell / 文件） |
| `internal/dispatch` | 下发服务（人工下发 `direct`）与定时调度器 |
| `internal/planner` · `executor` · `evaluator` | 探索图写权限分工：planner 写 objective/action，executor 写 observation，evaluator 经复现门晋升 result 并交付 finding |
| `internal/framework/llm` · `runtime` · `registry` | LLM 适配（Anthropic / OpenAI 兼容）、ReAct 迭代、工具注册与拦截链 |
| `internal/workspace` · `attachment` · `sandbox` | 任务工作区文件、附件落地、沙箱启动与工作区保留清扫 |
| `internal/proxy` · `ingestor` · `traffic` | 流量捕获、聚合成批、按来源 `proxy` 生成会话 |
| `web/src` | React 19 + TanStack Query + react-router 7；页面在 `pages/`，领域组件在 `features/` |

数据流要点：

1. 来源只有两种：人工下发（`direct`，含定时任务克隆）和代理捕获（`proxy`）。
2. 每次交战对应一张探索图（`assignment_id`）。
3. 坐实主张经 `ClaimSink` 落 finding；审计链落 `exploration_verification`。

## 二、修复汇总（按 6 族）

### A 结构与抽象

- **注册表拦截链超时从未生效。** `registry` 最终执行器用的是外层 ctx，拦截器设置的超时没有传到工具。现改为链上 ctx，并删除死方法 `Interceptors()`。测试：`ContextReachesTool`。
- **动作停止信号到不了执行中的动作。** 新增 `executor/kill_switch.go`：每 3s 轮询动作状态，动作被中止时取消该动作的 ctx。此前 `kill_action` 只改了库状态，正在跑的动作感知不到。
- **复现门顺序错误。** 原顺序是先标记假设、后落图和交付；交付失败时假设已出待裁决集合，台账永久缺失。现改为先落图、再交付、最后标记，交付失败返回错误，由下一轮重试。语义重复命中时补发交付，finding 按 `dedup_key` 幂等。

### B 重复与死码

- **删除调度器无写入方的流量条目路径：**
  - `expandTrafficItem`、`proxyCronClaimLimit`、`Scheduler.ProxyStore`
  - `traffic.ClaimByIDs`、`Item.TrafficIDs`
  - 以及对应测试。全库没有模板写入这类条目，这条路径只有测试能走到。
- **删除 `body_absent` 断言别名。** 迁移 0200 把存量配方里的键改写为 `body_not_contains`（覆盖 `exploration_node.content`、`exploration_verification.primitives`、`finding.repro`），随后删除 `Assertion.UnmarshalJSON` 别名，并把该词加入术语门禁。这是阶段 3 转介的用户决策项，按「不兼容老代码」执行。
- 删除 `manifest.CategoryOrder` / `CategoryName`。
- **前端删除死代码：**
  - `useCredentialDetail`、`usePurgeCredentialSessions`、`useDeleteCredentialHost`
  - `purgeCredentialSessions`、`deleteCredentialHost` 及其测试
  - 测试里遗留的 `console.log`
  - 后端对应路由保留，作为运维 API。

### C 正确性与可观测

**代理（`proxy/server.go`）**

- **请求体被截断转发：** 原实现读取快照上限后重建 body，超出部分丢失。现在 `captureBody` 用捕获头部拼接剩余流，转发完整 body。
- **解压改了转发头：** 原实现解压后改写了转发出去的 `Content-Encoding`。现在只对克隆出的快照头解压。
- **解压炸弹：** `decompressIfEncoded` 增加输出上限，默认 32MiB。
- **日志泄露完整 URI**（含查询串里的令牌）：统一改为 `LogPath()`。覆盖 runner ingest、proxy、ingestor 三处。

**工作区（`workspace`）**

- **符号链接逃逸：** 原先只做词法检查。现在基于 `os.Root` 重写，内核级拒绝越界，并补测试 `TestSymlinkEscapeRejected`。
- **并发同名覆盖：** `WriteFrom` 改用 `O_EXCL` 原子落位。附件 `placeUnique` 在冲突时重新取名。
- **附件部分落地：** `Materialize` 改为两阶段：先全部校验，再移动，失败回滚。

**HTTP 接口与后台**

- **下载接口：** 打包失败时，原来返回 200 和半截 zip，现在在首字节前返回 404，流中途失败会记日志。文件名改用 RFC 2231 编码，解决中文文件名乱码；目录名取值也已修正。
- **入队失败后 run 永久挂起：** 下发和恢复两条路径都会在入队失败时把 run 置为 aborted。
- **工作区保留清扫：** 原按目录 mtime 判断，误删仍在写入的任务。现在按任务内最新文件时间判断，跳过点目录，只删除空的交战目录。

**LLM 与 agent 循环**

- **流截断被当作成功：** 流在没有终止事件时就关闭，现在会报 `ErrStreamTruncated`。用量统计同步处理。
- **请求级 `MaxTokens` 被忽略：** 现在贯通 openai 和 anthropic 两条路径，thinking 预算统一按上限钳制。
- **裁决官读不到假设陈述：** `extractHypothesis` 读的是错误的键，导致裁决官始终收到空假设，现改用 `attemptStatement`。
- **浏览器自愈重试丢失交战 ID：** 原重试不带 `AssignmentID`；超时或未恢复时也不报错。现统一走 `executeBrowserCommand`。
- **planner 吞错推进水位：** 分析出错时，planner 仍会推进已分析结果水位。现返回错误，多个错误用 `errors.Join` 聚合。
- **事件总线订阅竞态：** `SubscribeTask` 改为等待注册完成，或等到总线关闭。
- **静默失败改为告警：**
  - `kill_action` 校验目标是 action 节点
  - corpus rerank 失败告警
  - `checkActionAborted` 失败告警

**前端**

- **复现命令注入：** `findingEvidence` 生成 curl 命令时，方法名改为白名单，参数加 shell 引号。
- **截断文件保存后丢内容：** 截断文件原本可以编辑保存，会用截断内容覆盖原文件。现在只读预览，保存时校验仍是同一文件，失败提示。
- **迟到响应覆盖新状态：** 会话详情、会话列表、技能装配三处加了代际或请求序号守卫，切换后迟到的响应被丢弃。
- **日志流轮询重叠：** 固定间隔轮询改为串行 setTimeout，上一次请求结束后才发起下一次。
- **深链状态双源：** 会话页的 `?conv=` 改为唯一事实源，切换会话后刷新或分享都能还原。
- **凭据页部分失败被吞：** 现在有失败横幅和重试按钮。
- **代理端口文案写错：** 原文案写 8888，实际是 8588；容器部署时宿主端口为 58588。

### D 类型与契约

- **`tsc -b` 从不检查测试文件。** 新增 `web/tsconfig.test.json` 并加入 references，随后修复约 26 处 fixture 与真实类型的漂移，主要是漏字段和旧字段名。

### E 测试

- 以上每条修复都有对应的回归测试。
- `Composer` 中虚构的 `scanning` 用例改为真实的「上传中回车」用例。
- 去重用例的期望已按新的交付顺序修正。
- **测试日志污染包目录：** 根因是包级 logger 在测试启动时初始化，缺省相对目录 `logs` 会在每个被测包下生成 `logs/*.test.log`。`logx` 现在在 `testing.Testing()` 下缺省关闭文件落盘，显式设置 `LIUSHA_LOG_TO_FILE` 仍生效。测试：`TestLoadConfig_FileSinkOffByDefaultUnderTest`。

### F 规范与表达

**`.golangci.yml`：旧配置被静默忽略，此前的「0 issues」没有意义。**

- 重写为合法的 v2 schema。
- 新增 linter：bodyclose、errorlint、nilerr。
- 关闭 hugeParam、rangeValCopy，并写明理由。
- gocyclo 阈值设为 30，并写明理由。
- 收敛后 0 issues。

**CI（`.github/workflows/ci.yml`）**

- 集成测试步骤补上 `-tags=integration`。此前集成用例从未编译，`-run Integration` 只是按名字过滤。
- 新增 golangci-lint 步骤（版本钉在本地已验证的 v2.15.0）和术语门禁。
- 新增 web job：node 22、corepack pnpm、`pnpm build`（含 tsc）、`pnpm test`。
- 镜像构建依赖 test 和 web 两个 job。

**errorlint 修复**

- `%w: %w` 双包装，以及 `errors.Is`/`errors.As` 替代 `==` 和类型断言。
- 涉及 `cmd/runner`、aggregator、planner、sandbox server。

**前端分包**

- `WorkspacePage` 从 688KB 降到 13.8KB。CodeMirror 编辑器抽成 `features/workspace/FileEditor`，懒加载。
- 语言包按扩展名动态 import，加载失败退化为纯文本编辑。
- Vite 大包警告消除。

## 三、验证门（全绿）

| 命令 | 结果 |
|---|---|
| `go build ./... && go vet ./... && go vet -tags=integration ./...` | 通过 |
| CI 口径 `gofmt -l` | 无差异 |
| `golangci-lint run ./...`（v2.15.0） | 0 issues |
| `scripts/lint-terminology.sh` | TOTAL_RESIDUAL=0 |
| `go test -race -short ./...` | 全部通过 |
| `go test -race -tags=integration ./...`（testcontainers 实拉 pg） | 全部通过（56 包 ok）。阶段 3 记录的 dispatch 基线失败已修 |
| `cd web && npx tsc -b && pnpm build` | 通过，无大包警告 |
| `npx vitest run` | 74 文件 / 601 用例通过 |
| 迁移 0200 | 本地 up 到 version 200，探针行改写已验证 |

## 四、停止判据

触发判据 1（机械信号）：

- 三路体检报告的全部发现已修复，共 38 条：agent 核心 14、平台 10、前端 14。
- 前序阶段转介本阶段的 4 项与用户决策项 `body_absent` 已全部处理。
- 验证门全绿。

收口时**无剩余未处理发现**。体检中被否决、未改动的项（答不出实际危害，按纪律不动）：

- 大结构体按值传递、range 值拷贝：在每步一次 LLM 调用的量级下不可感知，已通过 lint 配置写明理由。
- gocyclo 21–30 区间的错误分类和参数分派函数：都是线性结构，拆分收益低于回归风险。
- 凭据「清空会话」和「删除整 host」后端路由保留：属于运维 API 面，只删除了前端的无调用方封装。

## 五、发布说明（本阶段新增）

- 执行迁移 0200：断言键 `body_absent` 改为 `body_not_contains`。没有兼容别名；down 为 no-op。
- `make lint` / CI 需要 golangci-lint ≥ v2（配置为 v2 schema）。
- 测试进程缺省不再写日志文件。需要时设置 `LIUSHA_LOG_TO_FILE=true`。

## 六、`.agents/governance.md` 建议稿（待用户确认入库）

```markdown
# 质量治理配置

## 领域锚点
- 来源：direct（人工下发，含定时克隆）/ proxy（代理捕获）——DB/API/路由/前端同名
- 探索图写权限：planner→objective/action；executor→observation；evaluator→result（唯一晋升门）
- 坐实交付：evaluator.ClaimSink → finding（dedup_key 幂等）

## 波次
1. agent 核心：internal/{planner,executor,evaluator,monitor,framework,registry,tools,corpus}
2. 平台服务：internal/{dispatch,runner,proxy,ingestor,traffic,workspace,attachment,sandbox,httpapi,bus,logx} + cmd/
3. 前端：web/src

## 验证门
go build ./... && go vet ./... && go vet -tags=integration ./...
gofmt -l $(find . -name '*.go' -not -path './vendor/*')
golangci-lint run ./...
scripts/lint-terminology.sh
go test -race -short ./...
go test -race -tags=integration ./...      # 需 Docker（testcontainers）
cd web && pnpm build && pnpm test          # build 含 tsc -b（覆盖测试文件）
```
