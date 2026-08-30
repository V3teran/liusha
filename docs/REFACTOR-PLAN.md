# Liusha 架构改进方案

**制定日期**: 2026-08-30  
**目标**: 统一概念命名，消除老架构残留，实现文档描述的完整双层监察架构

---

## 🎯 改进目标

1. **统一 Move/Action 命名** - 全面改为 `action`
2. **实现双层监察架构** - Executor 微观监察 + Planner 宏观监察
3. **明确两套 EventBus 的职责** - 区分并正确使用
4. **实现完整的世界模型** - 5 种节点 + 5 种关系的全量使用
5. **消除老架构残留** - 清理 cognition 层的定位问题

---

## 📋 架构真相分析

### 1. cognition.Executor vs executor.Executor 的关系

**真相**：两者是不同层次的抽象

```
┌─────────────────────────────────────────────┐
│  cognition.Executor (interface)             │
│  - 输入: worldmodel.Node                     │
│  - 输出: []verifier.Attempt                  │
│  - 职责: 执行一个 action，产出待验证的尝试   │
└─────────────────────────────────────────────┘
                    ↑ 实现
                    │
┌─────────────────────────────────────────────┐
│  domain/web.Executor (struct)               │
│  - 实现: cognition.Executor 接口             │
│  - 职责: 域适配层，调用战术 agent            │
│  - 流程:                                     │
│    1. 快照运行前的 finding                   │
│    2. 调用 AgentFunc (= dispatcher.Execute) │
│    3. 收割新 finding 转 Attempt              │
└─────────────────────────────────────────────┘
                    │ 调用
                    ↓
┌─────────────────────────────────────────────┐
│  executor.Executor (struct)                 │
│  - 职责: ReAct 执行引擎                      │
│  - 流程: LLM 推理 → 工具调用 → 循环          │
│  - 特性: 带监察、EventBus、Checkpoint        │
└─────────────────────────────────────────────┘
```

**结论**：
- `cognition.Executor` 是**域无关接口**（战略层）
- `domain/web.Executor` 是**域适配器**（战术层）
- `executor.Executor` 是**ReAct 引擎**（执行层）

**这不是老架构残留，是正确的分层设计！**

---

### 2. 两套 EventBus 的真相

#### EventBus #1: cognition.EventBus (Task 级)

**位置**: `internal/cognition/event.go`  
**用途**: Planner 重规划触发器  
**事件类型**:
```go
EventMoveCompleted       // Move 完成 → 触发 Planner 重规划
EventFindingDiscovered   // 发现漏洞 → 触发 Planner 调整
EventVerificationPassed  // 验证通过 → 触发 Planner 评估
EventManualGuidance      // 人工干预 → 触发 Planner
EventTaskStarted         // 任务启动 → 触发初始规划
EventHeartbeat           // 心跳 → 触发定期检查
```

**订阅者**: `planner.Agent`  
**发布者**: `cognition.ExecutionLoop`

**职责**: 驱动 Planner 的事件驱动规划循环

---

#### EventBus #2: eventbus.Bus (Action 级)

**位置**: `internal/eventbus/eventbus.go`  
**用途**: Planner ↔ Executor 监察通信  
**事件类型**:
```go
EventActionKilled      // Planner 终止 action → Executor 停止执行
EventActionSteered     // Planner 纠偏 action → Executor 注入消息
EventActionCompleted   // Executor 完成 action → Planner 感知
```

**订阅者**: `executor.Executor` (通过 eventLoop 协程)  
**发布者**: `planner.Agent` (通过 Kill/Steer 决策)

**职责**: 实现双层监察架构的通信基础

---

**结论**: **两套 EventBus 都是新架构核心，职责不重叠！**

- `cognition.EventBus` = Planner 的**输入触发器**（被动响应）
- `eventbus.Bus` = 监察架构的**控制信道**（主动干预）

---

### 3. 世界模型节点和关系的使用现状

#### 5 种节点类型

