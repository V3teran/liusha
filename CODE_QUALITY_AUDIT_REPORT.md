# Liusha 代码质量治理报告（第 2 轮 · 修正版）

> 执行时间：2026-10-19
> 方法：三个并行源码深读（ADK 运行时 / 数据流 / 领域模型与迁移链）+ 10 轮循环修复
> 验证：`go test -race -short ./...` 全绿；`go test -race -tags=integration ./...` 全绿（本轮前该套件完全无法运行）；`make lint`（术语门禁+vet+gofmt）通过；`golangci-lint run ./...` 0 发现

---

## 0. 对上一轮报告（同文件旧版）的勘误

旧版报告的核心 P0 结论「engagement 表仍在数据库中、agent_run/graph_node 仍挂 engagement_id、需要 0171 迁移统一」**是误报**：

- engagement 表在 0041 已 DROP，0153 二次清扫（`DROP TABLE IF EXISTS`，注释留有"代码零引用、0 行"证词）；
- graph_node/graph_edge 在 0020 已删，traffic_window 在 0002 已删；
- Go 代码全库 grep `engagement` 零命中，`task.assignment_id`/`insight.assignment_id` 早已是唯一顶层归属。

由此产生的三个未提交产物（0171 迁移 up/down、架构决策文档）引用了大量不存在的表和列，且 `internal/dbtest` 按字典序线性应用全部迁移——文件一旦留在目录即破坏全部集成测试。**已删除。**

上一轮提交 74651251 还引入了两个真实回归（见 §1.1、§1.2），本轮已修复。

---

## 1. 真实 Bug 修复（生产可触发）

### 1.1 迁移链从零构建损坏（74651251 引入）
- 74651251 把 0001_init.up.sql 的 `CREATE TABLE agent_task` 改名为 `agent_run`，但漏改同文件两处 FK（finding.task_id、llm_call.task_id 仍 `REFERENCES agent_task(id)`）。全新数据库应用 0001 即失败 → **全部集成测试自该提交起不可运行**。
- 修复：`git checkout` 还原 0001 up/down 至历史原状（历史迁移不可编辑——后续 0002/0012/0043/0054/0085 的改名链都假设 0001 建的是 agent_task）。0169 的幂等设计本就无需改 0001。
- 同源问题：0170 用 `CREATE INDEX CONCURRENTLY`，在 golang-migrate 事务模式与 dbtest 单次 Exec 下都无法执行 → 改普通索引（related_nodes 为新列、表小，锁开销可忽略）。

### 1.2 被动任务建 run 写非法角色（0162 后必失败）
- `internal/ingestor/traffic.go` 的 `enqueuePassive` 写 `Role: "traffic-analysis"`，而 0162 起 agent_run.role CHECK 只允许 planner/executor/evaluator/monitor → **每次 passive 攒批建 run 都违反约束**，被动扫描链路断裂。
- 修复：入口 run 对齐 active 路径（cmd/api 同样写 planner），用 `constants.AgentCodePlanner`；`agentComplexityTable` 清除死键 exploitation/traffic-analysis（DB 已不可能有这些角色）。

### 1.3 insight 写读路径三连 bug（0169 新功能从未真正跑通）
- `Append` 把空 SourceTaskID/SourceAgentRunID 按空串写入 uuid 列（空串是非法 uuid，归属缺位即写入失败）→ nullIfEmpty；
- `list` 把可空的 source_agent_run_id 扫进 `*string`（NULL 扫描报错）→ 指针扫描；
- `list` 的 SELECT 漏掉 `related_nodes` 列（ListByRelatedNode 返回的 RelatedNodes 恒空）→ 补列。

### 1.4 工具白名单语义矛盾（fail-open vs fail-closed）
- `tools.BuildTools` 用 `len(names)==0` 把 nil 和空 slice 混同 → 空 slice 返回全量工具；而 `registry.Allows` 与三处注释契约（monitor/planner/register）都是「nil=全量；空=空集」。evaluator 装配走 BuildTools，DB 配置空列表时拿到全量工具。
- 修复：改为 `names == nil`，附语义回归测试（`register_test.go`）。四份章程的 function_tools 均显式非空，现网行为不变。

