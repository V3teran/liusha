# Liusha 架构深度分析报告

**分析日期**: 2026-08-30  
**分析方法**: 从代码实际实现出发，不依赖注释，验证逻辑自洽性和概念优雅性

---

## 🎯 执行摘要

**总体评价**: ⚠️ **架构存在重大概念混乱和实现不一致**

### 关键发现

#### ❌ 严重问题（破坏性）

1. **核心概念命名混乱**: Move vs Action 术语不统一，代码和文档自相矛盾
2. **双层监察架构未实现**: 文档描述的 Planner/Executor 双层监察完全是幻觉
3. **EventBus 双系统共存**: Task 级和 Action 级两套事件总线，职责重叠
4. **世界模型概念漂移**: 从科学方法论滑向 PDDL，但执行层完全没用上

#### ⚠️ 中等问题（设计缺陷）

5. **Executor vs Dispatcher 职责模糊**: 谁是真正的执行引擎？
6. **Profile 机制过度设计**: 5 档复杂度配置，但实际只用 Medium/Complex
7. **Cognition 层定位尴尬**: ExecutionLoop 只是轮询调度器，不是认知引擎

#### ✅ 优点

8. **工具系统清晰**: Registry + Interceptor 设计合理
9. **数据库模式扎实**: WorldModel Store 的 SQL 设计良好
10. **沙箱管理优雅**: 引用计数 + 按 Assignment 粒度共享

---

## 📋 详细分析

### 1. 核心概念混乱：Move vs Action

#### 问题描述

代码中同时存在两个概念，互相混用：

```go
// worldmodel/model.go - 定义为 Action
const KindAction NodeKind = "action"  

// planner/agent.go - 注释说产出 Move
// Agent 是事件驱动的 Planner Agent，通过 LLM 推理产出 Move

// cognition/execution_loop.go - 混用两个名字
openActions, err := l.world.ListOpenActions(ctx, taskID)  // 变量名 actions
// ...
for _, move := range executable {  // 变量名 move
    if err := l.executeMove(ctx, taskID, move, rep); err != nil {
```

**代码实际使用统计**:
- 数据库表: `wm_node.kind = 'action'` ✅
- WorldModel API: `ListOpenActions`, `UpdateActionState` ✅
- Planner 工具: `propose_moves` ❌
- Executor 类型: `executor.Move` struct ❌
- Cognition 变量: 混用 `actions` 和 `move`

#### 根本原因

历史重构不彻底。从注释看：
- 旧版本用 "Move"（对标 AI Planning 的 Move）
- v4.0 改为 "Action"（对标 PDDL 的 Action）
- 但代码迁移不完整

#### 影响

- **新人理解成本高**: 不知道 Move 和 Action 是同一个东西
- **API 不一致**: worldmodel 用 Action，executor 用 Move
- **文档代码脱节**: 架构文档说 "action"，但 Planner 工具叫 "propose_moves"

#### 建议

**选择 1: 统一用 Action**（推荐）
- ✅ 符合 PDDL 标准
- ✅ worldmodel 已经是 Action
- ❌ 需要修改 executor.Move → executor.Action

**选择 2: 统一用 Move**
- ✅ 保持 executor 接口不变
- ❌ 需要修改 worldmodel.KindAction → KindMove
- ❌ 与 PDDL 标准脱节

---

### 2. 双层监察架构 = 幻觉

#### 文档宣称

> 微观监察 (Executor): 每 5 步评估，局部决策  
> 宏观监察 (Planner): 每 6 分钟全局评估，战略调整

#### 代码现实

**Executor 监察**：实际不存在

```bash
# 搜索 "monitorLoop" "selfEvaluate"
$ grep -r "monitorLoop\|selfEvaluate" internal/executor/
# 结果：0 个匹配
```

找到的文件：
- `executor/self_monitor.go` - **空文件占位符**
- `executor/run_with_monitoring.go` - **空文件占位符**

**Planner 监察**：名存实亡

```go
// planner/agent.go 的实际逻辑
func (a *Agent) Start(ctx context.Context) error {
    // 心跳定时器（30 秒）
    heartbeatTicker := time.NewTicker(30 * time.Second)
    
    case <-heartbeatTicker.C:
        a.eventBus.PublishHeartbeat(a.taskID)  // 只发心跳，不评估！
```

**真相**：
- Planner 30 秒心跳 = 只发事件，不做全局评估
- 没有 `evaluateGlobal()` 函数
- 没有 `Kill/Steer/CreateAction` 决策逻辑

