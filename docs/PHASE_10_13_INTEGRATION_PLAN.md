# Phase 10-13：Framework 与业务层集成计划

## 总体目标

**让业务层（Orchestrator + 4 个 Agent）使用 Framework 的通用能力，而非替换业务逻辑。**

```
Framework 层（已完成 Phase 1-9）
    ↓ 提供基础设施能力
业务层（现有实现）
    ↓ 实现 Liusha 特有工作流
最终用户
```

---

## Phase 10：业务 Agent 适配 Framework 接口（1周）

### 目标

让 4 个业务 Agent 实现 `framework/core.Agent` 接口，使其能被 Framework Runtime 管理。

### 改动清单

#### 1. 创建适配器层

```
internal/framework/adapters/
├── planner_adapter.go      # Planner 适配器
├── executor_adapter.go     # Executor 适配器
├── monitor_adapter.go      # Monitor 适配器
└── evaluator_adapter.go    # Evaluator 适配器
```

#### 2. 实现示例（Planner）

```go
// internal/framework/adapters/planner_adapter.go
package adapters

import (
    "context"
    
    "github.com/V3teran/liusha/internal/framework/core"
    "github.com/V3teran/liusha/internal/planner"
)

// PlannerAdapter 把业务 Planner 适配成 framework.Agent。
type PlannerAdapter struct {
    agent *planner.Agent
}

func NewPlannerAdapter(agent *planner.Agent) *PlannerAdapter {
    return &PlannerAdapter{agent: agent}
}

// 实现 core.Agent 接口
func (a *PlannerAdapter) Name() string {
    return "planner"
}

func (a *PlannerAdapter) Run(ctx context.Context) error {
    // 调用业务 Agent 的原有逻辑
    return a.agent.Start(ctx)
}

func (a *PlannerAdapter) Stop(ctx context.Context) error {
    // 业务 Agent 停止逻辑（如果有）
    return nil
}
```

#### 3. 其他 3 个 Agent 同理

- `ExecutorAdapter`：包装 `*executor.Pool`
- `MonitorAdapter`：包装 `*monitor.Agent`
- `EvaluatorAdapter`：包装 `*evaluator.Agent`

### 验证标准

```go
// 测试：所有适配器都实现了 core.Agent
var _ core.Agent = (*PlannerAdapter)(nil)
var _ core.Agent = (*ExecutorAdapter)(nil)
var _ core.Agent = (*MonitorAdapter)(nil)
var _ core.Agent = (*EvaluatorAdapter)(nil)
```

### 工作量评估

- **代码量**：~400 行（4 个适配器 × 100 行）
- **时间**：2-3 天
- **风险**：低（纯适配器，不改业务逻辑）

---

## Phase 11：业务 Orchestrator 使用 Framework Runtime（2周）

### 目标

让业务 Orchestrator 使用 Framework 的 `runtime.Orchestrator` 来管理 Agent 生命周期。

### 改动清单

#### 1. 重构业务 Orchestrator

```go
// internal/orchestrator/orchestrator.go（改造后）
type Orchestrator struct {
    taskID string
    
    // === 新增：Framework Runtime ===
    runtime *fwruntime.Orchestrator  // ← 用 Framework 管理 Agent
    
    // === 保持：业务资源 ===
    world    *knowledgegraph.Store
    traffic  *traffic.AgentStore
    findings *finding.Store
    eventBus *eventbus.Bus
    
    // === 保持：业务 Agents（但生命周期交给 runtime） ===
    planner      *planner.Agent
    monitor      *monitor.Agent
    executorPool *executor.Pool
    evaluator    *evaluator.Agent
    
    logger zerolog.Logger
}

func New(cfg Config) *Orchestrator {
    o := &Orchestrator{
        taskID:   cfg.TaskID,
        world:    cfg.World,
        traffic:  cfg.Traffic,
        findings: cfg.Findings,
        eventBus: cfg.EventBus,
        logger:   cfg.Logger,
    }
    
    // 创建业务 Agents（保持不变）
    o.planner = planner.New(cfg.PlannerConfig)
    o.monitor = monitor.New(cfg.MonitorConfig)
    o.executorPool = executor.NewPool(...)
    o.evaluator = evaluator.NewAgent(cfg.EvaluatorConfig)
    
    // === 新增：创建 Framework Runtime ===
    o.runtime = fwruntime.NewOrchestrator(fwruntime.OrchestratorConfig{
        Logger: cfg.Logger,
    })
    
    // === 新增：注册适配器到 Runtime ===
    o.runtime.RegisterAgent(adapters.NewPlannerAdapter(o.planner))
    o.runtime.RegisterAgent(adapters.NewMonitorAdapter(o.monitor))
    // Executor 和 Evaluator 按需调用，不注册到持续运行
    
    return o
}
```

