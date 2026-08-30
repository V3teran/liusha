# liusha 架构文档

**最后更新**: 2026-08-30  
**版本**: v4.3（架构改进完成 - 100%）

---

## 📖 术语映射

**架构概念 → 代码实现**

| 架构层 | 概念 | 代码包 | 核心类型 |
|--------|------|--------|---------|
| 任务编排 | Orchestrator | `internal/orchestrator` | `orchestrator.EventBus`, `orchestrator.ExecutionLoop` |
| 宏观规划 | Planner | `internal/planner` | `planner.Agent` |
| 微观执行 | Executor | `internal/executor` | `executor.Executor` |
| 域适配 | Domain | `internal/domain` | `domain.Profile` |
| 工厂调度 | Dispatcher | `internal/dispatcher` | `dispatcher.Dispatcher` |

> **注**：包名与架构概念完全一致。历史上曾使用 `orchestrator`（已重命名为 `orchestrator`）、`planneragent`、`actor` 等名称，已于 v4.2 统一重构。

---

## 🏗️ 整体架构

```
┌─────────────────────────────────────────────────────────────────┐
│                    liusha 认知引擎                               │
└─────────────────────────────────────────────────────────────────┘

                    ┌─────────────┐
                    │   用户目标   │
                    └──────┬──────┘
                           │
                           ▼
        ┌──────────────────────────────────────┐
        │         Planner (宏观指挥)            │
        │  - 创建 Objective/Action 节点         │
        │  - 全局评估（每6分钟）                 │
        │  - Kill/Steer/CreateAction           │
        └──────────────┬───────────────────────┘
                       │
            worldmodel │ (5种节点+5种关系)
                       │
        ┌──────────────▼───────────────────────┐
        │            EventBus                  │
        │  - action.killed                     │
        │  - action.steered                    │
        │  - action.completed                  │
        └──────────────┬───────────────────────┘
                       │
            订阅事件   │   发布事件
                       │
        ┌──────────────▼───────────────────────┐
        │         Dispatcher                   │
        │  - 按 Complexity 分配 Profile        │
        │  - 创建 Executor 实例                 │
        └──────────────┬───────────────────────┘
                       │
                       ▼
        ┌──────────────────────────────────────┐
        │    Executor (微观执行) - 三协程       │
        │                                      │
        │  [协程1: executeLoop]                │
        │    - ReAct 循环                      │
        │    - 调用工具                         │
        │    - 记录 Step                       │
        │                                      │
        │  [协程2: monitorLoop]                │
        │    - 每5步评估一次                    │
        │    - selfEvaluate()                 │
        │    - Steer/Kill 自己                 │
        │                                      │
        │  [协程3: eventLoop]                  │
        │    - 监听 EventBus                   │
        │    - 处理外部 Kill/Steer             │
        └──────────────┬───────────────────────┘
                       │
                       ▼
        ┌──────────────────────────────────────┐
        │          Tool Registry               │
        │  - write_lead / write_finding        │
        │  - write_hypothesis / write_evidence │
        │  - 其他工具...                        │
        └──────────────┬───────────────────────┘
                       │
                       ▼
        ┌──────────────────────────────────────┐
        │         WorldModel Store             │
        │  (PostgreSQL - 知识图谱)             │
        │                                      │
        │  节点类型 (5种):                      │
        │    - objective (目标)                │
        │    - action (任务)                   │
        │    - hypothesis (假设)               │
        │    - finding (发现)                  │
        │    - lead (线索)                     │
        │                                      │
        │  关系类型 (5种):                      │
        │    - supports (支撑)                 │
        │    - blocks (阻塞)                   │
        │    - leads_to (导致)                 │
        │    - verifies (验证)                 │
        │    - derived_from (派生)             │
        └──────────────────────────────────────┘
```

---

## 🧠 双层监察架构

### 微观监察 (Executor)

```
Executor 执行一个 action
  │
  ├─ [协程1: executeLoop]
  │    Step 1 → Step 2 → Step 3 → Step 4 → Step 5
  │                                           │
  │                                           ▼
  ├─ [协程2: monitorLoop] ◄──────────── 触发评估
  │    │
  │    ├─ getRecentSteps(5)
  │    ├─ selfEvaluate()
  │    │    - 状态: on_track / off_track / stalled
  │    │    - 严重度: low / medium / high
  │    │
  │    └─ 决策:
  │         ├─ off_track + high → Kill (停止执行)
  │         ├─ off_track + low/medium → Steer (注入纠偏消息)
  │         └─ stalled → Kill
  │
  └─ [协程3: eventLoop]
       - 监听 EventBus
       - 处理 Planner 的 Kill/Steer 事件
```

**特点**：
- ✅ 触发方式：每 5 步
- ✅ 评估窗口：最近 5 步
- ✅ 决策范围：只影响自己
- ✅ 日志记录：zerolog

### 宏观监察 (Planner)