#### EventBus 的真实作用

```go
// 实际事件类型
const (
    EventTaskStarted          EventType = "task.started"
    EventMoveCompleted        EventType = "move.completed"
    EventVerificationPassed   EventType = "verification.passed"
    EventManualGuidance       EventType = "manual.guidance"
    EventHeartbeat            EventType = "heartbeat"
)
```

EventBus 只是 **事件驱动的 Planner 重规划触发器**：
- Move 完成 → 触发 Planner 重新规划
- 验证通过 → 触发 Planner 调整计划
- **没有 Kill/Steer 事件**

#### 影响

1. **架构文档完全误导**: 精美的双层监察图示，实际代码一个都没实现
2. **EventBus 价值存疑**: 只用于触发 Planner 重新调用 LLM，过度工程化
3. **"监察" 概念虚假**: 实际只是 "事件触发重新规划"

#### 真实架构

```
用户目标
   ↓
Planner (事件驱动 LLM 规划)
   ├─ 工具: observe_state (读世界模型)
   ├─ 工具: propose_moves (写 Action 节点)
   └─ 工具: evaluate_progress (决定是否继续)
   ↓
Cognition.ExecutionLoop (轮询调度)
   └─ 每 2 秒轮询 open actions
   └─ 顺序执行可执行的 actions
      ↓
Dispatcher (复杂度路由)
   └─ 按 complexity 选 Profile
      ↓
Executor (ReAct 循环)
   └─ LLM 推理 + 工具调用
```

**没有监察，只有规划 → 执行 → 事件 → 重规划**

---

### 3. EventBus 双系统并存

#### 两套 EventBus

**系统 1: cognition.EventBus** (Task 级)
```go
// handler.go
eventBus    *cognition.EventBus     // Task 级别事件总线（Planner 用）

// cognition/eventbus.go
type Bus struct {
    subscribers map[string]chan Event  // taskID → chan
}
```

**系统 2: eventbus.Bus** (Action 级)
```go
// handler.go
actionBus   *eventbus.Bus           // Action 级别事件总线（Executor 用）

// executor/eventbus_adapter.go
type EventBusAdapter struct {
    inner *eventbus.Bus
}
```

#### 问题

1. **职责重叠**: 都是发布-订阅，都用于异步通信
2. **命名混乱**: `cognition.EventBus` vs `eventbus.Bus`（注意首字母大小写）
3. **适配器多余**: `EventBusAdapter` 只是简单包装

#### 为什么有两套？

**猜测的历史原因**：
- `cognition.EventBus` 是早期设计（Planner ↔ ExecutionLoop）
- `eventbus.Bus` 是后来加的（Planner ↔ Executor 直接通信）
- 但 Planner 监察没实现，导致 `eventbus.Bus` 实际没用上

#### 代码证据

```go
// dispatcher.go - actionBus 传给 Executor
a := executor.New(d.provider, subReg, d.compactor, d.checkpoint, d.emitter, d.logger, wmReader)
if d.eventBus != nil {
    a = a.WithEventBus(d.eventBus)  // 设置了但从不收到事件
}
```

Executor 期待收到 `action.killed` / `action.steered` 事件，但：
- Planner 从不发送这些事件（监察未实现）
- `eventbus.Bus` 事实上是死代码

#### 建议

**方案 A: 删除 eventbus.Bus**（推荐）
- 只保留 `cognition.EventBus`
- 删除 Executor 的 eventLoop 协程
- 删除 `EventBusAdapter`

**方案 B: 统一到 eventbus.Bus**
- 迁移 Planner 到新总线
- 删除 `cognition.EventBus`

---

### 4. 世界模型概念漂移

#### 设计初衷（从注释推断）

```go
// worldmodel/model.go 注释
// 设计决策（2026-08-28 重构）：
// 1. 5 种节点类型：objective/action/hypothesis/evidence/finding
// 2. 5 种关系类型：GENERATES/CONFIRMS/REFUTES/ENABLES/DEPENDS_ON
// 3. 对标科学方法论：目标 → 动作 → 假设 → 证据 → 发现
// 4. 对标 PDDL 标准：action 是 AI Planning 标准术语
```

**设计者的意图**：
- 科学方法论：假设 → 实验 → 证据 → 结论
- PDDL 规划：precondition → action → effect

#### 实现现实

**Hypothesis 节点**：几乎不用