| 节点类型 | 当前状态 | 创建者 | 使用情况 |
|---------|---------|--------|---------|
| `objective` | ✅ 使用 | handler.onboard | 任务启动时创建 |
| `action` | ✅ 使用 | planner.Agent (propose_moves) | Planner 生成 |
| `hypothesis` | ❌ 未使用 | - | **工具不存在** |
| `evidence` | ❌ 未使用 | - | **工具不存在** |
| `finding` | ✅ 使用 | write_finding 工具 | Executor 写入 |

#### 5 种关系类型

| 关系类型 | 当前状态 | 连接 | 使用情况 |
|---------|---------|------|---------|
| `GENERATES` | ✅ 使用 | action → finding | ExecutionLoop 创建 |
| `CONFIRMS` | ❌ 未使用 | evidence → finding | **无 evidence 节点** |
| `REFUTES` | ❌ 未使用 | evidence → hypothesis | **无 evidence/hypothesis** |
| `ENABLES` | ❌ 未使用 | finding → action | **未实现** |
| `DEPENDS_ON` | ✅ 使用 | action → action | Planner 生成依赖时使用 |

**结论**: 需要实现 `hypothesis` 和 `evidence` 节点及其工具，以及 `ENABLES` 关系。

---

### 4. Dispatcher 的真实职责

从代码分析：

```go
// dispatcher/dispatcher.go
func (d *Dispatcher) Execute(ctx context.Context, move executor.Move) (executor.Execution, error) {
    // 1. 按 complexity 选 Profile
    profile, ok := d.profiles[move.Complexity]
    
    // 2. 构造工具受限的 sub-registry
    subReg := d.buildSubRegistry(profile.Tools)
    
    // 3. 创建 Executor
    a := executor.New(d.provider, subReg, ...)
    
    // 4. 执行
    result, err := a.Run(ctx, move.ID, req)
    
    return executor.Execution{Result: result}, nil
}
```

**Dispatcher 的职责**：
1. **Profile 路由** - 根据 complexity 选择配置
2. **工具过滤** - 限制 Executor 可用的工具
3. **Executor 工厂** - 每次创建新实例
4. **参数转换** - Move → ExecutorReq

**命名问题**: "Dispatcher" 听起来像调度器，但实际是**配置工厂 + 执行入口**

**结论**: Dispatcher 是新架构的**关键组件**，不是老架构残留。作用是隔离 Planner（规划层）和 Executor（执行层）。

---

### 5. Profile 机制的 5 档配置

从 `internal/dispatcher/profile/profiles.go` 分析：

| Complexity | 步数 | Token | 工具数 | 适用场景 |
|-----------|------|-------|--------|---------|
| Trivial | <5 步 | 10K | 5 个 | 快速查询、单次工具调用 |
| Simple | ~10 步 | 30K | 8 个 | 基础信息收集、简单枚举 |
| Moderate | ~30 步 | 80K | 16 个 | 漏洞测试、流量重放 |
| Complex | ~50 步 | 120K | 18 个 | 漏洞利用、深度分析 |
| Extreme | ~100 步 | 200K | 全部 | 提权、横移、复杂攻击链 |

**配置差异**：
- **工具白名单**：从 5 个到全部（渐进授权）
- **步数限制**：从 5 到 100（资源控制）
- **Token 预算**：从 10K 到 200K（成本控制）
- **系统提示**：每档有不同的战术指导

**实际使用**：
- Solo 模式固定用 `Medium`（应该根据任务动态分配）
- Swarm 模式固定用 `Complex`（应该根据子任务分配）

**结论**: 5 档配置不是过度设计，而是**细粒度资源控制**。问题是**使用方式固定化**，没有充分利用。

---

### 6. Cognition 层的定位

从代码分析：

