# Roadmap 机制设计文档

## 概述

Roadmap（路线图）是 Liusha 的探索式任务规划机制，用于动态管理任务的执行路线。

## 核心概念

### 设计理念

```
传统方案（depends_on）：
  一次性全规划 → 无法动态调整 → 不适合探索式任务

Roadmap 方案：
  粗略规划 → 执行 → 根据结果调整 → 继续执行 → 循环
```

### 关键特性

1. **探索式规划**：根据执行结果动态调整
2. **中粒度步骤**：10-15 个可验证的里程碑
3. **完全替换策略**：简化 LLM 认知负担
4. **两层依赖**：高层（Step 之间）+ 低层（Action 之间）

---

## 数据模型

### RoadmapStep

```go
type RoadmapStep struct {
    TaskID    string                 // 任务 ID
    Step      float64                // 步骤编号（支持小数，如 1.5）
    Objective string                 // 步骤目标（自然语言）
    Status    RoadmapStepStatus      // 状态
    DependsOn []float64              // 依赖的步骤编号
    Context   map[string]interface{} // 上下文数据
    Rationale string                 // 推理过程
}
```

### 状态定义

| 状态 | 含义 | 转换条件 |
|------|------|---------|
| `todo` | 待执行 | 初始状态 |
| `active` | 执行中 | 派发了第一个 Action |
| `complete` | 已完成 | 所有 Action 完成 |
| `skipped` | 已跳过 | Planner 决定跳过 |

### 状态转换图

```
todo ──派发 Action──> active ──所有 Action 完成──> complete
 │                       │
 └──── 跳过 ─────────────┴──────────────────────> skipped
```

---

## 与 Action 的关系

### 两层架构

```
层级 1：Roadmap（高层规划）
  Step 1: "端口扫描和服务识别"
  Step 2: "Web 应用指纹识别"（depends_on: [1.0]）
  Step 3: "测试 SQL 注入"（depends_on: [2.0]）

层级 2：Action（低层执行）
  Step 1 派发：
    Action 1.1: "扫描 80/443 端口"
    Action 1.2: "识别 HTTP 服务"（depends_on: ["1.1"]）
    Action 1.3: "识别服务版本"（depends_on: ["1.2"]）
```

### 映射关系

- **1:N 映射**：一个 Step 派发多个 Action
- **依赖分层**：
  - Step.depends_on：高层依赖（Step 之间）
  - Action.depends_on：低层依赖（同一 Step 内）
- **状态联动**：
  - 派发第一个 Action → Step 变为 active
  - 所有 Action 完成 → Step 变为 complete

---

## 工作流程

### 初始规划（任务启动时）

```
1. Planner 启动
   ↓
2. 调用 LLM 生成初始 Roadmap（10-15 步）
   ↓
3. 使用 generate_roadmap 工具保存
   ↓
4. 为 Step 1 派发 Action
   ↓
5. Step 1 状态：todo → active
```

### 动态调整（Step 完成后）

```
1. Step 1 的所有 Action 完成
   ↓
2. Planner 被唤醒
   ↓
3. 调用 observe_roadmap 查看当前状态
   ↓
4. 调用 observe_state 查看执行结果
   ↓
5. LLM 根据结果调整 Roadmap：
   - 插入新步骤（如发现 WordPress）
   - 修改后续步骤（如改变目标）
   - 跳过不需要的步骤（如目标已达成）
   ↓
6. 使用 generate_roadmap 保存更新后的 Roadmap
   ↓
7. 为下一个可执行的 Step 派发 Action
```

### 完全替换策略

```
旧 Roadmap:
  1.0: 信息收集 [complete]
  2.0: 漏洞测试 [active]
  3.0: 漏洞利用 [todo]

执行发现：目标是 WordPress

新 Roadmap（完全替换）:
  1.0: 信息收集 [complete]
  2.0: 漏洞测试 [active]  ← 保留正在执行的
  2.5: WordPress 版本检查 [todo]  ← 插入新步骤
  3.0: WordPress 漏洞利用 [todo]  ← 修改目标
```

---

## API 使用示例

### 生成初始 Roadmap

```go
steps := []worldmodel.RoadmapStep{
    {
        TaskID:    taskID,
        Step:      1.0,
        Objective: "端口扫描和服务识别",
        Status:    worldmodel.StepTodo,
        DependsOn: []float64{},
        Rationale: "首先识别目标的攻击面",
    },
    {
        TaskID:    taskID,
        Step:      2.0,
        Objective: "Web 应用指纹识别",
        Status:    worldmodel.StepTodo,
        DependsOn: []float64{1.0},
        Rationale: "根据端口扫描结果识别 Web 框架",
    },
    {
        TaskID:    taskID,
        Step:      3.0,
        Objective: "测试 SQL 注入",
        Status:    worldmodel.StepTodo,
        DependsOn: []float64{2.0},
        Rationale: "针对识别出的 Web 应用测试 SQL 注入",
    },
}

err := store.SaveRoadmap(ctx, taskID, steps)
```

### 查询 Roadmap

```go
// 加载完整 Roadmap
steps, err := store.LoadRoadmap(ctx, taskID)

// 获取下一个可执行的 Step
next, err := store.GetNextExecutableStep(ctx, taskID)

// 获取摘要
summary, err := store.GetRoadmapSummary(ctx, taskID)
fmt.Printf("完成率: %.1f%%\n", summary.CompletionRate * 100)
```

### 更新步骤状态

