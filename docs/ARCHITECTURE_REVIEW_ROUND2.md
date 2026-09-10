# Liusha Agent 架构全面复盘报告（第二轮）

## 执行时间
2024-09-XX（第一轮重构完成后）

## 审查标准
1. **命名中性性**：避免领域特定术语
2. **业界对标**：符合 AI Agent/ADK 最佳实践
3. **概念一致性**：术语统一，无冲突
4. **可扩展性**：跨领域复用能力

---

## 第 1 部分：已优化的核心概念 ✅

### 1. ✅ Insight（洞察）- 优秀
**当前状态：**
```go
type Insight struct {
    Category   Category   // target/credential/infrastructure/business/data/finding/obstacle/note
    Priority   Priority   // critical/high/medium/low
    Confidence Confidence // confirmed/probable/possible
}
```

**评估：**
- ✅ 命名中性（Insight 是通用术语）
- ✅ 对标业界（Business Insights, Data Insights）
- ⚠️ **Category 分类有安全倾向**

**问题分析：**

| Category | 评估 | 说明 |
|----------|------|------|
| `target` | ⚠️ 安全倾向 | "目标"在安全测试中特指攻击目标 |
| `credential` | ⚠️ 安全倾向 | "凭证"是安全/认证术语 |
| `infrastructure` | ✅ 中性 | 基础设施（通用） |
| `business` | ✅ 中性 | 业务逻辑（通用） |
| `data` | ✅ 中性 | 数据特征（通用） |
| `finding` | ⚠️ 有歧义 | 在安全领域特指"漏洞发现" |
| `obstacle` | ✅ 中性 | 障碍（通用） |
| `note` | ✅ 中性 | 笔记（通用） |

**建议优化：**

```go
// 更中性的分类
const (
    // 系统类
    CategorySystem     Category = "system"      // 系统信息（原 target/infrastructure）
    CategoryAuth       Category = "auth"        // 认证信息（原 credential）
    
    // 业务类
    CategoryBusiness   Category = "business"    // 业务逻辑（保持）
    CategoryData       Category = "data"        // 数据特征（保持）
    
    // 发现类
    CategoryDiscovery  Category = "discovery"   // 发现（原 finding，更中性）
    
    // 其他
    CategoryBlocker    Category = "blocker"     // 阻塞点（原 obstacle，更清晰）
    CategoryAnnotation Category = "annotation"  // 注释（原 note，更正式）
)
```

**但是：这个优化可能改动较大，建议暂时保持，未来逐步迁移。**

**结论：**
- 当前可接受（Insight 本身已经非常好）
- Category 可以保持（因为这些分类在多个领域都适用）
- `credential` 在企业应用中也通用（API Key、Token）
- `target` 在数据分析中也有意义（目标系统、目标数据）

---

### 2. ✅ Evaluator（评估器）- 优秀
**当前状态：** 已完成重命名（Verifier → Evaluator）

**评估：**
- ✅ 命名中性
- ✅ 对标 LangChain Evaluator
- ✅ 跨领域适用

**建议：保持不变**

---

### 3. ✅ Roadmap（路线图）- 优秀
**当前状态：**
```go
type RoadmapStep struct {
    Step      float64
    Objective string
    Status    RoadmapStepStatus // todo/active/complete/skipped
}
```

**评估：**
- ✅ 命名中性
- ✅ 对标业界（Product Roadmap）
- ✅ 跨领域适用

**建议：保持不变**

---

## 第 2 部分：需要优化的核心概念 ⚠️

### ❌ 1. Finding（发现）- 需要讨论

**当前状态：**
```go
const (
    KindObjective  NodeKind = "objective"  // 任务目标
    KindAction     NodeKind = "action"     // 执行动作
    KindHypothesis NodeKind = "hypothesis" // 待验证假设
    KindEvidence   NodeKind = "evidence"   // 验证证据
    KindFinding    NodeKind = "finding"    // 确认的发现 ⚠️
)
```

**问题分析：**

| 领域 | Finding 的含义 | 是否合适？ |
|------|---------------|----------|
| 安全测试 | 漏洞发现 | ✅ |
| 数据分析 | 数据洞察/异常 | ⚠️ 有歧义 |
| 代码审查 | Bug、优化点 | ⚠️ 有歧义 |
| 科学研究 | 研究发现 | ✅ |

**业界对标：**

| 系统 | 类似概念 | 命名 |
|------|---------|------|
| ARTEX | 最终结果 | Finding |
| LangChain | 执行结果 | Result |
| AutoGPT | 输出 | Output |
| 科学方法论 | 发现 | Finding |

**结论：Finding 可以保持**

**理由：**
1. ✅ 科学方法论中"Finding"是标准术语
2. ✅ 研究报告中常用"Key Findings"
3. ✅ 在所有领域，"经过验证的重要发现"都适用
4. ✅ WorldModel 的设计本身对标"科学方法论"