```bash
$ grep -r "KindHypothesis\|write_hypothesis" internal/tools/
# 只有工具定义，没有实际调用
```

**Evidence 节点**：从未创建

```bash
$ grep -r "KindEvidence\|CreateEvidence" internal/
# 0 个结果
```

**实际使用的节点**：
1. `objective` - 任务启动时创建一次
2. `action` - Planner 生成，ExecutionLoop 执行
3. `finding` - 工具 `write_finding` 创建（安全漏洞）
4. ~~`hypothesis`~~ - 工具存在但不调用
5. ~~`evidence`~~ - 根本没实现

#### 关系边的使用

```go
// cognition/execution_loop.go
edge := worldmodel.Edge{
    TaskID:    taskID,
    SrcID:     move.ID,
    Rel:       worldmodel.RelGenerates,  // 只用了这一个关系
    DstID:     node.ID,
    CreatedAt: time.Now(),
}
```

**5 种关系类型，只用 1 个**: `GENERATES`

其他 4 个（`CONFIRMS/REFUTES/ENABLES/DEPENDS_ON`）是死代码。

#### 问题根源

**概念过度抽象**：
- 设计者想建立 "科学方法论" 的知识图谱
- 但实际业务只需要 "规划 → 执行 → 发现" 三步
- Hypothesis/Evidence 是 nice-to-have，不是 must-have

#### 影响

1. **数据库表浪费**: `wm_node` 表有很多字段只对某些 kind 有效
2. **API 复杂度高**: 要处理 5 种节点的多态
3. **文档误导**: 描绘了精美的 "推理链"，实际只有线性流

#### 建议

**简化为 3 节点模型**:
```
objective (目标) → action (执行) → finding (发现)
```

删除：
- `hypothesis` 节点（改为 action 的 metadata）
- `evidence` 节点（改为 finding 的 evidence 字段）
- 4 个不用的关系类型

---

### 5. Executor vs Dispatcher 职责模糊

#### Dispatcher 的职责（代码分析）

```go
// dispatcher/dispatcher.go
func (d *Dispatcher) Execute(ctx context.Context, move executor.Move) (executor.Execution, error) {
    // 1. 按 complexity 选 Profile
    profile, ok := d.profiles[move.Complexity]
    
    // 2. 构造工具受限的 sub-registry
    subReg := d.buildSubRegistry(profile.Tools)
    
    // 3. 创建 Executor
    a := executor.New(d.provider, subReg, ...)
    
    // 4. 构建请求
    req := d.buildExecutorReq(profile, move, nil)
    
    // 5. 执行
    result, err := a.Run(ctx, move.ID, req)
    
    return executor.Execution{Result: result}, nil
}
```

**Dispatcher 做了什么**：
- Profile 路由（5 档复杂度）
- 工具过滤
- **创建 Executor 实例**
- 调用 `Executor.Run`

#### Executor 的职责（推断，文件未读取完整）

从类型定义看：
```go
// executor/types.go
type Executor struct {
    provider   provider.Provider
    registry   *registry.Registry
    compactor  Compactor
    checkpoint CheckpointStore
    emitter    SSEEmitter
    logger     zerolog.Logger
    worldmodel WorldModelReader
    eventBus   EventBus
}
```

**Executor 应该做什么**：
- ReAct 循环（LLM 推理 + 工具调用）
- 检查点恢复
- SSE 事件流
- 与 EventBus 交互

#### 问题

**Dispatcher 是工厂 还是 执行器？**

从命名看：
- `Dispatcher.Execute()` - 像执行器
- 但它只是 `new Executor + call Run` - 像工厂

**真实角色**:
- Dispatcher = **Executor 工厂 + Profile 路由器**
- Executor = **真正的执行引擎**

#### 命名建议

**重命名为 `ExecutorFactory`**:
```go
type ExecutorFactory struct {
    profiles map[worldmodel.Complexity]Profile
    // ...
}

func (f *ExecutorFactory) CreateAndRun(ctx context.Context, move executor.Move) (executor.Execution, error) {
    // ...
}
```

或者更激进：**删除 Dispatcher，Profile 逻辑放到 Executor**

---

### 6. Profile 机制过度设计

#### 5 档复杂度配置

