# AI 渗透测试 Agent 架构设计

> 版本：v1.1
> 状态：待落地实现（已审查）

---

## 一、一句话概括

> 分层五级：Provider（LLM）→ Actor（ReAct 引擎）→ Dispatcher（战术工厂）→ Cognition（并发PAE循环）→ Assignment/Task（调度层）。真正的 LLM Agent 恰好两类：Planner（策略）和 Actor（战术）。Critic 是轻量判断器。其余全是确定性逻辑。

---

## 二、系统总览

```
╔══════════════════════════════════════════════════════════════╗
║  调度层 (infrastructure)                                      ║
║  Assignment ─── Task ─── Cognition.Run(taskID, campaign)     ║
╠══════════════════════════════════════════════════════════════╣
║  战略层 (strategic)                              [LLM① planner tier] ║
║  Planner ──读── Ledger + Frontier ──写→ PlannerState          ║
║  输出：[]Move（含 DependsOn 依赖边）                           ║
╠══════════════════════════════════════════════════════════════╣
║  战术层 (tactical)                          [LLM② executor tier] ║
║  Dispatcher ──按 MoveKind 选 Profile──→ Actor (ReAct 引擎)   ║
║  输入：Move  输出：[]Execution                                 ║
╠══════════════════════════════════════════════════════════════╣
║  证据层 (evidence)                                            ║
║  Verifier ──Replayer 复现──→ Landmark/Finding ──写入──→ Ledger║
╠══════════════════════════════════════════════════════════════╣
║  持久层 (persistence)                                         ║
║  Ledger（世界模型）│ Graph（因果图，懒加载）│ Corpus（跨任务）║
╚══════════════════════════════════════════════════════════════╝
```

### 主循环（Cognition Loop，并发版本）

```
sandbox := Sandbox.Spawn(taskID)
defer sandbox.Close()

go TrafficIngester.Watch(ctx, sandbox.ProxyURL, taskID, ledger, verifier)
// 并行于主循环，监听 MITM 代理流量 → http_trace Signal → Verifier.Promote → ledger.Write

state := PlannerState{}

loop:
  result := Planner.Plan(PlanReq{
      Frontier:         ledger.Frontier(taskID),       // confirmed 且无出边的 landmark（必须注入）
      TopKLandmarks:    ledger.TopK(taskID, frontier, 30), // 相关性 Top-30 Summary
      History:          history,
      State:            state,
      EnabledMoveKinds: campaign.EnabledMoveKinds,
      Budget:           scanBudget.Remaining(),
  })
  if result.Halt != nil || scanBudget.Exhausted() → halt

  state = result.State

  // 拓扑排序 + 并发执行无依赖 Move
  batches := topoSort(result.Moves)
  for _, batch := range batches {
      results := fanOut(batch, func(move Move) MoveOutcome {
          executions := Dispatcher.Execute(ctx, move, sandbox)
          landmark, finding := Verifier.Promote(move, executions)
          if landmark != nil { ledger.Write(landmark) }
          if finding  != nil { findings.Write(finding) }
          moveRecord.Write(MoveRecord{Move: move, Status: done, ...})
          return MoveOutcome{Move: move, Landmark: landmark, Finding: finding}
      }, campaign.MaxConcurrentMoves)
      history = append(history, toMoveRecords(results)...)
      if anyHaltSignal(results) { break loop }
  }

  // 扫描结束后触发报告生成（PostScanHook，非 MoveKind）
  if shouldHalt { campaign.PostScanHook(taskID) }
```

`topoSort` 按 `Move.DependsOn` 建依赖图，拓扑分层后每层内的 Move 并发执行。`fanOut` 限制并发数为 `ScanBudget.MaxConcurrentMoves`（默认 3）。

---

## 三、智能体边界

| 组件 | 类型 | LLM Tier |
|------|------|----------|
| **Planner** | LLM Agent | `planner`（推理级，如 Opus） |
| **Actor** | LLM Agent | `executor`（快速级，如 Sonnet） |
| **Critic** | LLM 判断器 | `inspector`（最便宜，如 Haiku） |
| Dispatcher | 确定性工厂 | — |
| Verifier | 确定性（Replayer）+ 可选 inspector | — |
| Cognition | 确定性调度 | — |
| Ledger | 数据层 | — |

三个 tier 均通过 `llmcfg` DB 配置，不 hardcode 模型名。

**结论：有且仅有两类真正的 Agent。Planner 是策略 Agent，Actor 是战术 Agent。Critic 和 Verifier 的可选 LLM 调用是判断器，不独立规划。**

---

## 四、核心类型系统

### 4.1 Move（策略意图单元）