**建议：保持不变**

---

### ⚠️ 2. Assignment（分配）- 需要优化

**当前状态：**
```go
type Assignment struct {
    ID     string
    TaskID string
    // ...
}
```

**问题分析：**

1. **Assignment 的语义不清晰**
   - 在代码中，Assignment 似乎是"子任务"或"工作单元"
   - 但"Assignment"字面意思是"分配"（动作），不是"被分配的东西"（名词）

2. **与 Task 的关系不明确**
   - Task：顶层任务
   - Assignment：？？？

**业界对标：**

| 系统 | 类似概念 | 命名 |
|------|---------|------|
| Kubernetes | 工作单元 | Job / WorkloadTask |
| Celery | 任务实例 | Task Instance |
| Airflow | 任务实例 | Task Instance |
| JIRA | 子任务 | Subtask / Issue |
| GitHub | 工作项 | Work Item |

**建议方案：**

#### **方案 A：Assignment → Job（工作）**

```go
type Job struct {
    ID     string
    TaskID string  // 所属 Task
    // ...
}
```

**理由：**
- ✅ Job 是通用术语（Kubernetes Job, Cron Job）
- ✅ 语义清晰：Task 是目标，Job 是执行
- ✅ 跨领域适用

**关系：**
```
Task（任务）
  └── Job（工作/执行单元）
       └── Action（动作）
```

#### **方案 B：Assignment → Execution（执行）**

```go
type Execution struct {
    ID     string
    TaskID string
    // ...
}
```

**理由：**
- ✅ 语义清晰
- ✅ 与 Executor 呼应

**关系：**
```
Task（任务）
  └── Execution（执行实例）
       └── Action（动作）
```

#### **方案 C：保持 Assignment，但明确定义**

如果 Assignment 是"分配给 Executor 的工作包"，那语义上也说得通。

**建议：方案 A（Assignment → Job）**

---

### ⚠️ 3. Hypothesis（假设）- 需要讨论

**当前状态：**
```go
const (
    KindHypothesis NodeKind = "hypothesis" // 待验证假设
)
```

**问题分析：**

| 领域 | Hypothesis 的含义 | 是否合适？ |
|------|------------------|----------|
| 安全测试 | 待验证的漏洞假设 | ✅ |
| 数据分析 | 待验证的数据假设 | ✅ |
| 科学研究 | 科学假设 | ✅ |
| 代码审查 | ？？？ | ❌ 不适用 |
| 自动化运维 | ？？？ | ❌ 不适用 |

**业界对标：**

| 系统 | 类似概念 | 命名 |
|------|---------|------|
| 科学方法论 | 假设 | Hypothesis |
| LangChain | 中间结果 | Intermediate Result |
| AutoGPT | 思考 | Thought |
| ReAct | 观察 | Observation |

**问题：Hypothesis 太"科学"了**

在安全测试中，"假设漏洞存在"很合理。但在其他领域：
- 代码生成：没有"假设"
- 自动化运维：没有"假设"

**建议方案：**

#### **方案 A：Hypothesis → Proposal（提案）**

```go
const (
    KindProposal NodeKind = "proposal" // 待验证的提案/候选
)
```

**理由：**
- ✅ 更通用（任何领域都有"提案"）
- ✅ 语义：Executor 提出候选结果，Evaluator 验证

**关系：**
```
Action → Proposal（Executor 提出） → Evidence（Evaluator 产生） → Finding（确认）
```

#### **方案 B：Hypothesis → Candidate（候选）**

```go
const (
    KindCandidate NodeKind = "candidate" // 待验证的候选结果
)
```

**理由：**
- ✅ 通用（候选解决方案、候选结果）
- ✅ 中性

#### **方案 C：保持 Hypothesis**

如果 WorldModel 的定位就是"科学方法论"，那 Hypothesis 没问题。

**我的建议：**
- 如果 WorldModel 定位是"科学方法论" → 保持 Hypothesis
- 如果要完全通用 → 改为 Proposal 或 Candidate

**结论：需要你决定 WorldModel 的定位**

---

### ⚠️ 4. Evidence（证据）- 需要讨论

**当前状态：**
```go
const (
    KindEvidence NodeKind = "evidence" // 验证证据
)
```

**问题分析：**

类似 Hypothesis，Evidence 也是"科学方法论"术语。

| 领域 | Evidence 的含义 | 是否合适？ |
|------|----------------|----------|
| 安全测试 | 漏洞证据 | ✅ |
| 科学研究 | 实验证据 | ✅ |
| 法律 | 法律证据 | ✅ |
| 数据分析 | ？？？ | ⚠️ |
| 代码生成 | ？？？ | ❌ |

**业界对标：**