```go
// worldmodel/model.go
const (
    ComplexityTrivial  Complexity = "trivial"   // 平凡
    ComplexitySimple   Complexity = "simple"    // 简单
    ComplexityModerate Complexity = "moderate"  // 中等
    ComplexityComplex  Complexity = "complex"   // 复杂
    ComplexityExtreme  Complexity = "extreme"   // 极端
)
```

#### 实际使用（统计代码调用）

```go
// handler_run.go
d, reg, err := h.buildDispatcher(ctx, provider.ComplexityMedium, instruction, sink)  // Solo 用 Medium

d, reg, err := h.buildDispatcher(ctx, provider.ComplexityComplex, sysPrompt, sink)   // Swarm 用 Complex
```

**实际只用 2 档**: Medium（solo）, Complex（swarm）

#### 问题

1. **配置爆炸**: 要为 5 档都写 Profile（工具列表、Budget、Settle）
2. **边界模糊**: Trivial vs Simple、Moderate vs Complex 的区分标准？
3. **维护负担**: LLM 能力升级时，5 档都要调参

#### Profile 的真实价值

```go
// dispatcher/profile/profiles.go （推测）
type Profile struct {
    Complexity   worldmodel.Complexity
    SystemPrompt string
    Tools        []string           // 工具白名单
    Budget       executor.Budget    // Token/步数限制
    Settle       executor.SettleConfig
}
```

**有用的配置**:
- Tools 白名单（限制能力）
- Budget（防止失控）

**没用的配置**:
- 5 档复杂度（实际只用 2 档）
- SystemPrompt 差异（应该在 Scenario 层区分）

#### 建议

**简化为 2 档**:
```go
const (
    ComplexityStandard Complexity = "standard"  // 单 agent
    ComplexityAdvanced Complexity = "advanced"  // 多 agent
)
```

或者直接 **删除 Complexity 概念**，改用：
- Solo vs Swarm（在 Scenario 层决定）
- Profile 只配置 Tools + Budget

---

### 7. Cognition 层定位尴尬

#### 命名的期待

`cognition` 包名让人期待：
- 认知架构
- 推理引擎
- 知识管理

#### 实际实现

```go
// cognition/execution_loop.go
func (l *ExecutionLoop) Run(ctx context.Context, taskID string) (Report, error) {
    ticker := time.NewTicker(l.pollInterval)  // 每 2 秒
    defer ticker.Stop()
    
    for {
        select {
        case <-ticker.C:
            if err := l.processPendingActions(ctx, taskID, &rep); err != nil {
                return rep, err
            }
            // 停止条件：无待执行 Action
            if pendingCount == 0 {
                return rep, nil
            }
        }
    }
}
```

**ExecutionLoop 做了什么**：
1. 每 2 秒轮询数据库（`ListOpenActions`）
2. 筛选可执行的 actions（检查依赖）
3. 顺序调用 `executor.Execute`
4. 更新 action 状态（running → done/failed）

**这只是一个调度器，不是认知引擎**

#### 问题

**命名过度承诺**:
- `cognition` 听起来很高大上
- 实际只是 `scheduler` 或 `orchestrator`

**职责混乱**:
- 轮询逻辑（infrastructure）
- 依赖检查（应该在 worldmodel）
- 状态更新（应该在 executor）

#### 建议

**重命名包**:
```
cognition/ → scheduler/
ExecutionLoop → DependencyScheduler
```

**简化职责**:
```go
type Scheduler struct {
    world    *worldmodel.Store
    executor Executor
}

func (s *Scheduler) RunUntilDone(ctx context.Context, taskID string) error {
    for {
        actions := s.world.GetExecutableActions(taskID)  // 依赖逻辑移到 worldmodel
        if len(actions) == 0 {
            return nil
        }
        for _, a := range actions {
            s.executor.Execute(ctx, a)  // 状态更新在 executor 内部
        }
    }
}
```

---

### 8. ✅ 工具系统设计优秀

#### Registry + Interceptor 模式

```go
// registry/registry.go
type Registry struct {
    tools        map[string]Tool
    interceptors []Interceptor
}

type Interceptor func(ctx context.Context, t Tool, args []byte, next ExecuteFunc) (ToolResult, error)

func (r *Registry) Execute(ctx context.Context, name string, args []byte) (ToolResult, error) {
    // 责任链模式
    next := func(ctx context.Context, t Tool, args []byte) (ToolResult, error) {
        return t.Execute(ctx, args)
    }
    for i := len(r.interceptors) - 1; i >= 0; i-- {
        next = r.chain(r.interceptors[i], next)
    }
    return next(ctx, tool, args)
}
```