#### 2. 改造 Run 方法

```go
func (o *Orchestrator) Run(ctx context.Context) error {
    o.logger.Info().Msg("orchestrator starting")
    
    // === 使用 Framework 启动持续运行的 Agents ===
    // 不再手动 go o.planner.Start(ctx)
    // 不再手动 go o.monitor.Start(ctx)
    go o.runtime.Run(ctx)  // ← Framework 负责生命周期
    
    // === 业务逻辑保持不变 ===
    // 等待 Planner 初始规划完成
    <-o.planner.WaitInitialPlanDone()
    
    // 主循环（保持不变）
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
            // 1. 获取可执行 Actions（从 KnowledgeGraph）
            actions := o.getExecutableActions()
            
            // 2. 执行 Actions（Executor Pool，按需调用）
            o.executeActions(ctx, actions)
            
            // 3. 验证 Hypotheses（Evaluator，按需调用）
            o.verifyHypotheses(ctx)
            
            // 4. 处理 Monitor 决策
            o.handleMonitorDecisions(ctx)
            
            // 5. 检查完成条件
            if o.isComplete() {
                return nil
            }
            
            time.Sleep(1 * time.Second)
        }
    }
}
```

### 关键点

1. **Agent 生命周期**：交给 Framework Runtime
2. **业务编排逻辑**：保持不变（仍在业务 Orchestrator 中）
3. **资源注入**：保持不变（业务资源仍由业务层持有）

### 验证标准

- [ ] Planner 和 Monitor 由 Framework Runtime 启动
- [ ] 业务编排逻辑正常执行
- [ ] 日志显示 Framework Runtime 的生命周期事件

### 工作量评估

- **代码量**：~300 行（改造 Orchestrator）
- **时间**：4-5 天
- **风险**：中（需要理清生命周期边界）

---

## Phase 12：集成 Framework 的状态管理（2周）

### 目标

把 KnowledgeGraph 的关键状态同步到 Framework 的 Checkpoint，支持任务恢复。

### 改动清单

#### 1. 定义 Liusha 的状态结构

```go
// internal/orchestrator/state.go（新建）
package orchestrator

import (
    "github.com/V3teran/liusha/internal/knowledgegraph"
)

// LiushaState 是 Liusha 任务的持久化状态。
type LiushaState struct {
    // 任务元信息
    TaskID    string    `json:"task_id"`
    Phase     string    `json:"phase"`  // "planning" | "executing" | "monitoring" | "evaluating" | "completed"
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
    
    // KnowledgeGraph 快照
    Objective    *knowledgegraph.Node   `json:"objective"`
    CurrentPlan  *knowledgegraph.Node   `json:"current_plan"`
    PendingActions []*knowledgegraph.Node `json:"pending_actions"`
    CompletedActions []*knowledgegraph.Node `json:"completed_actions"`
    
    // 执行状态
    TotalActions     int `json:"total_actions"`
    CompletedCount   int `json:"completed_count"`
    FailedCount      int `json:"failed_count"`
    
    // Metadata
    Metadata map[string]any `json:"metadata"`
}

// ExtractState 从 KnowledgeGraph 提取快照。
func (o *Orchestrator) ExtractState() LiushaState {
    // 从 o.world 提取关键节点
    // ...
}

// RestoreState 从快照恢复 KnowledgeGraph。
func (o *Orchestrator) RestoreState(state LiushaState) error {
    // 恢复到 o.world
    // ...
}
```

#### 2. 集成 Checkpointer

```go
// internal/orchestrator/orchestrator.go（扩展）
type Orchestrator struct {
    // ... 原有字段
    
    // === 新增：Checkpoint 能力 ===
    checkpointer *postgres.Checkpointer
}

func New(cfg Config) *Orchestrator {
    // ... 原有初始化
    
    // === 新增：创建 Checkpointer ===
    o.checkpointer = postgres.NewCheckpointer(
        cfg.DB,  // PostgreSQL 连接池
        cfg.Logger,
    )
    
    return o
}
```

#### 3. 定期保存 Checkpoint