```
Planner 心跳循环 (每6分钟)
  │
  ├─ getGlobalState()
  │    - 读取所有 action 节点
  │    - 读取 objective
  │    - 读取 findings
  │
  ├─ evaluateGlobal()
  │    - LLM 全局评估
  │    - 返回 GlobalAssessment:
  │        * strategy: continue / adjust / abort
  │        * actions_to_kill: []
  │        * actions_to_steer: {id: guidance}
  │        * new_actions: []
  │
  └─ executeDecisions()
       ├─ Kill(actionID, reason)
       │    - 存储 KilledReason 到 Metadata
       │    - UpdateActionState(aborted)
       │    - Publish(action.killed)
       │
       ├─ Steer(actionID, guidance)
       │    - 存储 SteeringMessage 到 Metadata
       │    - Publish(action.steered)
       │
       └─ CreateAction(goal)
            - 创建新 action 节点
```

**特点**：
- ✅ 触发方式：每 6 分钟
- ✅ 评估范围：全局（所有 action）
- ✅ 决策范围：跨 action 协调
- ✅ 持久化：Metadata + 日志

---

## 📦 核心组件

### 1. EventBus（事件总线）

**职责**：Planner 和 Executor 之间的异步通信

```go
type Bus struct {
    subscribers map[string]*subscriber  // actionID → subscriber
}

// 订阅（Executor 端）
subscription := bus.Subscribe(ctx, actionID)
for event := range subscription.Events() {
    // 处理事件
}

// 发布（Planner 端）
bus.Publish(Event{
    Type:     "action.killed",
    ActionID: actionID,
})
```

**特点**：
- ✅ 进程内通信（无网络开销）
- ✅ 异步非阻塞
- ✅ 按 actionID 路由
- ✅ 自动生命周期管理

### 2. WorldModel（知识图谱）

**存储结构**：

```sql
-- 节点表
CREATE TABLE wm_node (
    id          TEXT PRIMARY KEY,
    task_id     TEXT NOT NULL,
    kind        TEXT NOT NULL,  -- objective/action/hypothesis/finding/lead
    content     JSONB,
    state       TEXT,           -- pending/running/done/failed/aborted
    priority    INTEGER,
    confidence  TEXT,           -- possible/probable/confirmed
    metadata    JSONB,          -- 扩展字段
    created_at  TIMESTAMPTZ,
    updated_at  TIMESTAMPTZ
);

-- 关系表
CREATE TABLE wm_edge (
    from_id  TEXT,
    to_id    TEXT,
    rel_type TEXT,  -- supports/blocks/leads_to/verifies/derived_from
    PRIMARY KEY (from_id, to_id, rel_type)
);
```

**Metadata 用途**：

```go
// Action 节点的 Metadata
type ActionMetadata struct {
    SteeringMessages []SteeringMessage  // Planner/Executor 的纠偏记录
    KilledReason     *KilledReason      // Kill 原因
}

type SteeringMessage struct {
    Timestamp time.Time
    Source    string  // "planner" | "executor"
    Guidance  string
    Applied   bool
}

type KilledReason struct {
    Timestamp time.Time
    Source    string  // "planner" | "executor"
    Reason    string
}
```

### 3. Executor（执行引擎）

**核心数据结构**：

```go
type Step struct {
    Index       int
    Thought     string       // LLM 推理
    ToolCalls   []ToolCall   // 工具调用
    ToolResults []ToolResult // 工具结果
    Hypotheses  []string
}

type ToolCall struct {
    ID   string
    Name string
    Args string  // JSON
}

type ToolResult struct {
    ToolCallID string
    Output     string
    Error      string
}
```

**配置**：

```go
type MonitorConfig struct {
    Enabled       bool
    StepInterval  int     // 每 N 步评估
    EvaluateSteps int     // 评估最近 N 步
}

// 默认配置
config := MonitorConfig{
    Enabled:       true,
    StepInterval:  5,
    EvaluateSteps: 5,
}
```

### 4. Planner（宏观指挥）

**评估输出**：

```go
type GlobalAssessment struct {
    Strategy       string              // "continue" | "adjust" | "abort"
    Reasoning      string              // LLM 推理
    ActionsToKill  []string            // 要终止的 action IDs
    ActionsToSteer map[string]string   // actionID → guidance
    NewActions     []NewAction         // 要创建的新 action
}
```

**配置**：

```go
type Config struct {
    HeartbeatInterval     time.Duration  // 心跳间隔
    MaxNewActionsPerCycle int            // 每周期最多创建的 action 数
    MaxSteersPerCycle     int            // 每周期最多 steer 的 action 数
    MaxKillsPerCycle      int            // 每周期最多 kill 的 action 数
}

// 默认配置
config := Config{
    HeartbeatInterval:     6 * time.Minute,
    MaxNewActionsPerCycle: 3,
    MaxSteersPerCycle:     5,
    MaxKillsPerCycle:      3,
}
```