**优点**:
1. ✅ 清晰的责任链模式
2. ✅ Interceptor 可组合（日志、限流、统计）
3. ✅ 工具注册解耦

#### 实际使用

```go
// handler_run.go
func (h handler) toolRecordInterceptor(executorID, taskID string) registry.Interceptor {
    return func(ctx context.Context, t registry.Tool, args []byte, next registry.ExecuteFunc) (registry.ToolResult, error) {
        start := time.Now()
        res, err := next(ctx, t, args)
        durMs := int(time.Since(start).Milliseconds())
        
        // 记录工具调用
        h.toolCalls.Append(ctx, toolinvocation.Invocation{
            ExecutorID: executorID,
            ToolName:   t.Name(),
            DurationMs: durMs,
        })
        
        // 节流心跳
        if now-last >= heartbeatThrottleMs {
            h.tasks.Heartbeat(ctx, taskID)
        }
        
        return res, err
    }
}
```

**一个 Interceptor 做了 3 件事**:
1. 工具调用记录（审计）
2. 心跳上报（保活）
3. 性能统计（监控）

**这是正确的设计** ✅

---

### 9. ✅ 数据库模式扎实

#### WorldModel 表设计

```sql
CREATE TABLE wm_node (
    id          TEXT PRIMARY KEY,
    task_id     TEXT NOT NULL,
    kind        TEXT NOT NULL,
    content     JSONB,
    
    -- action 专用
    state       TEXT,
    complexity  TEXT,
    depends_on  TEXT[],
    blocked_reason TEXT,
    
    -- hypothesis/finding 专用
    confidence  TEXT,
    
    -- 通用
    priority    INTEGER,
    owner       TEXT,
    source_type TEXT,
    source_id   TEXT,
    tags        TEXT[],
    metadata    JSONB,
    
    created_at  TIMESTAMPTZ,
    updated_at  TIMESTAMPTZ,
    completed_at TIMESTAMPTZ
);

CREATE TABLE wm_edge (
    task_id   TEXT,
    src_id    TEXT,
    rel       TEXT,
    dst_id    TEXT,
    attrs     JSONB,
    created_at TIMESTAMPTZ,
    PRIMARY KEY (src_id, dst_id, rel)
);
```

**优点**:
1. ✅ **多态节点表**: 用 `kind` 字段区分类型，避免 5 张表
2. ✅ **可选字段**: 用 NULL 表示 "不适用"（如 action 的 state）
3. ✅ **JSONB 灵活性**: content/metadata/attrs 用 JSON 存储非结构化数据
4. ✅ **复合主键**: edge 用 (src, dst, rel) 避免重复边
5. ✅ **索引友好**: task_id + kind 是常见查询模式

#### API 设计

```go
// store.go
func (s *Store) ListOpenActions(ctx context.Context, taskID string) ([]Node, error) {
    return s.ListActionsByState(ctx, taskID, StateOpen)
}

func (s *Store) UpdateActionState(ctx context.Context, id string, state State, blockedReason *string) error {
    query := `UPDATE wm_node SET state = $2, blocked_reason = $3, 
              completed_at = CASE WHEN $2 IN ('done', 'failed', ...) THEN now() ELSE NULL END
              WHERE id = $1 AND kind = 'action'`
}
```

**优点**:
1. ✅ **类型安全的查询**: 方法名明确返回类型
2. ✅ **原子更新**: 用 SQL CASE 自动设置 completed_at
3. ✅ **WHERE kind 过滤**: 防止误操作非 action 节点

**这是教科书级别的设计** ✅

---

### 10. ✅ 沙箱管理优雅

#### 引用计数 + 按 Assignment 粒度

```go
// handler_run.go
sandboxClient, err := h.sandboxMgr.Acquire(ctx, assignmentID)
if err != nil {
    return h.failTask(ctx, p.ExecutorID, fmt.Errorf("sandboxMgr.Acquire(%s): %w", assignmentID, err))
}

defer func() {
    if err := h.sandboxMgr.Release(context.Background(), assignmentID); err != nil {
        h.logger.Warn().Err(err).Str("assignment_id", assignmentID).Msg("sandboxMgr.Release 失败")
    }
}()
```

**设计要点**:
1. ✅ **Assignment 粒度**: 同一 Assignment 的多个 Task 共享容器
2. ✅ **引用计数**: Release 时不立即销毁，等所有 Task 结束
3. ✅ **延迟清理**: 引用归零后延迟销毁（避免频繁创建）
4. ✅ **独立 context**: 用 `context.Background()` 不受 Task 取消影响