```go
type MoveKind string

const (
    MoveKindRecon         MoveKind = "recon"
    MoveKindScan          MoveKind = "scan"
    MoveKindExploit       MoveKind = "exploit"
    MoveKindUseCredential MoveKind = "use_credential"
    MoveKindPostExploit   MoveKind = "post_exploit"
)

// report 不是 MoveKind，由 Campaign.PostScanHook 在扫描结束后自动触发

type Move struct {
    ID          string
    Kind        MoveKind
    Target      LandmarkRef  // 结构化引用，替代字符串 slug
    Objective   string
    Cues        []string     // Planner 给 Actor 的提示
    Constraints []Constraint // 结构化约束，非字符串
    Priority    int
    DependsOn   []string     // 依赖的 Move.ID，空=无依赖（可并发）
}

// Constraint 是结构化约束，不靠 LLM 自我遵守
type Constraint struct {
    Kind  ConstraintKind
    Value string // 约束参数（如 max_severity=medium）
}

type ConstraintKind string

const (
    ConstraintPassiveOnly    ConstraintKind = "passive_only"
    ConstraintNoDestructive  ConstraintKind = "no_destructive"
    ConstraintMaxSeverity    ConstraintKind = "max_severity"
    ConstraintRateLimit      ConstraintKind = "rate_limit_rps"
)

type MoveStatus string

const (
    MoveStatusDone      MoveStatus = "done"
    MoveStatusAbandoned MoveStatus = "abandoned"
    MoveStatusStalled   MoveStatus = "stalled"
)

// MoveRecord 携带结果上下文供 Planner 决策
type MoveRecord struct {
    Move
    Status   MoveStatus
    HaltWhy  string   // Critic/Budget 给的终止原因摘要
    Findings []string // Finding.ID 列表
}
```

### 4.2 LandmarkRef（结构化目标引用，域无关）

替代原来的字符串 slug，消除 IPv6、URL 路径中斜杠导致的解析歧义，同时支持 web/binary/cloud/lateral 全域。

```go
// LandmarkRef 唯一标识一个目标节点，domain-agnostic 三元组。
// Locator 的语义由对应 Domain Profile 解释，核心层不 parse。
type LandmarkRef struct {
    Domain  string // "web" | "binary" | "cloud" | "lateral"
    RefKind string // endpoint | file | resource | node | ... （域内类型）
    Locator string // 域内寻址字符串，url-encoded
}

func (r LandmarkRef) Key() string {
    // 规范化输出：domain:ref_kind:url_encoded(locator)
    // 例："web:endpoint:%2Fapi%2Fv1%2Fusers"
}

func (r LandmarkRef) Display() string {
    // 人类可读短文本，前端展示用
}
```

### 4.3 Landmark（世界模型节点）与 Finding（报告实体）

两者严格分离：Landmark 是运行时世界图的节点，Finding 是最终渗透报告的产物。

```go
type LandmarkKind string

const (
    LandmarkTarget     LandmarkKind = "target"
    LandmarkService    LandmarkKind = "service"
    LandmarkEndpoint   LandmarkKind = "endpoint"
    LandmarkCredential LandmarkKind = "credential"
    LandmarkWeakness   LandmarkKind = "weakness"
    LandmarkSession    LandmarkKind = "session"    // 已获取的访问会话
    LandmarkArtifact   LandmarkKind = "artifact"   // 文件、截图等制品
    LandmarkGoal       LandmarkKind = "goal"       // 任务目标节点
)

type LandmarkState string

const (
    LandmarkHypothesized LandmarkState = "hypothesized" // Planner 预设，未验证
    LandmarkConfirmed    LandmarkState = "confirmed"
    LandmarkRefuted      LandmarkState = "refuted"
)

type Landmark struct {
    ID         string
    Ref        LandmarkRef
    Kind       LandmarkKind
    State      LandmarkState
    Summary    string        // ≤1 行，注入 Planner/Actor 系统提示
    Detail     string        // 完整内容，read_landmark(id) 按需拉取
    Signals    []Signal
    Confidence float64       // 0~1，基于 Signal 权重加权均值
    TaskID     string
    MoveID     string
    CreatedAt  time.Time
    UpdatedAt  time.Time
}

type FindingStatus string

const (
    FindingStatusOpen            FindingStatus = "open"
    FindingStatusConfirmed       FindingStatus = "confirmed"
    FindingStatusFixed           FindingStatus = "fixed"
    FindingStatusFalsePositive   FindingStatus = "false_positive"
    FindingStatusAccepted        FindingStatus = "accepted"
)

type Finding struct {
    ID            string
    TaskID        string
    LandmarkID    string        // 关联的 Landmark（可为空）
    Title         string
    Severity      Severity
    Description   string
    Evidence      []Signal
    Remediation   string
    DependsOn     []string      // 前置 Finding.ID（链式漏洞）
    MoveID        string
    // 标准化字段（去重键 + 报告输出）
    CWEID         string        // 如 "CWE-89"，findings 去重键的一部分
    OWASPCategory string        // 如 "A03:2021"
    Repro         json.RawMessage // 完整复现步骤（repro_cmd + params）
    // 工单流转
    Status        FindingStatus
    TriageNote    string
    TriagedAt     *time.Time
    Seq           int64         // 稳定短号，前端展示
    CreatedAt     time.Time
}

type Severity string

const (
    SeverityCritical Severity = "critical"
    SeverityHigh     Severity = "high"
    SeverityMedium   Severity = "medium"
    SeverityLow      Severity = "low"
    SeverityInfo     Severity = "info"
)
```

### 4.4 Signal（证据单元）

