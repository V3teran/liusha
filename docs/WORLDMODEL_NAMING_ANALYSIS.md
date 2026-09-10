# WorldModel 命名和科学方法论深度分析

## 问题 1：WorldModel 这个命名还可以吗？

### 当前定义
```go
// Package worldmodel 实现统一的世界模型（认知图）。
```

### 业界对标

| 系统 | 类似概念 | 命名 |
|------|---------|------|
| **LangChain** | 对话和执行状态 | Memory |
| **AutoGPT** | 任务状态 | Context / State |
| **ReAct** | 观察和推理 | Trajectory |
| **ARTEX** | 知识库 | Knowledge Base |
| **强化学习** | 环境状态 | World Model |
| **AI Planning** | 规划状态空间 | State Space |
| **认知架构** | 知识表示 | Knowledge Graph / Belief Network |

### WorldModel 的问题

#### ❌ 问题 1：在 AI 领域，WorldModel 有特定含义

**强化学习中的 World Model：**
- 用于预测环境动态（s, a → s'）
- 模拟未来状态
- 用于 Model-Based RL

**Liusha 的 WorldModel：**
- 是"认知图"（Knowledge Graph）
- 存储执行历史和推理链
- 不是"预测模型"

**冲突：** Liusha 的 WorldModel 不是 RL 意义上的 World Model

---

#### ❌ 问题 2：命名不够直观

"WorldModel"听起来很抽象，不能直接看出它是什么。

**对比：**
- ✅ `Memory`（LangChain）- 一听就懂
- ✅ `KnowledgeGraph` - 一听就懂
- ❌ `WorldModel` - 需要解释

---

#### ❌ 问题 3：与"世界模型"的哲学含义冲突

哲学/认知科学中的"世界模型"：
- 主体对外部世界的内部表示
- 非常宽泛的概念

Liusha 的实际内容：
- 执行图（Action Graph）
- 推理链（Reasoning Chain）
- 验证链（Verification Chain）

---

### 替代方案

#### **方案 A：KnowledgeGraph（知识图）** ⭐⭐⭐⭐⭐

```go
package knowledgegraph

// KnowledgeGraph 实现任务的认知图谱
```

**优点：**
- ✅ 业界标准术语
- ✅ 语义准确（图结构 + 节点 + 关系）
- ✅ 中性通用
- ✅ 无歧义

**对标：**
- Google Knowledge Graph
- Neo4j Knowledge Graph
- 学术界标准术语

---

#### **方案 B：ExecutionGraph（执行图）** ⭐⭐⭐⭐

```go
package executiongraph

// ExecutionGraph 实现任务的执行和推理图谱
```

**优点：**
- ✅ 直观（强调"执行"）
- ✅ 中性通用
- ✅ 对标 Airflow DAG、Temporal Workflow

**缺点：**
- ⚠️ 不强调"推理"和"验证"

---

#### **方案 C：TaskGraph（任务图）** ⭐⭐⭐

```go
package taskgraph

// TaskGraph 实现任务的执行和推理图谱
```

**优点：**
- ✅ 简单直观
- ✅ 中性通用

**缺点：**
- ⚠️ 可能与 Task 混淆

---

#### **方案 D：ReasoningGraph（推理图）** ⭐⭐⭐⭐

```go
package reasoninggraph

// ReasoningGraph 实现任务的推理和验证图谱
```

**优点：**
- ✅ 强调"推理"
- ✅ 对标 AI Planning

**缺点：**
- ⚠️ 不强调"执行"

---

#### **方案 E：StateGraph（状态图）** ⭐⭐⭐

```go
package stategraph

// StateGraph 实现任务的状态图谱
```

**优点：**
- ✅ 通用
- ✅ 对标 LangGraph（LangChain 的图执行引擎）

**缺点：**
- ⚠️ "State"可能与"状态机"混淆

---

### 我的推荐排序

**1. KnowledgeGraph（知识图）** ⭐⭐⭐⭐⭐
- 最准确：图结构 + 知识表示
- 最通用：业界标准
- 最清晰：一听就懂

**2. ExecutionGraph（执行图）** ⭐⭐⭐⭐
- 强调执行
- 直观

**3. ReasoningGraph（推理图）** ⭐⭐⭐⭐
- 强调推理
- 对标 AI Planning

---

## 问题 2：科学方法论还能有什么能替换的吗？

### 当前：科学方法论

```
Objective（目标）
  → Action（动作）
     → Hypothesis（假设）
        → Evidence（证据）
           → Finding（发现）
```

### 替代方案

#### **方案 A：保持科学方法论** ✅ 当前