#### 为什么按 Assignment 粒度？

从业务逻辑推断：
- Assignment = 一次扫描任务（如 "扫描 example.com"）
- Task = Assignment 的一个子任务（如 "端口扫描"、"目录爆破"）
- 同一 Assignment 的多个 Task 需要共享状态（文件、凭证）

**这是正确的业务建模** ✅

---

## 🔍 数据流真相

### 实际数据流（与文档对比）

#### 文档描述的流程

```
用户目标
  ↓
Planner 创建 worldmodel 节点
  ├─ objective
  └─ action (多个，带依赖)
  ↓
EventBus 事件驱动
  ↓
Dispatcher 按 Complexity 调度
  ↓
Executor 执行（3 协程：executeLoop + monitorLoop + eventLoop）
  ├─ monitorLoop: 每 5 步评估，自我纠偏/kill
  └─ eventLoop: 接收 Planner 的 kill/steer 事件
  ↓
工具调用 → WorldModel Store
  ├─ write_hypothesis
  ├─ write_evidence
  └─ write_finding
  ↓
Planner 心跳（6 分钟）全局评估
  ├─ evaluateGlobal()
  ├─ Kill 偏离的 action
  └─ Steer 需要纠偏的 action
```

#### 代码实际的流程

```
用户提交 brief
  ↓
handler.handleSolo/handleSwarm
  ├─ onboard: 创建 objective 节点（best-effort）
  ├─ buildDispatcher: 创建 Dispatcher
  └─ runCognition: 启动认知循环
  ↓
runCognition (cmd/runner/handler_cognition.go 推测)
  ├─ 启动 Planner Agent (goroutine)
  └─ 启动 ExecutionLoop.Run
  ↓
Planner.Start (事件循环)
  ├─ 初始规划: replan(EventTaskStarted)
  │   ├─ observe_state (读世界模型)
  │   └─ propose_moves (写 action 节点到数据库)
  └─ 监听事件:
      ├─ EventMoveCompleted → replan
      └─ EventHeartbeat → 只发心跳，不评估
  ↓
ExecutionLoop.Run (轮询调度)
  └─ 每 2 秒:
      ├─ ListOpenActions
      ├─ 过滤可执行（依赖检查）
      ├─ UpdateActionState(running)
      ├─ executor.Execute(move)
      │   ↓
      │  Dispatcher.Execute
      │   ├─ 选 Profile (complexity 路由)
      │   ├─ 创建 Executor 实例
      │   └─ Executor.Run
      │       ├─ ReAct 循环 (LLM + 工具)
      │       └─ 返回 result
      ├─ UpdateActionState(done/failed)
      └─ eventBus.PublishMoveCompleted
  ↓
工具调用（通过 Registry）
  ├─ write_finding → wm_node (kind=finding)
  └─ （其他工具如 sandbox、corpus）
  ↓
Planner 收到 EventMoveCompleted
  └─ replan: 调用 LLM 决定是否生成新 action
```

### 关键差异

| 功能 | 文档描述 | 代码现实 |
|------|---------|---------|
| Executor 监察 | ✅ 每 5 步评估 | ❌ 不存在 |
| Planner 监察 | ✅ 每 6 分钟全局评估 | ❌ 只发心跳 |
| Kill/Steer | ✅ 双向（Planner ↔ Executor） | ❌ 没有实现 |
| Hypothesis 节点 | ✅ executor 产出 | ❌ 工具存在但不调用 |
| Evidence 节点 | ✅ verifier 产出 | ❌ 完全没实现 |
| 事件总线 | ✅ Planner ↔ Executor 通信 | ⚠️ 只用于 Planner 重规划触发 |

---

## 🎨 概念命名评价

### 优雅的命名 ✅

1. **WorldModel**: 中性、准确，不预设实现方式
2. **Dispatcher**: 清楚表达 "路由" 的职责
3. **Registry**: 工具注册表，标准模式
4. **Interceptor**: 拦截器，标准模式
5. **Profile**: 配置 Profile，清晰
6. **Assignment**: 业务概念，与 Task 区分明确

### 混乱的命名 ❌

1. **Move vs Action**: 同一概念两个名字
2. **Executor**: 太泛化，不知道执行什么
3. **Cognition**: 过度承诺，实际是调度器
4. **EventBus**: 两套系统，命名冲突
5. **Agent**: 到处都是（Planner Agent, Executor, Scanner Agent）