```go
// cognition/execution_loop.go
func (l *ExecutionLoop) Run(ctx context.Context, taskID string) (Report, error) {
    // 每 2 秒轮询
    ticker := time.NewTicker(l.pollInterval)
    
    for {
        // 获取 open actions
        openActions, err := l.world.ListOpenActions(ctx, taskID)
        
        // 过滤可执行（依赖检查）
        for _, m := range openActions {
            if m.CanExecute(completed) {
                executable = append(executable, m)
            }
        }
        
        // 顺序执行
        for _, move := range executable {
            l.executor.Execute(ctx, move)  // 调用 cognition.Executor 接口
            l.promoter.Promote(ctx, attempt)
            l.world.CreateEdge(...)  // 创建 GENERATES 关系
        }
    }
}
```

**ExecutionLoop 做了什么**：
1. 轮询 worldmodel 获取 open actions
2. 依赖检查（Planner 生成的 depends_on）
3. 调用 cognition.Executor 执行（域适配）
4. 调用 Promoter 晋升 Attempt 到 worldmodel
5. 创建溯源边（action → finding）

**命名问题**: "cognition" 过度承诺，实际是**依赖调度 + 域编排**

**结论**: Cognition 层是**战略-战术转换层**，连接 Planner（规划）和 Domain（执行）。不是老架构残留，但命名不够精确。

---

## 🚀 改进方案

### 阶段 1: 统一 Move/Action 命名（P0）

#### 1.1 类型重命名

```bash
# executor 包
executor.Move → executor.Action
executor.LandmarkRef → executor.TargetRef

# 变量和方法
所有 move/moveID → action/actionID
PublishMoveCompleted → PublishActionCompleted
executeMove → executeAction
```

#### 1.2 文件列表

需要修改的文件（按包分组）：

**executor 包**:
- `internal/executor/types.go` - Move struct 定义
- `internal/executor/actor.go` - 所有 move 变量
- `internal/executor/*.go` - 所有相关文件

**cognition 包**:
- `internal/cognition/event.go` - EventMoveCompleted → EventActionCompleted
- `internal/cognition/execution_loop.go` - executeMove → executeAction

**planner 包**:
- `internal/planner/tools.go` - propose_moves → propose_actions
- `internal/planner/agent.go` - 注释更新

**dispatcher 包**:
- `internal/dispatcher/dispatcher.go` - Move → Action

**cmd 包**:
- `cmd/runner/handler_run.go` - nodeToActorMove → nodeToActorAction
- `cmd/runner/cognition.go` - 注释更新

**文档**:
- `docs/ARCHITECTURE.md` - 全局替换 Move → Action
- `docs/DATA-FLOW.md` - 全局替换

#### 1.3 实施步骤

1. **先改类型定义**（打破编译）
2. **批量修复编译错误**（IDE 重构）
3. **手动检查业务逻辑**（确保语义正确）
4. **运行测试**（验证功能）
5. **更新文档**（保持同步）

---

### 阶段 2: 实现 Executor 微观监察（P0）

#### 2.1 当前状态

**已有代码**:
- ✅ `executor/config.go` - MonitorConfig 定义
- ✅ `executor/self_monitor.go` - selfEvaluate() 实现
- ✅ `executor/run_with_monitoring.go` - monitorLoop() 框架
- ✅ `executor/event_loop.go` - eventLoop() 框架

**缺失部分**:
- ❌ monitorLoop 协程未启动
- ❌ eventLoop 协程未启动
- ❌ 三协程协同逻辑未连接

#### 2.2 实施方案

**修改文件**: `internal/executor/run.go`

```go
func (a *Executor) Run(ctx context.Context, moveID string, req ExecutorReq) (ExecutorResult, error) {
    // 当前代码只有单协程执行
    
    // 【新增】判断是否启用监察
    if a.monitorEnabled {
        return a.runWithMonitoring(ctx, moveID, req)
    }
    
    // 【保留】无监察模式（向后兼容）
    return a.runSimple(ctx, moveID, req)
}

// runWithMonitoring 三协程模式
func (a *Executor) runWithMonitoring(ctx context.Context, moveID string, req ExecutorReq) (ExecutorResult, error) {
    execCtx, cancel := context.WithCancel(ctx)
    defer cancel()
    
    state := newExecutionState(moveID, req)
    
    // 【新增】启动三协程
    go a.executeLoop(execCtx, state)        // 协程1: 执行循环
    go a.monitorLoop(execCtx, cancel, state) // 协程2: 自我监察
    go a.eventLoop(execCtx, cancel, state)   // 协程3: 外部控制
    
    // 等待完成
    <-state.done
    return state.result, state.err
}
```