---

## 🔄 控制流

### Kill 流程

**Planner Kill**：
```
1. Planner 评估发现某 action 需要终止
2. 读取 action 节点
3. 解析 metadata
4. 添加 KilledReason {source: "planner", reason: "..."}
5. 更新 metadata 到数据库
6. UpdateActionState(aborted)
7. Publish(action.killed)
8. 记录日志（logger.Warn）
   ▼
9. Executor.eventLoop 收到事件
10. state.stop(true) + cancel()
11. executeLoop 检测到 stopped，停止执行
```

**Executor Kill**：
```
1. monitorLoop 每5步评估
2. selfEvaluate() 返回 off_track + high
3. state.stop(true) + cancel()
4. 记录日志（logger.Warn）
5. executeLoop 检测到 stopped，停止执行
```

### Steer 流程

**Planner Steer**：
```
1. Planner 评估发现某 action 需要纠偏
2. 读取 action 节点
3. 解析 metadata
4. 添加 SteeringMessage {source: "planner", guidance: "..."}
5. 更新 metadata 到数据库
6. Publish(action.steered)
7. 记录日志（logger.Info）
   ▼
8. Executor.eventLoop 收到事件
9. correctionChan <- guidance
   ▼
10. executeLoop 下一步注入消息: "[STEERING from planner] ..."
```

**Executor Steer**：
```
1. monitorLoop 每5步评估
2. selfEvaluate() 返回 off_track + low/medium
3. correctionChan <- correction
4. 记录日志（logger.Info）
   ▼
5. executeLoop 下一步注入消息: "[STEERING from self] ..."
```

---

## 📊 数据持久化

| 数据类型 | 存储位置 | 查询方式 | 用途 |
|---------|---------|---------|------|
| **节点/关系** | wm_node/wm_edge | SQL | 知识图谱 |
| **Planner Kill 原因** | Node.Metadata | GetNode + 解析 | 审计 |
| **Planner Steer 消息** | Node.Metadata | GetNode + 解析 | 审计+应用 |
| **Planner 决策日志** | zerolog | 日志查询 | 调试 |
| **Executor Kill 原因** | zerolog | 日志查询 | 调试 |
| **Executor Steer 消息** | zerolog | 日志查询 | 调试 |
| **Step 历史** | 内存 | - | 实时评估 |

---

## 🎯 设计原则

### 1. 双层监察

- **微观**：快速响应（每5步），局部决策
- **宏观**：全局协调（每6分钟），战略调整

### 2. 事件驱动

- Planner 和 Executor 解耦
- 异步通信，非阻塞
- 单向数据流

### 3. 持久化分层

- **宏观决策**：存数据库（审计需求）
- **微观决策**：记日志（调试需求）

### 4. 知识图谱

- 节点表达实体（objective/action/hypothesis/finding/lead）
- 关系表达推理链（supports/blocks/verifies...）
- 支持时间旅行查询

---

## 📈 性能特征

### 资源占用
- Executor 协程：3 个/action
- Planner 协程：1 个
- EventBus：O(1) 路由，O(N) 通知
- 内存：~1MB/action（含 Step 历史）

### 响应时间
- 微观监察：5 步内触发
- 宏观监察：6 分钟内触发
- 事件通知：< 10ms
- Kill 响应：< 100ms（下一次循环检查）
- Steer 响应：< 1 步（下一次 LLM 调用）

### 可扩展性
- 支持数百个并发 Executor
- EventBus 无中心瓶颈
- WorldModel 为唯一共享状态

---

## 🔧 配置示例

```go
// 创建事件总线
bus := eventbus.New()

// 创建 Executor
executor := executor.New(provider, reg, compexecutor, checkpoint, emitter, logger).
    WithEventBus(bus).
    WithMonitor(true, 5, 5, provider)  // 每5步评估最近5步

// 创建 Planner
planner := planner.New(worldmodel, provider, bus, logger).
    WithHeartbeat(6 * time.Minute)

// 启动 Planner（后台运行）
go planner.Run(ctx, taskID)

// 执行 Executor
result, err := executor.Run(ctx, actionID, req)
```

---

## 🎯 未来优化（可选）

### 短期
- [ ] Executor 读取 worldmodel 中的 Steering 消息
- [ ] Planner 配置限流（MaxSteersPerCycle 等）

### 中期
- [ ] 自适应监察间隔（根据任务复杂度调整）
- [ ] 评估结果质量分析（准确率统计）
- [ ] 可视化监察时间线

### 长期
- [ ] 从历史评估中学习（改进 prompt）
- [ ] 预测性监察（提前发现风险）
- [ ] 多 Planner 协同（分布式决策）

---

**版本**: v4.0  
**状态**: ✅ 生产就绪（双层监察架构）  
**核心特性**: 事件驱动 + 双层监察 + 知识图谱