### 1.5 流量摄入消息静默丢失
- `handleMessage` 在 defer 中无条件 XAck：落库失败（PG 抖动）也确认，消息即丢且无重投。
- 修复：ack-on-success（永久性毒消息仍 ACK）+ `reclaimLoop` 周期 XAUTOCLAIM 回收 PEL 滞留（60s 阈值/30s 扫描）。at-least-once，重投可能产生重复 proxy_traffic 行——对 LLM 驱动的分析可容忍，换取瞬时故障不丢流量。

### 1.6 HTTP 工具不响应取消
- `executeHTTPRequest` 收了 ctx 但从未绑定请求 → 任务取消/超时无法中断在途请求。修复：`req = req.WithContext(ctx)`。

---

## 2. 错误处理与可观测性

- aggregator 五处吞掉的 Redis 错误（Set/Expire/SAdd/Del/SRem）：SAdd 失败会让静默 host 永久失去超时补偿、Del 失败会让下窗口误触发——全部补 warn 日志（注入 logger，语义仍 best-effort 不改控制流）。
- runner `handle` 幂等防重检查在 GetByID 出错时被静默跳过 → 补 warn（不阻断执行，但可见）。
- ingest endpoint token 用 `!=` 明文比较 → `crypto/subtle.ConstantTimeCompare`（防时序侧信道）。
- replayer `assertDiscriminative` 里两段注释掉的实现体删除，保留一行决策说明；死变量 `commonSuccessCodes` 删除。

---

## 3. 死代码与投机抽象清除（全部先验证零消费方）

| 项 | 证据 | 处置 |
|---|---|---|
| `Tool.ConcurrencySafe()` 接口契约 | 全库无读取方（仅 setter 与接口顺从性实现），ReAct 循环串行执行工具 | 接口方法 + BaseTool 字段 + With/Set + 18 处构造调用全删 |
| `Registry.ExecuteParallel` | 仅测试引用；生产循环串行 | 方法 + 测试删 |
| `Registry.execute` | 随 ExecuteParallel 删除成孤儿（golangci unused 证实） | 删 |
| `dispatch` asynq 队列 | 全仓无生产者入队；runner 却消费空队列 | RoleDispatch/QueueDispatch/权重配置/config.yaml 行/测试全删 |
| `credential.Provider.List` | 零生产调用方 | 接口方法 + Redis 实现 + 测试替身删 |
| `Publisher.Stream()` | 零调用方 | 删 |
| `ProxyStore.maxReqBody/maxRespBody` | 构造后从不读取（AgentStore 才用） | 字段删，注释修正 |
| `DefaultReActConfig` | 仅 7 处测试引用，生产各自显式构造 | 降级为测试内私有 helper |
| e2e 死常量 pollInterval/dialTimeout/rawIOTimeout | unused | 删 |
| `docs/superpowers/specs/*.md` 引用 ×8 | 目录整体不存在 | 死引用删（注释实质内容保留） |
| skill/set.go 的孤儿 Set 注释 | `type Set` 已不存在 | 注释删；文件改名 view.go（内容=Reader+View） |

**明确不删**：`framework/core` GraphStore（经核实是 explorationgraph 的存储引擎，适配器模式，非死代码）；流式 LLM 路径（见 §6 决策项）。

---

## 4. 老架构概念清除（R1/术语治理）

- **术语门禁**：`engagement` 从「允许」改为「禁用」（表已删、代码零引用，防回流）；门禁运行 0 残留。
- **旧角色体系**（traffic-analysis/trafficAnalysis/exploitation/dispatcher/actor/parentRegistries/PreDoneCheck）：代码、注释、测试夹具、日志文案全量清除，统一为四角色（planner/executor/evaluator/monitor）。涉及 14 处注释重写（cmd/runner/main.go 包注释、ingest_handler、adapters、sandbox launcher/exec、finding、llminvocation、worker Payload、skill card 等）。
- **孤儿 spec 引用**、tools 包「17 个工具/lead/control/findings 域」陈旧包注释 → 按现状（11 个共享工具 + 角色包内自建工具）重写。
- 注释-行为对齐：executor MaxSteps（0=默认 1000，非无限制）、planner 双名注释、snapshot Duration（external 路径恒 0，如实声明）。