**优点：**
- ✅ 严谨
- ✅ 适合验证类任务

**缺点：**
- ⚠️ 在非验证类任务中不自然

---

#### **方案 B：AI Planning 术语**

```
Goal（目标）
  → Action（动作）
     → Effect（效果）
        → State（状态）
           → Result（结果）
```

**对标：**
- PDDL（Planning Domain Definition Language）
- STRIPS

**优点：**
- ✅ AI Planning 标准
- ✅ 更通用

**缺点：**
- ⚠️ 失去"验证"语义

---

#### **方案 C：通用执行模型**

```
Objective（目标）
  → Action（动作）
     → Proposal（提案）
        → Evaluation（评估）
           → Result（结果）
```

**优点：**
- ✅ 完全通用
- ✅ 跨领域

**缺点：**
- ⚠️ 失去科学严谨性

---

#### **方案 D：混合术语（推荐）** ⭐⭐⭐⭐⭐

保持当前术语，但重新定义语义：

```
Objective（目标）
  → Action（动作）
     → Observation（观察）  // 原 Hypothesis，更通用
        → Evaluation（评估）  // 原 Evidence，更通用
           → Result（结果）     // 原 Finding，更通用
```

**理由：**
- ✅ 对标 ReAct（Reasoning + Acting + **Observing**）
- ✅ 更通用（Observation 适用所有领域）
- ✅ 保持验证流程（Observation → Evaluation → Result）

**命名对比：**

| 当前 | 新方案 | 理由 |
|------|--------|------|
| Hypothesis | Observation | ReAct 标准，更通用 |
| Evidence | Evaluation | 对标 Evaluator Agent |
| Finding | Result | 更中性 |

---

### 我的推荐

**方案 D：混合术语（Observation/Evaluation/Result）** ⭐⭐⭐⭐⭐

**但是：** 这个改动较大，需要权衡收益。

**如果不改，当前的科学方法论也可以接受**，因为：
- ✅ Hypothesis 在科学研究、数据分析中都通用
- ✅ Evidence 在质量保证、测试中通用
- ✅ Finding 可以理解为"关键发现"（通用）

---

## 问题 3：Evaluator 目前是 Agent 吧？

### 当前状态检查

让我检查 Evaluator 的实际定义：
```go
// internal/evaluator/agent.go
type Agent struct {
    // ...
}
```

**确认：是的，Evaluator 是一个 Agent！**

### 问题分析

#### **命名一致性问题**

**当前命名：**
- Planner **Agent**
- Executor **Agent**
- Evaluator **Agent**
- Orchestrator（不是 Agent）

**但包名：**
- `internal/planner` - OK
- `internal/executor` - OK
- `internal/evaluator` - OK（但之前是 verifier）
- `internal/orchestrator` - OK

#### **没有问题！**

**理由：**
1. ✅ 包名是"角色名"（planner/executor/evaluator）
2. ✅ 内部类型是 `Agent`
3. ✅ 对外称呼是"Planner Agent"、"Evaluator Agent"
4. ✅ 这是标准模式

**对比 LangChain：**
```python
from langchain.agents import Agent
planner_agent = Agent(...)
executor_agent = Agent(...)
```

**结论：命名完全正确，无需修改！**

---

## 最终建议

### 高优先级：WorldModel → KnowledgeGraph

**变更：**
```
internal/worldmodel → internal/knowledgegraph

package worldmodel → package knowledgegraph
```

**理由：**
1. ✅ 更准确（知识图谱）
2. ✅ 业界标准（Google/Neo4j）
3. ✅ 无歧义（不与 RL 的 World Model 混淆）
4. ✅ 更直观

---

### 中优先级：科学方法论术语优化（可选）

**选项 A：保持不变**（推荐）
- 当前的 Hypothesis/Evidence/Finding 可以接受
- 改动成本高

**选项 B：改为 Observation/Evaluation/Result**
- 对标 ReAct
- 更通用
- 但改动较大

---

### 低优先级：Evaluator（无需修改）

**当前完全正确：**
- ✅ 包名：evaluator
- ✅ 类型：Agent
- ✅ 称呼：Evaluator Agent

---

## 总结

### 需要你决定

**1. WorldModel → KnowledgeGraph？**
- ✅ 推荐改（更准确、更标准）
- ❌ 保持不变（可接受）

**2. 科学方法论 → Observation/Evaluation/Result？**
- ✅ 改（更通用，对标 ReAct）
- ❌ 保持不变（当前可接受）

**3. Evaluator？**
- ✅ 完全正确，无需改

---

**请告诉我你的决定！**