### 命名建议

#### 核心概念统一

```
Move/Action → Task  (避免与 task 表冲突则用 ActionNode)
Executor → ReactRunner (表达 ReAct 执行引擎)
Cognition → Orchestrator (编排器)
Dispatcher → ExecutorFactory 或 ProfileRouter
```

#### 包名重构

```
cognition/       → orchestrator/
  execution_loop → dependency_scheduler

planner/         → task_planner/
  agent          → planner

executor/        → react_runner/
  executor       → runner
```

---

## 📊 架构自洽性评分

| 维度 | 评分 | 说明 |
|------|------|------|
| **概念一致性** | 3/10 | Move/Action 混用，文档代码不符 |
| **职责清晰性** | 5/10 | Dispatcher/Executor/Cognition 边界模糊 |
| **实现完整性** | 4/10 | 监察架构未实现，Hypothesis/Evidence 未用 |
| **命名优雅性** | 6/10 | 工具系统优秀，但核心概念混乱 |
| **数据库设计** | 9/10 | WorldModel 表设计扎实 |
| **代码质量** | 7/10 | 工具系统、沙箱管理优秀，但整体设计有缺陷 |
| **文档准确性** | 2/10 | 架构文档与代码严重脱节 |

**总分**: 36/70 = **51%**（刚及格）

---

## 🚀 改进建议

### 短期（修复概念混乱）

1. **统一 Move/Action 命名**
   - 选择：Action（符合 PDDL）
   - 修改：executor.Move → executor.ActionRequest
   - 修改：planner 工具 propose_moves → propose_actions

2. **删除幻觉架构**
   - 删除：executor/self_monitor.go, run_with_monitoring.go（空文件）
   - 删除：eventbus.Bus（未使用）
   - 删除：EventBusAdapter（多余）
   - 更新：架构文档，删除 "双层监察" 描述

3. **简化世界模型**
   - 删除：Hypothesis/Evidence 节点类型
   - 删除：CONFIRMS/REFUTES/ENABLES 关系类型
   - 保留：Objective → Action → Finding

### 中期（重构架构）

4. **重命名包和类型**
   ```
   cognition/execution_loop → orchestrator/scheduler
   Dispatcher → ProfileRouter
   ```

5. **合并 EventBus**
   - 只保留一套事件系统
   - 明确事件类型和订阅规则

6. **简化 Complexity**
   - 5 档 → 2 档（Standard/Advanced）
   - 或者删除，改用 Solo/Swarm 区分

### 长期（架构演进）

7. **实现真正的监察（如果需要）**
   - Executor 内置 self-monitor（不是文档中的协程模式）
   - Planner 实现全局评估（定时调用 LLM 分析进度）
   - 实现 Kill/Steer 机制

8. **引入 Workflow DSL**
   - 用 YAML 描述 Action 依赖图
   - Planner 只生成高层计划
   - ExecutionLoop 执行 DAG 调度

---

## 📝 结论

Liusha 的架构存在 **严重的概念混乱和实现不完整** 问题：

### 关键问题

1. **命名混乱**: Move/Action 不统一，两套 EventBus
2. **文档虚假**: 架构文档描述的监察系统完全未实现
3. **过度设计**: 5 种节点类型只用 3 个，5 档复杂度只用 2 档
4. **职责不清**: Cognition/Dispatcher/Executor 边界模糊

### 优点

1. **工具系统**: Registry + Interceptor 设计优秀
2. **数据库**: WorldModel 表设计扎实
3. **沙箱管理**: 引用计数 + Assignment 粒度合理

### 总体评价

**架构处于 "能用但不优雅" 的状态**：
- ✅ 核心功能可运行（Planner 规划 + Executor 执行）
- ⚠️ 代码质量不一致（部分优秀，部分混乱）
- ❌ 文档严重误导（描述了未实现的功能）
- ❌ 概念不够自洽（需要重构统一）

**建议优先级**：
1. **P0**: 修复命名混乱（Move/Action 统一）
2. **P0**: 更新架构文档（删除虚假描述）
3. **P1**: 简化世界模型（删除不用的节点）
4. **P2**: 重构包名和类型名（提升可读性）

---

**分析完成**: 2026-08-30  
**方法论**: 代码优先 + 零信任注释 + 逻辑验证