```go
type SignalKind string

const (
    SignalCmdOutput      SignalKind = "cmd_output"      // weight 1.0
    SignalHTTPTrace      SignalKind = "http_trace"      // weight 0.9
    SignalFileContent    SignalKind = "file_content"    // weight 0.8
    SignalCredentialDump SignalKind = "credential_dump" // weight 1.0（最高可信）
    SignalNetworkScan    SignalKind = "network_scan"    // weight 0.7
    SignalScreenshot     SignalKind = "screenshot"      // weight 0.3
)

var signalWeight = map[SignalKind]float64{
    SignalCmdOutput:      1.0,
    SignalHTTPTrace:      0.9,
    SignalFileContent:    0.8,
    SignalCredentialDump: 1.0,
    SignalNetworkScan:    0.7,
    SignalScreenshot:     0.3,
}

type Signal struct {
    Kind        SignalKind
    ToolName    string     // 产生此信号的工具名（nmap / sqlmap / curl / …）
    Content     string     // ≤4096 字符摘要
    Detail      string     // 完整内容，按需拉取
    Truncated   bool
    CapturedAt  time.Time
}
```

**Confidence 计算**：`min(1.0, weightedMean(signals[:3]))`，取前三条信号的权重均值，上限 1.0。

**Verifier 证据门槛（两级）**：

| LandmarkKind | 最低 Signal 数 | 晋升方式 |
|---|---|---|
| target / service / endpoint / artifact | 0 | 直接晋升（Replayer 传空 primitives） |
| session / weakness | ≥ 1 | Replayer.Replay 复现坐实 |
| credential | ≥ 1 | Replayer.Replay 复现坐实 |

### 4.5 Actor（ReAct 执行引擎）

```go
type Actor struct {
    provider   Provider
    registry   *Registry
    compactor  Compactor
    critic     Critic
    checkpoint CheckpointStore
}

type ActorReq struct {
    System             string       // 不参与压缩：Profile.SystemPrompt + Landmark summaries + Move 上下文
    Inbox              []Message    // 参与压缩：初始指令
    Hypotheses         []string     // Working Memory：上次 Execution 遗留的临时假设
    Budget             Budget
    Settle             SettleConfig
    PendingConstraints []Constraint // 结构化约束，PreExecute hook 检查
}

type ActorResult struct {
    Steps      []Step
    Conclusion string
    Halt       HaltReason
    Usage      Usage
}

type Step struct {
    Index      int
    Thought    string       // LLM 文本部分（tool call 之前的推理）
    Raw        []Message    // 未压缩原始 history delta
    ModelSeen  []Message    // 模型实际收到的（压缩后）
    ToolCalls  []ToolCall
    Results    []ToolResult
    Hypotheses []string     // 本 Step 更新后的 Working Memory
}

type HaltReason string

const (
    HaltDone       HaltReason = "done"
    HaltBudget     HaltReason = "budget"
    HaltWatchdog   HaltReason = "watchdog"
    HaltConstraint HaltReason = "constraint"  // 结构化 Constraint 触发（PreExecute hook）
    HaltCancelled  HaltReason = "cancelled"   // context cancel（Directive halt）
    HaltError      HaltReason = "error"
)
```

**Constraint 检查时机**：Registry 的 `PreExecuteInterceptor` 在**工具调用前**检查 `PendingConstraints`，非空则拒绝调用并返回 `HaltReason: constraint`，不等工具执行完毕。

**硬中断**：`DirectiveKind = "halt"` 通过传入的 `context.CancelFunc` 立即取消，`Sandbox.Exec` 响应 context cancel，响应延迟降至毫秒级。

### 4.6 Execution（单次 Move 执行包装）

原名 Attempt，已改名为 Execution，避免与 Verifier 层的晋升尝试概念混淆。

```go
// 一个 Move 可能被 Dispatcher 执行多次（Critic 返回 Steer 后调整重跑）
type Execution struct {
    Index      int
    Result     ActorResult
    Steer      string   // Critic 给的 steering 文本，首次为空
    Hypotheses []string // 本次 Execution 结束时的 Working Memory，传给下次 Execution
}
```

`Verifier.Promote` 从 `[]Execution` 中按 `max(Confidence)` 选择最优，平局取 Step 数最多的。

### 4.7 Verifier（证据门控，Replayer 为主门）

```go
// Replayer 是 domain-specific 复现执行器。Verifier 把"复现"委托给它，自身不碰域细节。
// primitives 是要回放的原语序列（web=replay_traffic、binary=gdb、cloud=API 调用）。
type Replayer interface {
    Replay(ctx context.Context, primitives json.RawMessage) (ReplayResult, error)
}

type ReplayResult struct {
    Confirmed  bool            // 复现是否坐实（决定能否进图）
    Evidence   json.RawMessage // 复现证据（req/resp、崩溃现场、API 响应…）
    DurationMs int64
}

type Verifier struct {
    replayer  Replayer  // 必须，无 Replayer 则无晋升能力
    inspector Provider  // 可选，仅用于 artifact/screenshot 类 Signal 的辅助语义验证
}

// New 构造 Verifier。replayer 为 nil 时 Promote 报错。
func New(replayer Replayer, inspector Provider) *Verifier

func (v *Verifier) Promote(ctx context.Context, move Move, executions []Execution) (*Landmark, *Finding, error)
```

**Promote 执行流**：
1. 从 `[]Execution` 中选 max(Confidence) 的 Execution
2. 调 `Replayer.Replay(primitives)`
3. 无论结果如何，调 `RecordVerification`（审计链不断）
4. 复现未坐实 → 返回 `(nil, nil)`，Landmark 保持 `hypothesized`
5. 复现坐实 → UpsertNode（confirmed）→ 可选对 artifact/screenshot 类 Signal 调 inspector 辅助验证
6. 输出 `*Landmark, *Finding`