```go
// Step 1 派发了 Action，标记为 active
err := store.UpdateStepStatus(ctx, taskID, 1.0, worldmodel.StepActive)

// Step 1 所有 Action 完成，标记为 complete
err := store.UpdateStepStatus(ctx, taskID, 1.0, worldmodel.StepComplete)

// 决定跳过 Step 3
err := store.UpdateStepStatus(ctx, taskID, 3.0, worldmodel.StepSkipped)
```

### 关联 Action 和 Step

```go
step := 1.0
action := worldmodel.Node{
    ID:          "action_123",
    TaskID:      taskID,
    Kind:        worldmodel.KindAction,
    Content:     []byte(`{"instruction": "扫描 80 端口"}`),
    State:       &stateOpen,
    RoadmapStep: &step,  // 关联到 Step 1.0
}

store.CreateNode(ctx, action)
```

### 判断 Step 是否完成

```go
// 查询 Step 1.0 的所有 Action
actions, err := store.ListActionsByRoadmapStep(ctx, taskID, 1.0)

// 判断 Step 1.0 是否完成
isComplete, err := store.IsRoadmapStepComplete(ctx, taskID, 1.0)
```

---

## Planner 工具

### observe_roadmap

观察当前任务的 Roadmap 状态

**返回**：
```json
{
  "has_roadmap": true,
  "total_steps": 10,
  "todo_steps": 5,
  "active_steps": 1,
  "complete_steps": 4,
  "completion_rate": "40.0%",
  "steps": [
    {
      "step": 1.0,
      "objective": "端口扫描和服务识别",
      "status": "complete"
    },
    {
      "step": 2.0,
      "objective": "Web 应用指纹识别",
      "status": "active",
      "depends_on": [1.0]
    }
  ]
}
```

### generate_roadmap

生成或更新 Roadmap（完全替换）

**参数**：
```json
{
  "steps": [
    {
      "step": 1.0,
      "objective": "端口扫描和服务识别",
      "depends_on": [],
      "rationale": "首先识别目标的攻击面"
    },
    {
      "step": 2.0,
      "objective": "Web 应用指纹识别",
      "depends_on": [1.0],
      "rationale": "根据端口扫描结果识别 Web 框架"
    }
  ]
}
```

---

## 设计决策

### 为什么用完全替换而非增量更新？

**理由**：
1. **简化 LLM 认知负担**：LLM 重新生成完整 Roadmap，无需记住"插入/修改/删除"操作
2. **避免冲突**：无需处理"Step 1.5 已存在"等冲突问题
3. **保证一致性**：每次更新都是完整的 Roadmap，不会出现"孤儿步骤"
4. **易于调试**：每次 Roadmap 都是完整的快照，便于审计

**代价**：
- 每次更新都删除旧 Roadmap（可接受，Roadmap 只有 10-15 步）

### 为什么步骤编号用 float64？

**理由**：
1. **支持动态插入**：在 1.0 和 2.0 之间插入 1.5
2. **简化 Planner 逻辑**：无需重新编号所有后续步骤
3. **保持顺序**：按浮点数排序即可

**替代方案（被否决）**：
- 整数编号 + 重新编号：复杂度高
- UUID：无法表达顺序

### 为什么 depends_on 保留而非完全废弃？

**理由**：
1. **两层依赖**：高层（Step 之间）+ 低层（Action 之间）
2. **职责分离**：Planner 管高层，Executor 管低层
3. **并发控制**：同一 Step 内的多个 Action 可能有串行依赖

**限定作用域**：
- depends_on 只能依赖"同一 Step 派发的 Action"
- 跨 Step 的依赖通过 RoadmapStep.depends_on 表达

---

## 对比：Roadmap vs depends_on

| 维度 | depends_on | Roadmap |
|------|-----------|---------|
| **规划时机** | 一次性全规划 | 逐步规划 |
| **调整能力** | 无法调整 | 动态调整 |
| **上下文传递** | 通过 WorldModel 间接 | 直接在 Step.Context |
| **适合场景** | 固定流程 | 探索式任务 |
| **粒度** | Action 级 | Step 级（中粒度） |

---

## 未来扩展

### 条件分支

```go
Step 2: "测试认证机制"
  ↓ 执行后发现
  
如果发现弱密码：
  Step 3: "尝试弱密码登录"
  
如果发现 SQL 注入：
  Step 3: "利用 SQL 注入绕过认证"
```

实现方式：
- Planner 根据 Step 2 的 Context 动态生成 Step 3

### Roadmap 可视化

```
前端展示：
┌─────────────────────────────────────┐
│ Roadmap 进度：40% (4/10)            │
├─────────────────────────────────────┤
│ ✓ 1.0 端口扫描和服务识别            │
│ ✓ 2.0 Web 应用指纹识别              │
│ ⏳ 3.0 测试 SQL 注入（执行中）       │
│ ⏸ 4.0 测试 XSS                      │
│ ⏸ 5.0 测试 CSRF                     │
└─────────────────────────────────────┘
```

---

## 总结

Roadmap 机制是 Liusha 探索式任务规划的核心，通过动态调整路线图，实现了真正的自主探索能力。

**核心优势**：
- ✅ 动态调整（根据结果调整）
- ✅ 中粒度步骤（可验证的里程碑）
- ✅ 完全替换（简化 LLM 认知）
- ✅ 两层依赖（高层 + 低层）
- ✅ 通用设计（不限于安全领域）