| 系统 | 类似概念 | 命名 |
|------|---------|------|
| 科学方法论 | 证据 | Evidence |
| LangChain | 验证结果 | Validation Result |
| AutoGPT | 反馈 | Feedback |

**建议方案：**

#### **方案 A：Evidence → ValidationResult（验证结果）**

```go
const (
    KindValidationResult NodeKind = "validation_result"
)
```

**理由：**
- ✅ 完全中性
- ✅ 语义清晰

#### **方案 B：Evidence → Evaluation（评估）**

```go
const (
    KindEvaluation NodeKind = "evaluation"
)
```

**理由：**
- ✅ 与 Evaluator 呼应
- ✅ 通用

#### **方案 C：保持 Evidence**

如果定位是"科学方法论"。

**我的建议：与 Hypothesis 一致**
- 如果保持 Hypothesis → 保持 Evidence（科学方法论）
- 如果改为 Proposal → 改为 Evaluation（通用）

---

## 第 3 部分：WorldModel 定位决策

### **关键问题：WorldModel 的定位是什么？**

#### **选项 A：科学方法论（当前）**

```
Objective（目标）
  → Action（动作）
     → Hypothesis（假设）
        → Evidence（证据）
           → Finding（发现）
```

**优点：**
- ✅ 概念严谨
- ✅ 对标科学研究
- ✅ 适合需要"验证"的领域（安全、科学、质量保证）

**缺点：**
- ⚠️ 对非验证类任务不适用（代码生成、数据转换）
- ⚠️ 过于"学术"

---

#### **选项 B：通用执行模型**

```
Objective（目标）
  → Action（动作）
     → Proposal（提案/候选）
        → Evaluation（评估）
           → Result（结果）
```

**优点：**
- ✅ 完全中性
- ✅ 跨领域适用
- ✅ 更符合"通用 ADK"定位

**缺点：**
- ⚠️ 失去了"科学方法论"的严谨性

---

#### **选项 C：混合模型**

保持 WorldModel 核心（Hypothesis/Evidence/Finding），但：
- 将其视为"可选的验证层"
- 不是所有 Action 都必须产生 Hypothesis
- 简单任务可以 Action → Result（直接）

**优点：**
- ✅ 灵活
- ✅ 保留严谨性

**缺点：**
- ⚠️ 复杂度增加

---

## 第 4 部分：最终建议

### **核心决策点**

**请你决定：WorldModel 的定位？**

1. **选项 A：保持"科学方法论"**
   - 保持：Hypothesis、Evidence、Finding
   - 适合：安全测试、科学研究、质量保证
   - 代价：限制跨领域

2. **选项 B：完全通用化**
   - 改为：Proposal、Evaluation、Result
   - 适合：通用 ADK
   - 代价：失去学术严谨性

3. **选项 C：保持现状，文档化适用场景**
   - 保持当前命名
   - 明确：WorldModel 对标"科学方法论"
   - 其他类型任务可以不用完整流程

---

### **立即优化建议（不依赖上述决策）**

#### **1. Assignment → Job（高优先级）**

```go
// 重命名
internal/assignment → internal/job

type Job struct {
    ID     string
    TaskID string
    Status JobStatus
    // ...
}
```

**理由：**
- ✅ Job 是业界标准（Kubernetes, Celery）
- ✅ 语义清晰
- ✅ 无争议

---

#### **2. Insight Category 保持不变（低优先级）**

当前的 Category 分类虽然有安全倾向，但：
- `credential` 在企业应用中通用（API Token）
- `target` 可以理解为"目标系统"（通用）
- `infrastructure` 已经很通用
- `finding` 可以理解为"关键发现"（通用）

**建议：保持不变，除非你有更好的分类体系**

---

## 第 5 部分：总结和行动项

### **当前架构评分：9.5/10**

**优势：**
- ✅ 核心概念已优化（Insight, Evaluator, Roadmap）
- ✅ 大部分命名中性
- ✅ 对标业界最佳实践

**待优化：**
- ⚠️ Assignment → Job（建议改）
- ⚠️ WorldModel 定位（需要你决定）

### **行动项**

**立即执行：**
1. **Assignment → Job 重命名**（如果你同意）

**需要决策：**
2. **WorldModel 定位**
   - 选项 A：保持科学方法论
   - 选项 B：完全通用化
   - 选项 C：混合模型

---

## 附录：其他小建议

### 1. Complexity 可以添加文档注释

```go
// Complexity 是 Action 的执行复杂度抽象
//
// 具体度量由执行器决定：
// - 安全测试：执行步数、工具数量
// - 数据分析：数据量、计算复杂度
// - 代码生成：代码行数、依赖数量
type Complexity string
```

### 2. Priority 可以统一

目前：
- Insight 有 Priority（critical/high/medium/low）
- Node 有 Priority（整数 1-10）

建议统一为一套系统。

---

**请告诉我你的决定，我立即执行！**