### 4.8 Budget + SettleConfig + ScanBudget

```go
type Budget struct {
    MaxSteps          int
    MaxTokens         int     // 实际 token 计数（Provider.CountTokens），非估算
    WatchdogSecs      int
    CompactionTrigger float64  // default 0.70，触发上下文压缩
    CriticInterval    int      // default 5，每 N 步评估一次（特殊情况强制立即评估）
}

type SettleConfig struct {
    Threshold    float64    // default 0.85，触发结算指令注入
    Directive    string     // 结算指令（per MoveKind 不同，由 Profile 定义）
    AllowedTools []string   // 结算阶段只开放这些工具
}

// Scan 级别总预算
type ScanBudget struct {
    MaxMoves           int
    MaxTotalTime       time.Duration
    MaxConcurrentMoves int // 并发 Move 上限，default 3
}
```

**Critic 调用时机**（以下任一触发立即评估，否则按 `CriticInterval` 轮询）：
- Actor 发出 conclusion
- 触发 SettleConfig 阈值
- 连续 `CriticInterval` 个 Step 的工具全部出错

### 4.9 Critic + Assessment

```go
type Critic interface {
    Evaluate(ctx context.Context, move Move, recent []Step) (Assessment, error)
}

type Assessment struct {
    Advancing   bool
    Observation string
    Verdict     Verdict
}

type Verdict string

const (
    VerdictContinue Verdict = "continue"
    VerdictSteer    Verdict = "steer"
    VerdictAbandon  Verdict = "abandon"
)
```

### 4.10 Planner + PlannerState

```go
type Planner interface {
    Plan(ctx context.Context, req PlanReq) (PlanResult, error)
}

type PlanReq struct {
    TaskID           string
    Frontier         []Landmark    // confirmed 且无出边的 landmark（攻击前沿，必须注入，通常 <20 个）
    TopKLandmarks    []Landmark    // 按与 Frontier 的相关性取 Top-30 的 Summary（不是全量）
    // 完整 Detail 由 Actor 通过 read_landmark(id) 按需拉取，不进 Planner 提示词
    History          []MoveRecord  // 含 Status + HaltWhy + Findings
    State            PlannerState
    EnabledMoveKinds []MoveKind    // Campaign 允许的 MoveKind，Planner 严格遵守
    Budget           ScanBudgetRemaining
}

type PlanResult struct {
    Moves []Move          // 含 DependsOn 依赖关系
    State PlannerState
    Halt  *PlanHalt       // nil = 继续；非 nil = 终止原因
}

// PlannerState 替代 map[string]json.RawMessage，schema 固定防止跨轮漂移。
// Extensions 仅允许写预定义键（domain_notes / priority_override），
// Planner 输出 JSON Schema 通过 additionalProperties: false 约束可写键，防止跨轮语义漂移。
type PlannerState struct {
    HumanCues     []string          `json:"human_cues"`
    StrategyNotes string            `json:"strategy_notes"`
    Hypotheses    []string          `json:"hypotheses"`
    Extensions    map[string]string `json:"extensions"` // 仅允许预定义键：domain_notes, priority_override
}

type ScanBudgetRemaining struct {
    RemainingMoves int
    RemainingTime  time.Duration
}
```

**Planner System Prompt 框架**（必须包含以下部分，具体内容由 Profile 注入）：

```
# 角色
你是渗透测试战略规划者。根据当前世界状态和历史执行记录，规划下一批可并发执行的 Move。

# 攻击前沿（优先覆盖，confirmed 且无出边）
{frontier}

# 相关世界状态（Top-30 摘要）
{topKLandmarks}
// 按与当前 Frontier 的相关性排序；完整详情通过 read_landmark(id) 按需拉取

# 可用行动类型
{enabledMoveKinds}

# 执行历史
{history}

# 渗透测试方法论
{methodologyBlock}
// recon → scan → exploit → use_credential → post_exploit 标准流程
// credential 发现后应规划 use_credential Move
// weakness 发现后应规划 exploit Move

# 规划约束
- DependsOn 必须引用已存在或本批次中的 Move.ID
- 不重复规划 History 中已 done 的等价 Move
- 尊重 Budget.RemainingMoves 不过度展开
- Extensions 只写 domain_notes 或 priority_override 键
```

### 4.11 Dispatcher（Move-aware Actor 工厂）

```go
type Dispatcher struct {
    profiles map[MoveKind]Profile
    provider Provider
    sandbox  *Sandbox
}

type Profile struct {
    MoveKind     MoveKind
    SystemPrompt string
    Tools        []Tool
    Budget       Budget
    Settle       SettleConfig
    MaxExecutions int
}

func (d *Dispatcher) Execute(ctx context.Context, move Move, sandbox *Sandbox) ([]Execution, error) {
    profile := d.profiles[move.Kind]
    var executions []Execution
    var hypotheses []string

    for i := 0; i < profile.MaxExecutions; i++ {
        req := d.buildReq(profile, move, hypotheses, executions)
        result, err := newActor(d.provider, profile).Run(ctx, req)
        if err != nil { return executions, err }

        exec := Execution{Index: i, Result: result, Hypotheses: result.lastHypotheses()}
        if i > 0 { exec.Steer = executions[i-1].steerText }
        executions = append(executions, exec)
        hypotheses = exec.Hypotheses

        assessment, _ := d.critic.Evaluate(ctx, move, result.Steps)
        switch assessment.Verdict {
        case VerdictContinue, VerdictAbandon:
            return executions, nil
        case VerdictSteer:
            executions[len(executions)-1].steerText = assessment.Observation
        }
    }
    return executions, nil
}
```