## 5. 命名与代码规范

- stutter 改名：`conversation.ConversationRole`→`Role`、`executor.ExecutorOutput`→`Output`（含 Parse/process 函数）、`metrics.MetricsCollector`→`Collector`。
- **golangci-lint 从 171 条降至 0**（该工具此前未接入任何入口；建议后续把 `golangci-lint run ./...` 加进 `make lint`，见 §6）。
- 中间件 Before/AfterIteration 签名、导出符号注释格式、unconvert、ctx 参数位置等全部修复。

## 6. 决策项裁决（第 3 轮，2026-10-19 下午——已全部实施并验证）

### 6.1 流式 LLM 路径 → 已整体移除
裁决依据：`StreamEnabled` 在 4 处生产构造点从不置 true 且无任何配置入口（yaml/env 全无）；
其唯一设计动机「首 token 即回调 OnThought」在生产同样无人装配；前端实时性由 scanstream
（事件级 Redis Pub/Sub）承担，产品不存在 token 级流式需求；接口税落在 Provider 全部 6 个实现上；
聚合逻辑与 Complete 解析是平行双路径（漂移风险）。未来需要时 git 历史可完整找回。
已删：Provider.Stream + StreamEvent 类型、anthropic/openai 流式实现与 helper（362 行）、
timeout/usage/retry/bridge 的 Stream、runtime StreamEnabled/callLLMStreaming、全部相关测试。
连带退场：llm_invocation 的 ttft_ms/is_stream 死列（迁移 0171 删列）+ Go 字段 + API 输出
+ 前端死展示（恒 false 门控）。

### 6.2 LLM 双路由栈 → 已合并到 framework/llm，internal/llm 退役
核实修正：初判「两套 retry 引擎」不准确——internal/llm 的分类/退避表本就别名自 framework；
真正重复的是 ①构造 switch 逐行双份（framework 那份注释明言「导出供业务策略层共用」却被无视）
②retry 循环体 Generator/Provider 两种形状各一份 ③types.go 纯别名壳。合并后：
- framework Router 补两处能力：retry 选项注入（api 从 yaml 装配退避表）+ Provider 缓存 30s TTL
  （顺带修复「前端改 provider 配置直到重启才生效」的存量缺陷——此前 InvalidateAll 全仓零调用）；
- cmd/api 以 roleRouter 适配（role → llmcfg.AgentComplexity → framework Router → Provider→Generate
  薄适配），chat/qa/msgclass 的最小依赖接口不变；
- 兜底工厂下沉 llmstore（runner/api 共用）；provider 实连探测改用 framework 构造器；
- 删除 internal/llm 整包（~620 行）与 llmstore.ProviderForRole 孤儿方法。
行为差异（有意）：api 侧配置热更新从「即时」变为「≤30s」（换来 runner 侧从「永不」变「≤30s」）。

### 6.3 internal/cognition 名不副实 → 认知循环已下沉入包
cmd/runner 的 434 行 main 包装配（四 Agent 组装/控制平面消费/生命周期/工具遥测/活动打点）
全部迁入 internal/cognition（loop/lifecycle/control/telemetry/activity.go），handler 侧只剩
Deps 组装的薄适配。理由：包名与领域核心概念（认知循环）对齐；装配链正是本轮 BuildTools
白名单 bug 的发生地，独立成包 + 显式 Deps 后可脱离 asynq/handler 单测；符合 main 包只做
装配的 Go 惯例。纯结构迁移，零行为变化（测试全绿佐证）。

### 6.4 golangci-lint 已接入 `make lint`
基线 0 发现，后续回归即失败。

## 7. 未变更项（有充分理由不动）

- traffic 两 store 的过滤构造代码「重复」：共享助手（LIKE 转义/host 抽取/截断）已在 httputil.go 集中，两处 List 的字段差异是真实领域差异（identity/tool vs contenttype/search/since），再抽公共层属过度抽象。
- store 命名 NewStore vs New：包名已含 store 后缀者用 New（llmstore.New），Go 惯例，不统一反而错。
- 迁移编号缺口 0145/0168：golang-migrate 只按字典序应用，缺口无害，不补占位迁移。

---

## 8. 变更统计