**关键实现**:
1. **executeLoop**: 主执行循环（已有，需微调）
2. **monitorLoop**: 每 5 步评估（已有框架，需连接）
3. **eventLoop**: 监听 EventBus（已有框架，需连接）

#### 2.3 数据结构

```go
// execution_state.go
type executionState struct {
    mu          sync.Mutex
    moveID      string
    goal        string
    steps       []Step        // 执行历史
    currentStep int
    stopped     bool          // monitorLoop/eventLoop 设置
    stopReason  string
    
    correctionChan chan string // monitorLoop/eventLoop → executeLoop
    done           chan struct{}
    result         ExecutorResult
    err            error
}
```

---

### 阶段 3: 实现 Planner 宏观监察（P0）

#### 3.1 当前状态

**已有代码**:
- ✅ `planner/evaluation.go` - GlobalAssessment 定义
- ✅ `planner/evaluation.go` - evaluateGlobal() 实现
- ✅ `planner/control.go` - executeDecisions() 框架
- ✅ `planner/control.go` - Kill/Steer 方法

**缺失部分**:
- ❌ Planner.Start() 只发心跳，不调用 evaluateGlobal()
- ❌ EventBus 发布 action.killed/steered 未实现

#### 3.2 实施方案

**修改文件**: `internal/planner/agent.go`

```go
func (a *Agent) Start(ctx context.Context) error {
    events := a.eventBus.Subscribe(a.taskID)
    defer a.eventBus.Unsubscribe(a.taskID)
    
    // 【修改】心跳定时器改为评估定时器
    evaluationTicker := time.NewTicker(6 * time.Minute)  // 不是 30 秒
    defer evaluationTicker.Stop()
    
    // 初始规划
    if err := a.replan(ctx, Event{Type: EventTaskStarted, TaskID: a.taskID}); err != nil {
        a.logger.Error().Err(err).Msg("initial planning failed")
    }
    
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
            
        case event := <-events:
            // 响应事件重规划
            if err := a.replan(ctx, event); err != nil {
                a.logger.Error().Err(err).Msg("replan failed")
            }
            
        case <-evaluationTicker.C:
            // 【新增】定期全局评估
            if err := a.periodicEvaluation(ctx); err != nil {
                a.logger.Error().Err(err).Msg("periodic evaluation failed")
            }
        }
    }
}

// 【新增】定期评估方法
func (a *Agent) periodicEvaluation(ctx context.Context) error {
    // 1. 获取全局状态
    state, err := a.getGlobalState(ctx)
    if err != nil {
        return fmt.Errorf("get global state: %w", err)
    }
    
    // 2. LLM 评估
    assessment, err := a.evaluateGlobal(ctx, state)
    if err != nil {
        return fmt.Errorf("evaluate global: %w", err)
    }
    
    // 3. 执行决策（Kill/Steer/CreateAction）
    if err := a.executeDecisions(ctx, a.taskID, assessment); err != nil {
        return fmt.Errorf("execute decisions: %w", err)
    }
    
    return nil
}
```

**修改文件**: `internal/planner/control.go`

