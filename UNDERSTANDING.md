# Liusha 架构深度理解

## 📋 核心概念

### 1. 核心实体

#### Task（任务）
- **定义**：用户提交的一次安全测试任务
- **表**：`task` 表
- **字段**：
  - `id`: UUID
  - `brief`: 用户的测试描述（例如："测试网站 http://example.com，找出漏洞"）
  - `target_host`: 目标主机
  - `status`: active | done | failed | aborted
  - `assignment_id`: 关联的 assignment
- **生命周期**：pending → active → done/failed/aborted

#### Assignment（任务分配）
- **定义**：一组相关的任务项，可以是单发、批量或定时任务
- **表**：`assignment` 表
- **来源**：
  - `manual`: 用户手动创建
  - `cron`: 定时任务
  - `batch`: 批量导入

#### Agent Run（Agent 执行记录）
- **定义**：一次 Agent 的执行实例
- **表**：`agent_run` 表
- **角色**：
  - `executor`: 执行器角色
  - `planner`: 规划器角色
- **状态**：pending → running → done/error/aborted

#### World Model（世界模型）
- **定义**：知识图谱，存储任务的所有节点和关系
- **表**：`wm_node`, `wm_edge`, `wm_verification`
- **节点类型**（Kind）：
  - `objective`: 目标节点（从 brief 解析）
  - `action`: 动作节点（Planner 生成的具体执行步骤）
  - `hypothesis`: 假设节点（推测的漏洞点）
  - `evidence`: 证据节点（从流量中提取）
  - `finding`: 漏洞发现节点
- **节点状态**（State）：
  - `open`: 待执行
  - `running`: 执行中
  - `done`: 已完成
  - `blocked`: 被阻塞
  - `failed`: 失败
  - `exhausted`: 已穷尽

#### Finding（漏洞发现）
- **定义**：检测到的安全漏洞
- **表**：`finding` 表
- **字段**：
  - `severity`: critical | high | medium | low | info
  - `kind`: 漏洞类型（sqli, xss, rce等）
  - `summary`: 一行摘要
  - `evidence`: 证据（HTTP请求/响应等）
  - `confidence`: 置信度
  - `status`: open | confirmed | false_positive | fixed

### 2. 核心组件

#### API (`cmd/api`)
- **职责**：接收用户请求，创建任务并入队
- **流程**：
  1. 接收 `/chat` 请求（brief + message）
  2. 创建 Assignment
  3. 展开为 Task + Agent Run
  4. 将任务入队到 Redis（asynq）

#### Runner (`cmd/runner`)
- **职责**：从队列中取任务并执行
- **流程**：
  1. 从 asynq 队列获取任务
  2. 启动 Planner Agent（后台 goroutine）
  3. 等待初始规划完成
  4. 启动 Execution Loop

#### Planner Agent (`internal/planner`)
- **职责**：宏观规划，将目标分解为具体动作
- **工作模式**：事件驱动
- **流程**：
  1. 接收事件（TaskStarted, ActionCompleted等）
  2. 读取 World Model 当前状态
  3. 调用 LLM 生成新的 Actions
  4. 将 Actions 写入 World Model
  5. 发送事件通知 Execution Loop

#### Execution Loop (`internal/executor/loop.go`)
- **职责**：执行循环，按优先级执行 Actions
- **流程**：
  1. 从 World Model 读取 open 状态的 Actions
  2. 筛选出可执行的 Actions（依赖已满足）
  3. 按优先级排序
  4. 调用 Coordinator.Execute 执行
  5. 将结果写回 World Model
  6. 通知 Planner Agent
  7. 重复直到没有待执行的 Actions

#### Coordinator (`internal/executor/coordinator.go`)
- **职责**：协调单个 Action 的执行
- **流程**：
  1. 接收 Action
  2. 执行快照（记录执行前的 findings）
  3. 调用 AgentFunc（即 runAgent 闭包）
  4. 收割新产生的 findings
  5. 返回 Attempts

