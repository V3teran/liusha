# 动态图编排架构

## 概述

本系统采用**动态图编排**架构，支持 AI Agent 在运行时根据观察结果动态生成执行计划。

## 核心组件

### KnowledgeGraph（动态图存储）

**职责**：存储和管理任务执行的动态图状态

**核心能力**：
- 运行时动态添加/修改节点
- CAS 并发控制（分布式安全）
- 实时查询最新状态
- 支持复杂依赖关系

**节点类型**（5种）：
- `objective`: 任务目标
- `action`: 执行动作
- `observation`: 观察结果  
- `evaluation`: 评估结论
- `result`: 最终结果

**关系类型**（5种）：
- `GENERATES`: action → observation
- `CONFIRMS`: evaluation → result
- `REFUTES`: evaluation → observation
- `ENABLES`: result → action
- `DEPENDS_ON`: action → action

### Orchestrator（编排器）

**职责**：协调所有 Agents 的执行

**执行流程**：
1. 轮询 KnowledgeGraph 获取可执行 Actions
2. 检查依赖关系，判断并行/串行执行
3. 使用 CAS 原子抢占 Actions（分布式安全）
4. 执行完成后更新状态到 KnowledgeGraph

**性能优化**：
- 依赖解析：O(n) 复杂度
- 性能监控：action 规模、依赖解析耗时
- 阈值告警：100/500/1000+ actions

### Planner（规划器）

**职责**：动态生成执行计划

**工作模式**：
- 持续运行，监听 Observations
- 根据反馈动态生成新 Actions
- 支持依赖关系定义
- 关联 Roadmap 分步规划

## ReAct 循环

系统实现完整的 ReAct（Reasoning-Action-Observation）循环：

```
时刻 T0: Planner 初始规划 → 生成 Action A1, A2
时刻 T1: Orchestrator 执行 A1 → Executor 产生 Observation O1
时刻 T2: Planner 分析 O1 → 动态生成 Action A3（依赖 A1）
时刻 T3: Orchestrator 执行 A3 → 继续循环
```

### 动态规划示例

**场景：安全测试自适应攻击链**

```
1. [Planner] 初始规划：
   - A1: 端口扫描（无依赖）
   - A2: Web 指纹识别（无依赖）

2. [Executor] 执行 A1 → O1: 发现开放端口 80, 443, 8080

3. [Planner] 基于 O1 动态规划：
   - A3: HTTP 服务探测 :80（依赖 A1）
   - A4: HTTPS 服务探测 :443（依赖 A1）
   - A5: 非标准端口探测 :8080（依赖 A1）

4. [Executor] 并行执行 A3, A4, A5 → O3, O4, O5

5. [Planner] 基于观察结果继续动态规划...
```

## 性能监控

### Action 规模阈值

| 阈值 | 状态 | 说明 |
|------|------|------|
| < 100 | ✅ 正常 | 当前架构最佳性能区间 |
| 100-500 | ⚡ 监控 | 接近评估阈值，持续观察 |
| > 500 | ⚠️  告警 | 建议评估是否需要专用 DAG 引擎 |

### 依赖解析耗时

| 耗时 | 状态 | 说明 |
|------|------|------|
| < 1s | ✅ 正常 | 性能良好 |
| 1-2s | ⚡ 监控 | 接近告警阈值 |
| > 2s | ⚠️  告警 | 可能存在性能瓶颈 |

### 监控方式

**代码埋点**（`internal/orchestrator/metrics.go`）：
```go
metrics.CheckActionScale(ctx, taskID, actionCount)
metrics.RecordDependencyResolutionTime(ctx, taskID, durationMs)
```

**数据库视图**（`docs/database/action_scale_monitoring.sql`）：
```sql
-- 查看所有任务规模
SELECT * FROM action_scale_monitor;

-- 查看高规模任务
SELECT * FROM high_scale_tasks;
```

## 架构决策

### 为什么选择动态图？

**系统要求**：
- AI Agent 需要根据反馈动态调整策略
- 安全测试场景无法预先定义完整流程
- Planner 必须在运行时生成新 Actions

**静态 DAG 的局限**（已移除 WorkflowEngine）：
- Compile-Execute 两阶段锁死图结构
- 运行时无法添加新节点
- 不适合 ReAct 循环模式

**详细分析**：参见 `docs/architecture/ADR-003-dynamic-graph-vs-static-dag.md`

## 未来扩展

### 何时引入专用 DAG 引擎？

满足以下**全部条件**时重新评估：
1. ✅ 单任务 Action 数常态化 >500
2. ✅ 需要复杂条件分支（if-else，非简单依赖）
3. ✅ 需要嵌套子工作流

### 扩展方向

1. **高级调度策略**：
   - 优先级队列
   - 资源感知调度
   - 动态并行度控制

2. **复杂编排模式**：
   - 条件分支（if-then-else）
   - 循环结构（while-loop）
   - 子工作流嵌套

3. **性能优化**：
   - 分层执行优化
   - 增量拓扑排序
   - 并行度自适应调整

## 参考资料

- **学术基础**：
  - ReAct 论文（Yao et al., 2022）
  - PDDL（Planning Domain Definition Language）

- **行业实践**：
  - LangGraph：动态 Agent 图框架
  - AutoGPT：运行时任务生成

- **代码实现**：
  - `internal/knowledgegraph/`: 动态图存储
  - `internal/orchestrator/`: 编排执行
  - `internal/planner/`: 动态规划