### 4.12 Directive（人工干预）

```go
type DirectiveKind string

const (
    DirectiveGuidance   DirectiveKind = "guidance"
    DirectiveConstraint DirectiveKind = "constraint"
    DirectiveHalt       DirectiveKind = "halt"
)

type Directive struct {
    Kind       DirectiveKind
    Content    string
    Constraint *Constraint
}
```

**Directive 注入流**：

```
前端
  ├─ DirectiveGuidance    → PlannerState.HumanCues 追加 → 下轮 Planner 读取
  ├─ DirectiveConstraint  → Actor.PendingConstraints → PreExecuteInterceptor 工具调用前检查
  └─ DirectiveHalt        → context.CancelFunc() → Sandbox.Exec 响应 cancel → 毫秒级停止
```

### 4.13 Tool（工具接口）

```go
type Tool interface {
    Name() string
    ShortDesc() string
    Desc() string
    Schema() json.RawMessage
    Execute(ctx context.Context, args json.RawMessage) (ToolResult, error)
}

type ToolResult struct {
    Output  string
    Signal  *Signal
    Error   string
}

// Registry 并发执行一批 tool_call（LLM 单次返回多个调用时并发派发，结果按原始顺序收集）
func (r *Registry) ExecuteParallel(ctx context.Context, calls []ToolCall) []ToolResult
```

### 4.14 Provider（LLM 抽象）

```go
type Provider interface {
    Complete(ctx context.Context, req Request) (Response, error)
    Stream(ctx context.Context, req Request) (<-chan StreamEvent, error)
    CountTokens(ctx context.Context, req Request) (int, error)
    ModelID() string
}

type StreamEvent struct {
    Kind    StreamEventKind // thinking | text | tool_start | tool_end | done
    Content string
    Tool    *ToolCall
    Result  *ToolResult
}
```

### 4.15 Sandbox（执行环境，per-scan 容器）

```go
type Sandbox struct {
    TaskID   string
    WorkDir  string
    ProxyURL string
    cancel   context.CancelFunc
}

func (s *Sandbox) SubEnv(moveID string) *Sandbox
func (s *Sandbox) Exec(ctx context.Context, cmd []string, env map[string]string) (ExecResult, error)
func (s *Sandbox) ReadFile(ctx context.Context, path string) ([]byte, error)
func (s *Sandbox) WriteFile(ctx context.Context, path string, content []byte) error
func (s *Sandbox) ListDir(ctx context.Context, path string) ([]string, error)
func (s *Sandbox) Cleanup(ctx context.Context, pattern string) error
func (s *Sandbox) Close() error

type Attachment struct {
    Name    string
    Path    string // Sandbox 内路径
}

type ExecResult struct {
    Stdout   string
    Stderr   string
    ExitCode int
    TimedOut bool
    Files    []Attachment // 工具运行产出的文件（如 sqlmap dump）
    Warnings []string     // 非致命警告（版本不兼容等）
}
```

### 4.16 Campaign（前端可配置项）

```go
type Assignment struct {
    ID       string
    Targets  []Target
    Campaign Campaign
}

type Target struct {
    ID    string
    Host  string
    Brief string
}

type Campaign struct {
    EnabledMoveKinds   []MoveKind
    BudgetOverrides    map[MoveKind]Budget
    GlobalConstraints  []Constraint
    ScanBudget         ScanBudget
    PostScanHook       func(taskID string) // 扫描结束后自动触发报告生成，非 MoveKind
}
```

---

## 五、Package 结构

```
internal/
├── provider/
│   ├── provider.go
│   ├── anthropic.go
│   ├── openai.go
│   ├── retry.go          # 最多 3 次，指数退避 1s/2s/4s；5xx 可重试，4xx/cancel 不重试
│   └── router.go
│
├── actor/
│   ├── actor.go
│   ├── budget.go
│   ├── compactor.go      # 使用 CountTokens，非估算
│   ├── critic.go
│   ├── image.go
│   ├── checkpoint.go
│   └── types.go
│
├── registry/
│   ├── registry.go       # PreExecuteInterceptor + ExecuteParallel
│   └── tool.go
│
├── dispatcher/
│   ├── dispatcher.go
│   └── profile/
│       ├── recon.go
│       ├── scan.go
│       ├── exploit.go
│       ├── use_credential.go
│       └── post_exploit.go
│
├── ledger/
│   ├── ledger.go         # 含 Frontier() / TopK() 方法
│   ├── landmark.go
│   └── store.go          # 幂等键：(scan_id, kind, landmark_ref.Key())
│
├── verifier/
│   └── verifier.go       # Replayer 接口 + RecordVerification
│
├── ingester/
│   └── traffic.go        # TrafficIngester：监听 MITM 代理流量 → Signal → Verifier
│
├── env/
│   ├── env.go
│   └── container.go
│
├── planner/
│   └── planner.go
│
├── cognition/
│   └── run.go            # topoSort + fanOut + 崩溃恢复
│
└── graph/
    ├── graph.go
    └── compute.go        # 懒加载，动态计算
```