```go
func (o *Orchestrator) Run(ctx context.Context) error {
    // ... 原有逻辑
    
    // === 新增：定期 Checkpoint ===
    checkpointTicker := time.NewTicker(10 * time.Second)
    defer checkpointTicker.Stop()
    
    for {
        select {
        case <-checkpointTicker.C:
            if err := o.saveCheckpoint(ctx); err != nil {
                o.logger.Error().Err(err).Msg("failed to save checkpoint")
            }
        
        case <-ctx.Done():
            // 退出前保存最终 Checkpoint
            o.saveCheckpoint(context.Background())
            return ctx.Err()
        
        default:
            // 业务逻辑（保持不变）
            // ...
        }
    }
}

func (o *Orchestrator) saveCheckpoint(ctx context.Context) error {
    state := o.ExtractState()
    
    checkpoint := core.Checkpoint{
        TaskID:    o.taskID,
        State:     state,
        CreatedAt: time.Now(),
    }
    
    return o.checkpointer.Save(ctx, checkpoint)
}
```

#### 4. 支持任务恢复

```go
// internal/orchestrator/orchestrator.go（扩展）

// Resume 从 Checkpoint 恢复任务。
func (o *Orchestrator) Resume(ctx context.Context, checkpointID string) error {
    // 1. 加载 Checkpoint
    checkpoint, err := o.checkpointer.Load(ctx, checkpointID)
    if err != nil {
        return fmt.Errorf("load checkpoint: %w", err)
    }
    
    // 2. 恢复状态
    var state LiushaState
    if err := json.Unmarshal(checkpoint.State, &state); err != nil {
        return fmt.Errorf("unmarshal state: %w", err)
    }
    
    if err := o.RestoreState(state); err != nil {
        return fmt.Errorf("restore state: %w", err)
    }
    
    o.logger.Info().
        Str("checkpoint_id", checkpointID).
        Str("phase", state.Phase).
        Msg("task resumed from checkpoint")
    
    // 3. 继续执行
    return o.Run(ctx)
}
```

### 验证标准

- [ ] 任务执行中定期保存 Checkpoint
- [ ] 可以从 Checkpoint 恢复任务并继续执行
- [ ] 恢复后的状态与中断前一致

### 工作量评估

- **代码量**：~600 行（状态定义 + Checkpoint 集成）
- **时间**：5-7 天
- **风险**：中（需要理清 KnowledgeGraph 与 State 的映射）

---

## Phase 13：集成 Framework 的流式传输（1周）

### 目标

把任务执行进度实时推送给前端（WebSocket / SSE）。

### 改动清单

#### 1. 集成 StreamManager

```go
// internal/orchestrator/orchestrator.go（扩展）
type Orchestrator struct {
    // ... 原有字段
    
    // === 新增：流式传输 ===
    streamMgr *middleware.StreamManager
}

func New(cfg Config) *Orchestrator {
    // ... 原有初始化
    
    // === 新增：创建 StreamManager ===
    o.streamMgr = middleware.NewStreamManager(cfg.Logger)
    
    return o
}
```

#### 2. 发送执行事件

```go
func (o *Orchestrator) executeActions(ctx context.Context, actions []*Action) {
    stream, err := o.streamMgr.Stream(ctx, o.taskID)
    if err != nil {
        o.logger.Error().Err(err).Msg("failed to create stream")
        return
    }
    
    for _, action := range actions {
        // === 发送：Action 开始 ===
        stream.Send(middleware.StreamEvent{
            Type: "action_start",
            Data: map[string]any{
                "action_id":   action.ID,
                "action_type": action.Type,
                "description": action.Description,
            },
            Timestamp: time.Now(),
        })
        
        // 执行 Action（原有逻辑）
        result, err := o.executorPool.Execute(ctx, action)
        
        // === 发送：Action 完成 ===
        stream.Send(middleware.StreamEvent{
            Type: "action_complete",
            Data: map[string]any{
                "action_id": action.ID,
                "success":   err == nil,
                "result":    result,
                "error":     errString(err),
            },
            Timestamp: time.Now(),
        })
    }
}
```

#### 3. 前端订阅流

```go
// cmd/api/handlers/stream.go（新建）
package handlers

import (
    "net/http"
    
    "github.com/gin-gonic/gin"
    "github.com/V3teran/liusha/internal/framework/middleware"
)

type StreamHandler struct {
    streamMgr *middleware.StreamManager
}

// GET /api/tasks/:task_id/stream
func (h *StreamHandler) StreamEvents(c *gin.Context) {
    taskID := c.Param("task_id")
    
    // SSE 头
    c.Header("Content-Type", "text/event-stream")
    c.Header("Cache-Control", "no-cache")
    c.Header("Connection", "keep-alive")
    
    // 订阅流
    stream, err := h.streamMgr.Stream(c.Request.Context(), taskID)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    
    // 发送事件
    for event := range stream.Events() {
        c.SSEvent("message", event)
        c.Writer.Flush()
    }
}
```