```go
func (p *Agent) Kill(ctx context.Context, actionID string, reason string) error {
    // 1. 读取 action 节点
    node, err := p.world.GetNode(ctx, actionID)
    if err != nil {
        return fmt.Errorf("get node: %w", err)
    }
    
    // 2. 更新 metadata（添加 KilledReason）
    if err := p.world.UpdateNodeMetadata(ctx, actionID, ...); err != nil {
        return fmt.Errorf("update metadata: %w", err)
    }
    
    // 3. 更新状态
    if err := p.world.UpdateActionState(ctx, actionID, worldmodel.StateAborted, &reason); err != nil {
        return fmt.errorf("update state: %w", err)
    }
    
    // 4. 【新增】发布 EventBus 事件
    if p.actionBus != nil {
        p.actionBus.Publish(eventbus.Event{
            Type:     eventbus.EventActionKilled,
            ActionID: actionID,
            Payload: map[string]interface{}{
                "reason": reason,
                "source": "planner",
            },
        })
    }
    
    p.logger.Warn().Str("action_id", actionID).Str("reason", reason).Msg("planner killing action")
    return nil
}

func (p *Agent) Steer(ctx context.Context, actionID string, guidance string) error {
    // 类似 Kill，但发布 EventActionSteered
    // ...
    
    if p.actionBus != nil {
        p.actionBus.Publish(eventbus.Event{
            Type:     eventbus.EventActionSteered,
            ActionID: actionID,
            Payload: map[string]interface{}{
                "guidance": guidance,
                "source":   "planner",
            },
        })
    }
    
    return nil
}
```

**关键修改**:
1. 心跳从 30 秒改为 6 分钟（对齐文档）
2. 心跳触发全局评估（不只是发事件）
3. Kill/Steer 发布到 `eventbus.Bus`（action 级）

#### 3.3 依赖注入

**修改文件**: `internal/planner/agent.go`

```go
type Agent struct {
    taskID       string
    eventBus     *cognition.EventBus  // Task 级（接收触发）
    actionBus    *eventbus.Bus        // Action 级（发送控制）【新增】
    world        *worldmodel.Store
    controlPlane *controlplane.Store
    router       *provider.Router
    tools        *ToolRegistry
    logger       zerolog.Logger
    stopCh       chan struct{}
}

// Config 配置 Planner Agent
type Config struct {
    TaskID       string
    EventBus     *cognition.EventBus
    ActionBus    *eventbus.Bus        // 【新增】
    World        *worldmodel.Store
    ControlPlane *controlplane.Store
    Router       *provider.Router
    Logger       zerolog.Logger
}
```

**修改文件**: `cmd/runner/planner_manager.go`（推测文件名）

```go
// 创建 Planner 时注入 actionBus
planner := planner.New(planner.Config{
    TaskID:       taskID,
    EventBus:     h.eventBus,    // cognition.EventBus
    ActionBus:    h.actionBus,   // eventbus.Bus【新增】
    World:        h.world,
    ControlPlane: h.controlPlane,
    Router:       h.router,
    Logger:       h.logger,
})
```

---

### 阶段 4: 实现 Hypothesis 和 Evidence 节点（P1）

#### 4.1 创建工具

**新增文件**: `internal/tools/hypothesis.go`