- 修改 43 个文件，新增 1 个测试文件，删除 3 个未提交的错误产物文件，还原 2 个被误改的历史迁移，文件改名 1 个（set.go→view.go）。
- 所有修复均通过全量单元 + 集成测试验证（集成套件在本轮开始时因迁移链损坏完全不可用，已恢复）。

---

## 第 4 轮：四 Agent 流式聊天接通（2026-10-19 晚）

前三轮发现「对话模块流式」是三段各自建好但从未接通的半成品（前端打字机 UI / SSE delta 转发 /
provider 流式适配）。本轮按预定方案首次端到端接通，无兼容层、无死路径：

### 数据流（单一口径）
```
ReAct runtime（四 agent 唯一 LLM 收口点）
  └ OnThoughtDelta(每 token 增量) → reasoning.Tap.Delta
      └ reasoning.Stream：按 agent 分桶，150ms 窗口合批
          └ scanstream.PublishDelta（Redis Pub/Sub，不持久化，丢帧无害）
              └ api SSE handler 转发 event:delta（带 agent_name）
                  └ 前端 store 按 agent 分道 → 每道一个打字机气泡
每轮 ReAct 结束 → Tap.Done(终稿)
  ├ 先冲刷该 agent 残留增量（保序）
  ├ 落 conversation message（kind=event + metadata{kind:reasoning,agent_name}）→ PG 真相源
  └ 广播终态消息 → 前端清空该 agent 的气泡，由正式推理卡接管
```

### 各层变更
- **framework/llm**：Provider.Stream + StreamEvent 恢复（自 7330701f^ 适配）——本次接到了真实
  消费者；新增类型化 `ErrStreamingUnsupported`（替代旧字符串匹配降级）与共享聚合器
  `CollectStream`（runtime 与 api 聚合口径唯一）。
- **runtime**：`ReActConfig.OnThoughtDelta`——回调存在即走流式（无独立开关可失步），provider
  无流式能力自动降级 Complete（行为面无感）。
- **internal/reasoning（新包）**：会话级思考流聚合器。按 agent 分桶合批（150ms）、保序广播、
  终稿落库截断（4000 runes）、无会话（convID 空）整体 no-op。
- **四 agent**：executor（EngineConfig）/ planner（Intelligence.WithThoughts）/ evaluator
  （RouterJudge.WithThoughts）/ monitor（Config）各注入 Tap；Run 成功/降级路径均收束 Done。
- **cognition**：Deps 增加 Conversations + EventPublisher（原 handler 死字段接活）；
  ConversationID 从 asynq payload 贯通至循环；循环结束 defer Close 冲刷残留。
- **api 聊天/QA**：roleRouter.generateStream（流式 + 降级）+ scanAdapter.PublishDelta
  （agent_name=assistant）；chat/qa Service 改用 GenerateStream，全文仍以落库 assistant 消息为准。
- **前端**：store 单气泡 → 按 agent 分道（liveStreams + liveOrder）；终态消息按道清空
  （reasoning 终态清 agent 道、assistant 完整消息清聊天道）；TimelineThread 每道一个
  LiveThoughtLane（agent 道=推理卡形态、assistant 道=答复块形态）；配色表旧角色
  （reconnaissance/exploitation/traffic-analysis）→ 四角色 + assistant。
- **顺带清掉**：useTypewriter/agentColor/types 里描述旧角色与旧框架的陈旧注释。

### 行为说明（如实）
- 增量帧不持久化（与 scanstream「PG 是唯一真相源」设计一致）：断线期间的打字内容丢失，
  重连由终态消息补齐——气泡清空 + 完整卡呈现。
- 四 agent 并发时时间线以 executor 为主（高频）、monitor 最稀（6 分钟/轮）。
- 工具调用/观察不进思考流（工具审计页已有完整视图）；如需在气泡内加动作标记，是
  一行增量的后续项。

### 验证
单元(-race) + 集成(testcontainers) + make lint（golangci-lint 0）+ 术语门禁 0 残留 +
前端 464/464 全绿。新增测试：runtime 流式/降级 ×2、reasoning 合批/终态/no-op/截断 ×5、
前端分道/清道/多气泡并发渲染、chat/qa 流式替身。