**DB 表（新增）**：

```sql
CREATE TABLE actor_checkpoint (
    scan_id     TEXT NOT NULL,
    move_id     TEXT NOT NULL,
    step_idx    INT  NOT NULL,
    thought     TEXT,
    hypotheses  JSONB,
    raw         JSONB,
    tool_calls  JSONB,
    results     JSONB,
    created_at  TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY (scan_id, move_id, step_idx)
);

CREATE TABLE scan_state (
    scan_id       TEXT PRIMARY KEY,
    status        TEXT NOT NULL CHECK (status IN ('active','done','failed')),
    planner_state JSONB,
    created_at    TIMESTAMPTZ DEFAULT now(),
    updated_at    TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE move_record (
    scan_id    TEXT NOT NULL,
    move_id    TEXT NOT NULL,
    status     TEXT NOT NULL CHECK (status IN ('in_flight','done','abandoned','stalled')),
    halt_why   TEXT,
    findings   JSONB,
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY (scan_id, move_id)
);
```

---

## 六、记忆机制（四层）

### Actor Memory（短期，单次 Move）

```
1. compressImages(history, maxImages=3)
2. dedupReadTools(history)
3. actualTokens = Provider.CountTokens(history)
   actualTokens > Budget.CompactionTrigger × ContextWindow?
   是 → step 4；否 → 跳过
4. partition → [head | trailing | candidates]
   head     = System + first user message（永不压缩）
   trailing = 最近 N 轮完整 turn（保证 tool_call/result 对完整）
   candidates = 中间部分（压缩时保证不拆 tool_call/result 配对）
5. Provider.Complete(summarize prompt + candidates) → summary_message
   token 消耗计入 Budget.TokensUsed
6. fallback: headTruncate（保证 turn boundary）
```

**System Prompt 内容（不参与压缩）**：

```
# Move Context
Kind: {move.Kind}   Target: {move.Target.Display()}
Objective: {move.Objective}
Cues: {move.Cues}
Constraints: {move.Constraints}

# Current World State（Top-K 相关摘要）
{ledger.TopK(taskID, move.Target, K=min(30, ctx*10%))}
// 按与 move.Target 的相关性排序；完整 Detail 通过 read_landmark(id) 按需拉取

# Working Memory（本 Move 的临时假设）
{hypotheses}
```

### Planner Memory（战略记忆，跨 Move）

`PlannerState`（见 §4.10），schema 固定，Extensions 只允许预定义键。持久化在 `scan_state.planner_state`。

### Ledger Memory（中期，跨 Move）

- `Frontier()` 返回 confirmed 且无出边的 landmark，必须注入 PlanReq（通常 <20 个）
- `TopK(taskID, ref, K)` 返回按相关性排序的 Top-K Landmark.Summary，注入 PlanReq 和 Actor System Prompt
- `Landmark.Detail` 由 `read_landmark(id)` 按需拉取，不进任何系统提示
- Frontier 查询只返回 `confirmed` 节点，`hypothesized` 节点不进攻击前沿

### Corpus Memory（长期，跨任务）

- `search_corpus(host, category)` → 历史成功经验
- `write_corpus` → 任务结束 distill 写入
- 通过 `read_corpus` 工具注入 Actor，不通过系统提示全量展开

---

## 七、作战知识框架（Profile.SystemPrompt）