#### Dispatcher (`internal/dispatcher`)
- **职责**：根据 Complexity 选择合适的 Profile 并构建 Executor
- **Complexity 级别**：
  - `trivial`: 简单任务（如 ping, curl）
  - `simple`: 基础任务（如扫描端口）
  - `moderate`: 中等复杂度（如 SQL 注入测试）
  - `complex`: 复杂任务（如链式攻击）
  - `expert`: 专家级任务（需要深度推理）

#### Agent (Executor) (`internal/executor/agent.go`)
- **职责**：ReAct 循环执行引擎
- **流程**：
  1. 接收 ExecutorReq（System Prompt + Instruction）
  2. 进入 ReAct 循环：
     - Thought：LLM 思考下一步
     - Action：调用工具
     - Observation：获取工具结果
  3. 直到达到预算限制或自然结束
  4. 返回执行结果

### 3. 数据流

#### 完整流程（从用户请求到发现漏洞）

```
1. 用户提交请求
   POST /chat {brief: "测试 example.com"}
   ↓
2. API 处理
   - 创建 Assignment
   - 创建 Task
   - 创建 Agent Run (executor role)
   - 入队到 Redis (asynq)
   ↓
3. Runner 接收任务
   - asynq worker 从队列取任务
   - 调用 handler.handle()
   ↓
4. 启动 Planner Agent
   - 在独立 goroutine 中运行
   - 执行初始规划：
     * 读取 Task.brief
     * 调用 LLM 生成初始 Objectives 和 Actions
     * 写入 World Model (wm_node 表)
   - 发送 initialPlanDone 信号
   ↓
5. 启动 Execution Loop
   - 等待 initialPlanDone
   - 进入循环：
     a) 从 World Model 读取 open Actions
     b) 筛选可执行的 Actions（依赖满足）
     c) 按优先级排序
     d) 取第一个 Action 执行
   ↓
6. Coordinator 执行 Action
   - 执行前快照（记录已有 findings）
   - 调用 runAgent 闭包
   ↓
7. Dispatcher 选择 Profile
   - 根据 Action.Complexity 选择合适的 Profile
   - 构建 ExecutorReq（System + Instruction + Tools）
   - 创建 Agent 实例
   ↓
8. Agent 执行 ReAct 循环
   - Step 1: Thought → Action (调用工具如 nmap, sqlmap)
   - Step 2: Observation → Thought → Action
   - Step N: 直到完成或达到预算
   - 工具调用会产生流量 → 写入 agent_traffic 表
   ↓
9. 流量采集与分析
   - Proxy 拦截 HTTP 流量
   - 写入 Redis Stream (flow_events)
   - Ingestor 消费流量：
     * 启发式分析（检测漏洞特征）
     * 生成 Finding
     * 写入 finding 表
   ↓
10. Coordinator 收割 Findings
    - 执行后快照（记录新产生的 findings）
    - 对比执行前后的差异
    - 返回 Attempts
    ↓
11. Execution Loop 处理结果
    - 将 Action 状态更新为 done
    - 通知 Planner Agent（ActionCompleted 事件）
    ↓
12. Planner Agent 响应事件
    - 收到 ActionCompleted 事件
    - 重新评估当前状态
    - 生成新的 Actions（如果需要）
    - 写入 World Model
    ↓
13. 循环继续
    - Execution Loop 发现新的 open Actions
    - 重复步骤 5-12
    ↓
14. 任务完成
    - 没有更多 open Actions
    - 或达到最大步数限制
    - 更新 Task 状态为 done
    - 返回最终报告
```

## 🔧 关键机制

### 1. 事件驱动

#### 事件类型
- `TaskStarted`: 任务开始
- `ActionCompleted`: Action 完成
- `ActionFailed`: Action 失败
- `NewFinding`: 发现新漏洞

#### 事件总线
- **Planner 事件总线**：Task 级别，Planner Agent 订阅
- **Action 事件总线**：Action 级别，用于控制单个 Action 的执行

### 2. World Model 知识图谱