### 验证标准

- [ ] 前端可以订阅任务执行流
- [ ] 实时收到 Action 开始/完成事件
- [ ] 支持 SSE / WebSocket

### 工作量评估

- **代码量**：~400 行（流式集成 + API Handler）
- **时间**：3-4 天
- **风险**：低（StreamManager 已实现）

---

## Phase 14：补充 Framework 缺失能力（按需，2周）

### 可选任务

根据集成后的实际需求，可能需要：

#### 1. 子任务支持（如果需要）

```go
// internal/framework/runtime/subtask_runner.go（已实现）
// 业务层可以直接使用
subtaskRunner := runtime.NewSubtaskRunner(logger)
result, err := subtaskRunner.Run(ctx, runtime.SubtaskConfig{
    ParentTaskID: o.taskID,
    Objective:    "子任务目标",
    Isolated:     true,  // 独立子图
})
```

#### 2. 条件路由（如果需要）

```go
// internal/framework/core/graph.go（扩展）
type ConditionalNode struct {
    Condition func(State) bool
    TruePath  Node
    FalsePath Node
}
```

#### 3. Memory 管理（如果需要）

```go
// internal/framework/memory/manager.go（新建）
type MemoryManager struct {
    store    MemoryStore
    maxSize  int
    eviction EvictionPolicy
}
```

---

## 总体时间线

| Phase | 任务 | 时间 | 优先级 |
|-------|------|------|--------|
| Phase 10 | 业务 Agent 适配器 | 2-3 天 | P0（必做） |
| Phase 11 | Runtime 集成 | 4-5 天 | P0（必做） |
| Phase 12 | Checkpoint 集成 | 5-7 天 | P1（推荐） |
| Phase 13 | 流式传输集成 | 3-4 天 | P1（推荐） |
| Phase 14 | 补充能力 | 按需 | P2（可选） |
| **总计** | | **2-4 周** | |

---

## 风险评估

### 低风险

- Phase 10：纯适配器，不改业务逻辑
- Phase 13：StreamManager 已实现，只是集成

### 中风险

- Phase 11：需要理清生命周期边界（谁启动谁停止）
- Phase 12：需要理清 KnowledgeGraph ↔ State 的映射

### 缓解措施

1. **增量集成**：先完成 Phase 10-11，验证通过再做 12-13
2. **保留回退**：保持原有代码，用 Feature Flag 切换新旧逻辑
3. **充分测试**：每个 Phase 完成后跑端到端测试

---

## 成功标准

### Phase 10 成功标准

- [ ] 4 个适配器通过编译
- [ ] 实现 `core.Agent` 接口
- [ ] 单元测试通过

### Phase 11 成功标准

- [ ] Framework Runtime 启动 Planner 和 Monitor
- [ ] 业务编排逻辑正常执行
- [ ] 端到端测试通过

### Phase 12 成功标准

- [ ] 定期保存 Checkpoint
- [ ] 可以从 Checkpoint 恢复
- [ ] 恢复后状态一致

### Phase 13 成功标准

- [ ] 前端收到实时事件
- [ ] SSE / WebSocket 正常工作
- [ ] 负载测试通过

---

## 下一步行动

### 立即开始（今天）

1. **创建 Phase 10 分支**：`git checkout -b feat/phase-10-adapters`
2. **创建目录结构**：`mkdir -p internal/framework/adapters`
3. **实现第一个适配器**：`planner_adapter.go`

### 本周目标

- [ ] 完成 Phase 10（4 个适配器）
- [ ] 单元测试覆盖
- [ ] 代码审查

### 两周目标

- [ ] 完成 Phase 11（Runtime 集成）
- [ ] 端到端测试通过
- [ ] 文档更新

---

## 文档更新计划

完成集成后需要更新：

1. **架构文档**：`docs/ARCHITECTURE_FINAL_REVIEW_COMPLETE.md`
   - 补充 Framework 与业务层的关系图
   - 更新数据流图

2. **开发指南**：`docs/DEVELOPMENT_GUIDE.md`（新建）
   - 如何添加新 Agent
   - 如何使用 Framework 能力

3. **API 文档**：`docs/API.md`
   - 补充流式 API 文档

---

## 总结

**不是推倒重来，而是渐进增强！**

- **保持**：业务逻辑和编排流程
- **增强**：用 Framework 的基础设施能力
- **收益**：更健壮、可恢复、可观测的系统

**预计 2-4 周完成核心集成，Liusha 进入 v3.0 时代！**