```go
package tools

import (
    "context"
    "encoding/json"
    "github.com/google/uuid"
    "time"
    "github.com/V3teran/liusha/internal/registry"
    "github.com/V3teran/liusha/internal/worldmodel"
)

type writeHypothesisTool struct {
    deps Deps
}

func (t *writeHypothesisTool) Name() string { return "write_hypothesis" }

func (t *writeHypothesisTool) Description() string {
    return "记录一个待验证的假设（hypothesis）到世界模型"
}

func (t *writeHypothesisTool) Schema() registry.ToolSchema {
    return registry.ToolSchema{
        Name:        "write_hypothesis",
        Description: t.Description(),
        Parameters: json.RawMessage(`{
            "type": "object",
            "properties": {
                "statement": {
                    "type": "string",
                    "description": "假设陈述，如：'目标存在 SQL 注入漏洞'"
                },
                "reasoning": {
                    "type": "string",
                    "description": "提出假设的理由"
                },
                "test_plan": {
                    "type": "string",
                    "description": "如何验证这个假设"
                }
            },
            "required": ["statement"]
        }`),
    }
}

func (t *writeHypothesisTool) Execute(ctx context.Context, args []byte) (registry.ToolResult, error) {
    var input struct {
        Statement string `json:"statement"`
        Reasoning string `json:"reasoning"`
        TestPlan  string `json:"test_plan"`
    }
    if err := json.Unmarshal(args, &input); err != nil {
        return registry.ToolResult{Error: "参数解析失败"}, nil
    }
    
    if input.Statement == "" {
        return registry.ToolResult{Error: "statement 不能为空"}, nil
    }
    
    // 构造 content
    content, _ := json.Marshal(map[string]interface{}{
        "statement": input.Statement,
        "reasoning": input.Reasoning,
        "test_plan": input.TestPlan,
    })
    
    confidence := worldmodel.ConfidenceUnverified
    node := worldmodel.Node{
        ID:         uuid.New().String(),
        TaskID:     t.deps.TaskID,
        Kind:       worldmodel.KindHypothesis,
        Content:    content,
        Confidence: &confidence,
        Priority:   5,
        SourceType: worldmodel.SourceExecutor,
        SourceID:   t.deps.ExecutorID,
        CreatedAt:  time.Now(),
        UpdatedAt:  time.Now(),
    }
    
    // 写入 worldmodel
    id, err := t.deps.World.CreateNode(ctx, node)
    if err != nil {
        return registry.ToolResult{Error: fmt.Sprintf("创建节点失败: %v", err)}, nil
    }
    
    // 创建 action → hypothesis 关系（GENERATES）
    // 【需要从上下文获取当前 actionID】
    if actionID := ctx.Value("current_action_id").(string); actionID != "" {
        edge := worldmodel.Edge{
            TaskID:    t.deps.TaskID,
            SrcID:     actionID,
            Rel:       worldmodel.RelGenerates,
            DstID:     id,
            CreatedAt: time.Now(),
        }
        _ = t.deps.World.CreateEdge(ctx, edge)
    }
    
    return registry.ToolResult{
        Output: fmt.Sprintf("Hypothesis 创建成功: %s\n陈述: %s", id, input.Statement),
    }, nil
}
```

**新增文件**: `internal/tools/evidence.go`

```go
type writeEvidenceTool struct {
    deps Deps
}

// 类似 writeHypothesisTool 的结构
// 区别：
// 1. 需要关联 hypothesis_id
// 2. outcome: "confirms" | "refutes"
// 3. 创建 evidence → hypothesis 关系（CONFIRMS 或 REFUTES）
// 4. 如果 confirms，还要创建 evidence → finding 关系（CONFIRMS）
```

#### 4.2 注册工具

**修改文件**: `internal/tools/register.go`

```go
func RegisterAll(reg *registry.Registry, deps Deps) {
    // ... 现有工具
    
    // 【新增】worldmodel 工具
    if deps.World != nil {
        reg.Register(&writeHypothesisTool{deps: deps})
        reg.Register(&writeEvidenceTool{deps: deps})
    }
}
```

#### 4.3 Deps 扩展

**修改文件**: `internal/tools/deps.go`

```go
type Deps struct {
    TaskID        string
    ExecutorID    string
    Host          string
    Tasks         *task.Store
    Findings      *finding.Store
    Corpus        *corpus.Store
    Embedder      corpus.Embedder
    Reranker      corpus.Reranker
    Leads         *lead.Store
    ProxyStore    *traffic.ProxyStore
    AgentStore    *traffic.AgentStore
    Creds         credential.Provider
    Sandbox       *sandbox.Client
    ToolingLoader *skill.Loader
    VulnLoader    *skill.Loader
    World         *worldmodel.Store  // 【新增】
}
```

#### 4.4 Profile 更新

**修改文件**: `internal/dispatcher/profile/profiles.go`

在 Moderate/Complex/Extreme 的工具列表中添加：
```go
Tools: []string{
    // ... 现有工具
    "write_hypothesis",  // 【新增】
    "write_evidence",    // 【新增】
    "done",
},
```

---

### 阶段 5: 实现 ENABLES 关系（P2）

#### 5.1 Planner 工具增强

**修改文件**: `internal/planner/tools.go`