#### 节点关系
```
Objective (目标)
  ↓ (分解为)
Action (动作)
  ↓ (产生)
Evidence (证据)
  ↓ (支持)
Hypothesis (假设)
  ↓ (验证为)
Finding (漏洞)
```

#### 依赖关系
- Actions 可以依赖其他 Actions（`depends_on` 字段）
- 只有依赖全部完成的 Actions 才能执行

### 3. 流量采集

#### Proxy 模式
- MITM 代理拦截所有 HTTP(S) 流量
- 流量写入 Redis Stream (`flow_events`)

#### Ingestor 消费
- 从 Redis Stream 读取流量
- 启发式分析检测漏洞特征
- 生成 Finding 并关联到 Task

### 4. 闭包传递（AgentFunc）

```go
// 在 handleCognition 中创建闭包
run := func(ctx context.Context, action executor.Action) error {
    // ... dispatcher.Execute(...)
}

// 传递给 Coordinator
coord := executor.NewCoordinator(taskID, host, findings, run, logger)

// Coordinator 在执行时调用
err := coord.run(ctx, action)
```

## 🐛 已知问题分析

### ✅ 问题 1: Planner Agent Panic（已修复）
**现象**：
- Runner 进程在处理 ActionCompleted 事件时崩溃
- 日志在某个时间点后停止
- 任务状态一直是 active

**根本原因**：
- EventActionCompleted 事件发送时使用 `action_id` 字段
- 但接收时尝试读取 `move_id` 字段
- 导致 `nil.(string)` panic

**修复**：
- 统一使用 `action_id` 字段名
- 添加安全的类型断言检查，避免 panic
- 提交：26af9630

**验证结果**：
- ✅ ActionCompleted 事件被正确接收和处理
- ✅ 没有 panic
- ✅ 任务可以继续执行

### 🔍 问题 2: Agent 执行但不调用 LLM 和工具（排查中）
**现象**：
- Execution Loop 正常运行
- Actions 被标记为 done
- `attempts=0`, `findings=0`
- 没有 LLM 调用记录
- 没有工具调用记录
- Agent.Run 相关日志完全缺失

**已确认的调用链**：
```
✅ Execution Loop 启动
✅ executeMove 被调用
✅ Coordinator.Execute 被调用
✅ runAgent 闭包被调用
✅ Dispatcher.Execute 被调用
❌ Agent.Run ??? (日志缺失)
❌ LLM 调用 (没有记录)
❌ 工具调用 (没有记录)
```

**可能原因**：
1. **Agent.Run 立即返回** - Budget.MaxSteps = 0 或其他配置问题
2. **Agent.Run 未被调用** - Dispatcher.Execute 提前返回
3. **Logger 配置问题** - 日志被过滤或未正确输出
4. **错误被静默吞掉** - 某处有 recover 或空 error 处理

**排查进展**：
- 已添加详细的 Agent 执行日志（Agent.Run, executeLoop, Provider.Complete）
- 提交：3134ab1a
- 下一步：验证日志是否出现，或继续深挖调用链

### ❌ 问题 3: 任务卡在 active 状态（已理解）
**现象**：
- Task.status 一直是 active
- Execution Loop 停止但没有更新状态

**原因**：
- 问题 1 导致 Runner 崩溃，defer 中的状态更新未执行
- 修复问题 1 后，此问题应该也解决了

## 🎯 下一步排查计划

1. **添加更细粒度的日志**
   - Agent.Execute 的每个 step
   - 工具调用的详细信息
   - Finding 生成过程

2. **添加 Panic 恢复机制**
   - 在关键 goroutine 中添加 recover
   - 记录完整的 stack trace

3. **添加健康检查**
   - 监控 goroutine 数量
   - 监控内存使用
   - 监控数据库连接

4. **验证流量采集**
   - 检查 Proxy 是否正常拦截
   - 检查 Redis Stream 是否有数据
   - 检查 Ingestor 是否正常消费

5. **检查 LLM 响应**
   - 记录完整的 LLM 请求和响应
   - 检查是否有工具调用
   - 检查是否达到预算限制

---

**最后更新**: 2026-09-02
**版本**: v1.0