各 Profile 的 SystemPrompt 必须包含以下知识模块（内容从现有 agents/*.md 迁移整理）。这是架构落地的核心，不得省略。

### recon Profile（reconnaissance.md 迁移）

```
# 角色
探查员。摸清目标攻击面，产出结构化攻击面清单，不打洞不写 Finding。

# 工作流
1. 看已有流量（list_traffic / view_traffic）
2. 浏览器登录（高保真主来源）：各身份 browser_use，走核心业务流程
   登录成功 → 立即 write_credential（路 A 协议）
3. 爬虫补广度（katana + dirsearch + arjun，带认证凭据爬）
4. API 规范枚举（/openapi.json / /swagger.json → spectral lint）
5. 历史 URL（gau，仅公网目标）
6. 产出：endpoint × 漏洞方向的攻击面清单

# 凭证共享协议
{凭证协议全文，见 exploitation.md 迁移}

# 工具优先级
{流量字典语义 + replay_traffic 优先级}
```

### exploit Profile（exploitation.md 迁移）

```
# 角色
单攻击面突破手。接 Move.Objective 描述的具体攻击面，验证利用、写 Finding。

# 工作流
0. 判 brief 类型（evidence handoff → 直接复现；否则 → 深挖）
1. baseline 建立（确认目标可达 + 凭据有效）
2. 凭证拉取（curl 链路：read_credentials；浏览器链路：browser_use identity）
3. 工具优先级：replay_traffic → run_command → browser_use
4. 命中立即 write_finding，后续走 update_finding

# 写 finding 约束
- 真实命中（证据来自工具真实输出）
- 工具未失败（502/connection refused/exit≠0 视为未命中）
- 可复现（repro_cmd 完整，写入 Finding.Repro）
- 不重复（write 前调 read_findings）
- CWE 标准化（OS Command Injection→CWE-78，SQL Injection→CWE-89，…），写入 Finding.CWEID
- target.path 必填

# 请求重放 vs 浏览器选择策略
{分流逻辑全文}

# 凭证共享协议
{全文}
```

### scan / use_credential / post_exploit Profile

按 PTES 方法论编写，格式同上，此处省略（落地时补全）。

---

## 八、工具体系

### 按 MoveKind 分组

| Move.Kind | 可用 Tool |
|-----------|-----------|
| recon | run_command, read_landmark, write_landmark, read_corpus, list_traffic, view_traffic, replay_traffic, browser_use, write_credential, read_credentials, done |
| scan | run_command, read_landmark, write_landmark, write_weakness, read_corpus, list_traffic, view_traffic, done |
| exploit | run_command, read_landmark, write_landmark, write_weakness, write_finding, update_finding, read_findings, read_credentials, write_credential, replay_traffic, list_traffic, view_traffic, browser_use, read_corpus, write_corpus, write_hypothesis, done |
| use_credential | run_command, read_landmark, write_landmark, write_credential, read_credentials, replay_traffic, browser_use, read_corpus, write_hypothesis, done |
| post_exploit | run_command, read_landmark, write_landmark, write_credential, read_credentials, write_finding, read_findings, read_corpus, write_corpus, done |

`write_hypothesis` → 更新 Working Memory，纯状态更新，无 Sandbox 调用。

### Registry Interceptor 链

```
PreExecuteInterceptor        // ① Constraint 结构化检查（工具调用前，违规直接拒绝）
  → LoggingInterceptor
  → EvidenceCaptureInterceptor  // cmd_output 自动包装为 Signal（含 ToolName）
  → TimeoutInterceptor
  → SandboxInterceptor          // 走 SubEnv(moveID).Exec
  → Tool.Execute
  → ErrorMaskInterceptor        // 非 context.Canceled/DeadlineExceeded 的 error 转为
                                //   ToolResult.Error 字符串，ReAct 循环不中断
```

**并发工具派发**：LLM 单次返回多个 tool_call 时，Registry 调用 `ExecuteParallel` 并发派发所有调用，结果按原始顺序收集后统一返回给 LLM。

---

## 九、数据流（端到端）

```
用户/调度
  │
  ▼
Assignment（多 Target + Campaign）
  │  拆分：一 Target 一 Task，并发运行
  ▼
Task → Cognition.Run(taskID, campaign, target)
  │
  ├─ scan_state 检查（崩溃恢复：active → 从 checkpoint 恢复）
  ├─ Sandbox.Spawn(taskID)
  ├─ go TrafficIngester.Watch(ctx, sandbox.ProxyURL, ...)  // 并行于主循环
  │
  ├─→ Planner.Plan(...)                            // tier: planner
  │     输出：[]Move（含 DependsOn）
  │
  ├─→ topoSort(moves) → batches
  │
  ├─→ fanOut(batch, MaxConcurrentMoves):
  │     per Move（并发）:
  │       subSandbox = sandbox.SubEnv(move.ID)
  │       Dispatcher.Execute(ctx, move, subSandbox) → []Execution
  │         ├─ 按 Move.Kind 选 Profile
  │         ├─ 构建 ActorReq（System = Profile.Prompt + TopK summaries + hypotheses）
  │         └─ Actor.Run(req)
  │               每 Step：
  │                 Provider.CountTokens → 决定是否压缩
  │                 → compress → dedup → compact
  │                 → Provider.Complete / Stream    // tier: executor
  │                 → Registry.ExecuteParallel（并发工具派发）
  │                 → PreExecuteInterceptor（Constraint 检查）
  │                 → ErrorMaskInterceptor（工具错误转字符串）
  │                 → Step{Thought, Raw, ModelSeen, ToolCalls, Results, Hypotheses}
  │                 → checkpoint.Write(step)
  │                 → StreamEvent 推送（SSE）
  │                 → 每 CriticInterval 步 Critic.Evaluate  // tier: inspector
  │                 → [85% budget] SettleConfig 注入
  │
  ├─→ Verifier.Promote(move, executions)
  │     select best Execution (max Confidence，平局取最多 Step)
  │     Replayer.Replay(primitives) → ReplayResult
  │     RecordVerification（无论结果，审计链不断）
  │     confirmed → UpsertNode；refuted → (nil, nil)
  │     输出：*Landmark, *Finding
  │
  ├─→ ledger.Write（UPSERT ON CONFLICT (scan_id, kind, ref_key)，幂等）
  │   findings.Write（UPSERT ON CONFLICT (scan_id, cwe_id, target_ref)，幂等）
  │   move_record.Write（记录 Move 状态，供崩溃恢复）
  │
  └─→ history.Append(MoveRecord{Status, HaltWhy, Findings})
      → ScanBudget 检查 → 下一轮 Planner
      → 扫描结束 → campaign.PostScanHook(taskID)（生成报告）
```

### 被动流量数据流

```
TrafficIngester（goroutine，并行于 Cognition 主循环）
  监听 Sandbox.ProxyURL（MITM 代理事件流）
  → 构造 Signal{Kind: http_trace, ToolName: "mitmproxy"}
  → Verifier.Promote（直接晋升 endpoint/service Landmark，不经 Actor）
  → ledger.Write
  → Planner 下次 Plan() 可见
```

---

## 十、崩溃恢复

```
scan_state.status == 'active' ?
  是 → 查 move_record 找 status='in_flight' 的 Move
       从 actor_checkpoint 找该 Move 的最后 step_idx
       恢复 PlannerState 从 scan_state.planner_state
       恢复 history 从 move_record 表（status=done/abandoned/stalled）
       继续未完成的 Move（从 checkpoint 步继续）
  否 → 正常启动
```

幂等写入：
- `ledger.Write`：UPSERT ON CONFLICT (scan_id, kind, ref_key)
- `findings.Write`：UPSERT ON CONFLICT (scan_id, cwe_id, target_ref)
- `actor_checkpoint.Write`：INSERT，PRIMARY KEY 自然幂等
- `move_record.Write`：UPSERT ON CONFLICT (scan_id, move_id)

retry：最多 3 次，指数退避 1s/2s/4s。5xx 可重试；4xx、cancel 不重试。

---

## 十一、ExploreGraph（懒加载因果图）

不实时维护 explore_edges 表，从 Ledger + MoveRecord 动态计算。

**边推导规则**：

| rel | 推导规则 |
|---|---|
| `spawns` | Landmark → Move（Move.Target.Key == Landmark.Ref.Key） |
| `produces` | Move → Landmark（Landmark.MoveID == Move.ID） |
| `refutes` | Move → Landmark（Landmark.State == refuted AND Landmark.MoveID == Move.ID） |
| `promotes` | Landmark(weakness) → Finding（Finding.LandmarkID == Landmark.ID） |
| `derives` | Landmark → Landmark（Landmark.From 包含另一 Landmark.ID） |

```go
type ExploreGraph interface {
    Subgraph(ctx context.Context, taskID string) (*Graph, error)
    AttackPath(ctx context.Context, findingID string) ([]Node, []Edge, error)
    Frontier(ctx context.Context, taskID string) ([]Node, error)
    Goals(ctx context.Context, taskID string) ([]Node, error)
}
```

**goal Landmark**：`LandmarkKind = "goal"`，Cognition 启动时创建（hypothesized），Finding 达成目标时→confirmed，Scan 结束时未达成→refuted。

---

## 十二、前端实时反馈（SSE 流）

```
GET /api/scans/{taskID}/stream   Content-Type: text/event-stream

event: thinking
data: {"move_id":"m1","step":3,"content":"分析登录参数…"}

event: tool_start
data: {"move_id":"m1","step":3,"tool":"run_command","args":"…"}

event: tool_end
data: {"move_id":"m1","step":3,"tool":"run_command","signal_kind":"cmd_output"}

event: landmark
data: {"id":"lm-42","kind":"weakness","summary":"SQL 注入点：/api/login?username=…"}

event: finding
data: {"id":"f-7","severity":"high","title":"未授权 SQL 注入"}

event: move_done
data: {"move_id":"m1","status":"done","halt":"done"}
```

---

## 十三、调度层关系（Assignment / Task / Move）

```
Assignment（调度层，含多 Target）
  └── Task × len(Targets)（一 target 一 scan，并发运行）
        └── Cognition.Run（PAE 循环容器）
              └── Move × N（Planner 生成，含依赖关系）
                    └── Execution × M（Dispatcher 执行，Critic Steer 后可重试）
```

多 Scan 之间的知识共享走 Corpus，不走 Ledger。

---

## 十四、命名对照表

| 概念 | ARTEX | Cairn | CSAI | **本设计 v1.1** |
|------|-------|-------|------|----------------|
| 执行引擎 | `Session` | — | `RunResult` | **`Actor`** |
| 意图单元 | `Intent` | `Intent` | `Task` | **`Move`**（含 DependsOn） |
| 世界状态节点 | `Asset/Credential` | `Fact` | `ProjectFact` | **`Landmark`** |
| 报告产物 | `finding` | — | `EvidenceRefs` | **`Finding`** |
| 证据附件 | — | — | — | **`Signal`**（含 ToolName） |
| 执行步骤 | turn | — | `iteration` | **`Step`**（含 Hypotheses） |
| 进度评估 | — | — | — | **`Assessment`** |
| 执行环境 | proxy env | `Container` | local proc | **`Sandbox`**（含 SubEnv） |
| 停止原因 | budget/done | — | `CompletionReason` | **`HaltReason`**（含 cancelled） |
| 规划记忆 | `TodoStore` | — | `write_todos` | **`PlannerState`**（固定 schema） |
| 人工引导 | — | `Hint` | HITL | **`Directive`**（guidance/constraint/halt 三级） |
| Move 历史 | — | — | — | **`MoveRecord`** |
| Move 执行包 | — | — | — | **`Execution`**（含 Hypotheses，原 Attempt） |
| 前端策略 | — | — | — | **`Campaign`**（结构化 Constraint + PostScanHook） |
| Scan 总预算 | — | — | — | **`ScanBudget`**（含 MaxConcurrentMoves） |
| 战术工厂 | — | — | — | **`Dispatcher`**（含 Profile） |
| 临时假设 | — | — | — | **`Hypotheses`**（Working Memory） |
| 目标引用 | slug 字符串 | — | — | **`LandmarkRef`**（域无关三元组：Domain/RefKind/Locator） |
| 被动流量摄取器 | — | — | — | **`TrafficIngester`**（监听 MITM 代理，并行于主循环） |