```go
// propose_actions 工具增强
func (t *ProposeActionsTool) Execute(ctx context.Context, input map[string]interface{}) (interface{}, error) {
    // ... 现有逻辑
    
    // 【新增】处理 enable_by 字段
    for _, newAction := range input["actions"].([]interface{}) {
        actionMap := newAction.(map[string]interface{})
        
        // 如果指定了 enable_by（finding ID），创建 ENABLES 关系
        if enableBy, ok := actionMap["enable_by"].(string); ok && enableBy != "" {
            edge := worldmodel.Edge{
                TaskID:    taskID,
                SrcID:     enableBy,  // finding ID
                Rel:       worldmodel.RelEnables,
                DstID:     actionID,  // 新创建的 action ID
                CreatedAt: time.Now(),
            }
            if err := t.world.CreateEdge(ctx, edge); err != nil {
                t.logger.Warn().Err(err).Msg("创建 ENABLES 关系失败")
            }
        }
    }
}
```

#### 5.2 Schema 更新

```go
func (t *ProposeActionsTool) Schema() provider.ToolSchema {
    return provider.ToolSchema{
        Name: "propose_actions",
        Parameters: json.RawMessage(`{
            "type": "object",
            "properties": {
                "actions": {
                    "type": "array",
                    "items": {
                        "type": "object",
                        "properties": {
                            "instruction": {"type": "string"},
                            "complexity": {"type": "string"},
                            "priority": {"type": "integer"},
                            "depends_on": {
                                "type": "array",
                                "items": {"type": "string"}
                            },
                            "enable_by": {
                                "type": "string",
                                "description": "此 action 由哪个 finding 使能（可选）"
                            }
                        }
                    }
                }
            }
        }`),
    }
}
```

---

### 阶段 6: 优化 Complexity 使用（P2）

#### 6.1 动态 Complexity 分配

**修改文件**: `cmd/runner/handler_run.go`

```go
func (h handler) handleSolo(...) error {
    // ... 现有代码
    
    // 【修改】不要固定用 ComplexityMedium
    complexity := h.determineComplexity(brief, scen)
    
    d, reg, err := h.buildDispatcher(ctx, complexity, instruction, sink)
    // ...
}

// 【新增】动态决定复杂度
func (h handler) determineComplexity(brief string, scen cfgscenario.Scenario) provider.Complexity {
    // 简单启发式规则
    if strings.Contains(brief, "快速") || strings.Contains(brief, "检查") {
        return provider.ComplexitySimple
    }
    if strings.Contains(brief, "利用") || strings.Contains(brief, "提权") {
        return provider.ComplexityComplex
    }
    // 默认 Moderate
    return provider.ComplexityModerate
}
```

#### 6.2 Planner 分配 Complexity

**修改文件**: `internal/planner/tools.go`

```go
// propose_actions 工具已经支持设置 complexity
// 但 Planner 的 prompt 需要指导如何选择

// SystemPrompt 增加复杂度选择指南
const complexityGuide = `
## Complexity 选择指南

- **trivial**: 查询类（list_traffic, read_findings）
- **simple**: 基础枚举（端口扫描、目录爆破）
- **moderate**: 漏洞测试（SQL 注入测试、XSS 测试）
- **complex**: 漏洞利用（获取 shell、绕过认证）
- **extreme**: 复杂攻击链（提权→横移→持久化）
`
```

---

### 阶段 7: 命名优化（可选）

#### 7.1 Cognition → Orchestrator

```bash
# 包重命名
internal/cognition → internal/orchestrator

# 类型重命名
cognition.ExecutionLoop → orchestrator.DependencyScheduler
cognition.Executor → orchestrator.DomainExecutor
cognition.Promoter → orchestrator.Promoter
cognition.EventBus → orchestrator.PlannerEventBus
```

#### 7.2 理由

- "cognition" 承诺过高（认知引擎）
- "orchestrator" 更准确（编排器）
- "DependencyScheduler" 明确职责（依赖调度）

**风险**: 大规模重命名，影响范围广。建议**延后或跳过**。

---

## 📅 实施计划

### 时间线

| 阶段 | 工作量 | 优先级 | 预计时间 |
|------|--------|--------|---------|
| 阶段 1: Move/Action 统一 | 中 | P0 | 2-3 小时 |
| 阶段 2: Executor 监察 | 中 | P0 | 4-6 小时 |
| 阶段 3: Planner 监察 | 中 | P0 | 4-6 小时 |
| 阶段 4: Hypothesis/Evidence | 大 | P1 | 6-8 小时 |
| 阶段 5: ENABLES 关系 | 小 | P2 | 2-3 小时 |
| 阶段 6: Complexity 优化 | 小 | P2 | 2-3 小时 |
| 阶段 7: 命名优化 | 大 | P3 | 8-10 小时 |

**总工作量**: 28-39 小时（不含阶段 7）

### 验证方案

每个阶段完成后的验证：

#### 阶段 1 验证
```bash
# 编译通过
go build ./...

# 测试通过
go test ./... -short

# 文档同步
grep -r "Move\|move" docs/ | grep -v "Movement\|Remove" | wc -l
# 应该为 0（除了合理的上下文）
```

#### 阶段 2 验证
```bash
# 启动任务，观察日志
grep "monitorLoop\|selfEvaluate" logs/executor.log

# 预期输出：
# - monitorLoop started
# - selfEvaluate: status=on_track
# - monitorLoop: assessment completed
```

#### 阶段 3 验证
```bash
# 观察 Planner 日志
grep "periodicEvaluation\|action.killed\|action.steered" logs/planner.log

# 预期输出：
# - periodicEvaluation started
# - evaluateGlobal: strategy=adjust
# - planner killing action: action_123
```

#### 阶段 4 验证
```bash
# 查询 worldmodel
psql -d liusha -c "SELECT kind, COUNT(*) FROM wm_node GROUP BY kind;"

# 预期输出包含：
# hypothesis | 5
# evidence   | 3
```

---

## 📊 影响评估

### 风险评估

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|---------|
| 监察逻辑死锁 | 中 | 高 | 充分测试三协程模式 |
| EventBus 消息积压 | 低 | 中 | 监控通道容量 |
| Complexity 选择不当 | 中 | 低 | 可降级到固定值 |
| 重命名引入 bug | 低 | 中 | IDE 重构 + 测试覆盖 |

### 性能影响

| 变更 | CPU | 内存 | 延迟 |
|------|-----|------|------|
| 监察协程 | +10% | +5MB/action | +50ms/5步 |
| Hypothesis/Evidence | +5% | +2MB/task | 无 |
| ENABLES 查询 | +2% | 无 | +10ms/规划 |

**总体影响**: 可接受（<20% CPU，<10MB 内存）

---

## ✅ 验收标准

改进完成后，系统应满足：

### 功能完整性
- [ ] Move/Action 命名全局统一
- [ ] Executor 每 5 步自我评估
- [ ] Planner 每 6 分钟全局评估
- [ ] Kill/Steer 消息正确传递
- [ ] Hypothesis/Evidence 工具可用
- [ ] 5 种关系类型至少各使用 1 次

### 代码质量
- [ ] 所有测试通过
- [ ] 无编译警告
- [ ] 日志可观测（INFO 级别可看到监察决策）
- [ ] 文档与代码同步

### 性能指标
- [ ] 端到端延迟 < 基线 + 20%
- [ ] 内存增长 < 10MB/task
- [ ] 无 goroutine 泄漏

---

## 📚 参考资料

### 架构文档
- `docs/ARCHITECTURE.md` - 双层监察架构设计
- `docs/DATA-FLOW.md` - 数据流和控制流
- `docs/ARCHITECTURE-DEEP-ANALYSIS.md` - 本次深度分析

### 关键代码文件
- `internal/executor/actor.go` - Executor 核心
- `internal/planner/agent.go` - Planner 核心
- `internal/cognition/execution_loop.go` - 编排层
- `internal/dispatcher/dispatcher.go` - 复杂度路由
- `internal/worldmodel/model.go` - 数据模型

---

**制定者**: Claude (Opus 5)  
**审核者**: 用户（liusha 架构师）  
**状态**: 待批准
