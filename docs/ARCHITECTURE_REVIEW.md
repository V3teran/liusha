# Liusha Agent 架构完整复盘报告

## 审查标准
1. **命名中性性**：避免安全领域特定术语，支持跨领域复用
2. **业界对标**：符合 AI Agent/ADK 最佳实践
3. **概念一致性**：术语统一，无冲突
4. **可扩展性**：易于扩展到其他领域

---

## 第 1 部分：核心概念审查

### ✅ WorldModel 节点类型 - 优秀

**当前设计：**
```
- objective: 目标
- action: 动作
- hypothesis: 假设
- evidence: 证据
- finding: 发现
```

**评估：**
- ✅ 命名中性（通用术语）
- ✅ 符合科学方法论
- ✅ 对标 PDDL 标准（action）
- ✅ finding 在科学研究中是通用术语

**建议：保持不变**

---

### ✅ WorldModel 关系类型 - 优秀

**当前设计：**
```
- GENERATES: 生成
- CONFIRMS: 确认
- REFUTES: 反驳
- ENABLES: 使能
- DEPENDS_ON: 依赖
```

**评估：**
- ✅ 命名中性（通用关系）
- ✅ 语义清晰
- ✅ 符合逻辑推理规范

**建议：保持不变**

---

### ✅ Action 状态 - 优秀

**当前设计：**
```go
State:
- open: 待执行
- blocked: 被阻塞
- running: 执行中
- done: 已完成
- failed: 执行失败
- exhausted: 已耗尽
- aborted: 被中止
```

**评估：**
- ✅ 命名中性（通用状态）
- ✅ 覆盖完整生命周期
- ✅ 对标 ARTEX（但不是抄袭，这是业界标准）

**建议：保持不变**

---

### ❌ Lead（情报）- 需要重命名

**当前设计：**
```go
// internal/lead/model.go
type Category string

const (
    CategoryTarget         Category = "target"         // 目标信息
    CategoryCredential     Category = "credential"     // 凭证信息
    CategoryInfrastructure Category = "infrastructure" // 基础设施
    CategoryBusiness       Category = "business"       // 业务逻辑
    CategoryData           Category = "data"           // 数据特征
    CategoryFinding        Category = "finding"        // 发现（漏洞/问题）
    CategoryObstacle       Category = "obstacle"       // 障碍
    CategoryNote           Category = "note"           // 笔记
)
```

**问题分析：**

1. **"Lead" 术语来源**
   - 情报领域术语（Intelligence Lead）
   - 销售领域术语（Sales Lead）
   - 安全领域术语（Security Lead）
   
2. **跨领域适用性差**
   
   | 领域 | Lead 的含义 | 是否适用？ |
   |------|------------|----------|
   | 安全测试 | 情报线索 | ✅ |
   | 数据分析 | ？（不适用） | ❌ |
   | 代码生成 | ？（不适用） | ❌ |
   | 自动化运维 | ？（不适用） | ❌ |

3. **分类也有安全特征**
   - `credential`（凭证）- 安全术语
   - `infrastructure`（基础设施）- IT 术语
   - `obstacle`（障碍）- 通用，但在安全中特指"防御"

**业界对标：**

| 系统 | 类似概念 | 命名 |
|------|---------|------|
| LangChain | 共享状态 | Memory |
| AutoGPT | 执行结果 | Result / Output |
| ARTEX | 执行产物 | Fact |
| Agents SDK | 共享上下文 | Context / SharedState |

**推荐方案：重命名为 `Observation`（观察）**

**理由：**
1. ✅ 中性术语（科学方法论）
2. ✅ 跨领域适用（数据观察、系统观察、代码观察）
3. ✅ 与 WorldModel 概念一致（observation 是 evidence 的前身）
4. ✅ 符合 Agent 术语（ReAct = Reasoning + Acting + **Observing**）

**重命名映射：**
```
Lead → Observation

Category 重命名：
- target → target_info（保持）
- credential → auth_info（认证信息，更中性）
- infrastructure → system_info（系统信息）
- business → domain_info（领域信息）
- data → data_info（保持）
- finding → discovery（发现，与 WorldModel 的 finding 区分）
- obstacle → blocker（阻塞点，更通用）
- note → annotation（注释，更正式）
```

---

### ❌ Complexity（复杂度）- 需要优化

**当前设计：**
```go
type Complexity string

const (
    ComplexityTrivial  Complexity = "trivial"  // 极简（<5 步）
    ComplexitySimple   Complexity = "simple"   // 简单（~10 步）
    ComplexityModerate Complexity = "moderate" // 中等（~30 步）
    ComplexityComplex  Complexity = "complex"  // 复杂（~50 步）
    ComplexityExtreme  Complexity = "extreme"  // 极限（~100 步）
)
```

**问题分析：**

1. **命名本身 OK**（通用术语）
2. **但语义定义绑定了"步数"**
   - 注释：`// 极简（<5 步）`
   - 这是**执行领域**的定义，不是**规划领域**的定义

3. **跨领域适用性差**
   
   | 领域 | 复杂度的度量 |
   |------|------------|
   | 安全测试 | 执行步数 |
   | 数据分析 | 数据量、计算量 |
   | 代码生成 | 代码行数、依赖复杂度 |
   | 自动化运维 | 系统数量、配置复杂度 |

**推荐方案：抽象复杂度定义**

```go
type Complexity string

const (
    ComplexityTrivial  Complexity = "trivial"  // 极简任务
    ComplexitySimple   Complexity = "simple"   // 简单任务
    ComplexityModerate Complexity = "moderate" // 中等任务
    ComplexityComplex  Complexity = "complex"  // 复杂任务
    ComplexityExtreme  Complexity = "extreme"  // 极限任务
)

// 注释：
// 复杂度是任务执行难度的抽象表示，具体度量由执行器决定。
// 例如：
// - 安全测试：步数、工具数量
// - 数据分析：数据量、计算复杂度
// - 代码生成：代码行数、依赖数量
```

---

### ⚠️ Agent 角色命名 - 部分需要调整

**当前设计：**
```
- Planner: 规划 Agent
- Executor: 执行 Agent
- Verifier: 验证 Agent
- Orchestrator: 编排器
```

**评估：**

| 角色 | 评估 | 说明 |
|------|------|------|
| **Planner** | ✅ 优秀 | 业界标准术语（AI Planning） |
| **Executor** | ✅ 优秀 | 业界标准术语（Executor Pattern） |
| **Verifier** | ⚠️ 需要讨论 | 在安全领域特指"验证器"，但在通用场景下有歧义 |
| **Orchestrator** | ✅ 优秀 | 业界标准术语（Orchestration） |

**Verifier 的问题：**

| 领域 | Verifier 的含义 |
|------|---------------|
| 安全测试 | 验证漏洞真实性 |
| 软件测试 | 验证测试结果 |
| 数据分析 | ？（不清晰） |
| 代码生成 | ？（不清晰） |

**业界对标：**

| 系统 | 类似角色 | 命名 |
|------|---------|------|
| LangChain | 评估器 | Evaluator |
| ARTEX | 验证器 | Verifier |
| AutoGPT | 反思器 | Critic |
| Agents SDK | 审查器 | Reviewer |

**推荐方案：重命名为 `Evaluator`（评估器）**

**理由：**
1. ✅ 更通用（评估结果、评估质量）
2. ✅ 跨领域适用
3. ✅ LangChain 标准术语
4. ✅ 语义更宽泛（不仅是验证真假，还包括评估质量）

**角色职责调整：**
```
Evaluator（评估器）:
- 安全测试：评估假设漏洞的真实性
- 数据分析：评估分析结果的可信度
- 代码生成：评估代码质量
- 自动化运维：评估操作结果
```

---

### ✅ Roadmap - 优秀

**当前设计：**
```
- Roadmap: 路线图
- RoadmapStep: 路线图步骤
- Status: todo/active/complete/skipped
```

**评估：**
- ✅ 命名中性（通用术语）
- ✅ 业界标准（Product Roadmap）
- ✅ 跨领域适用

**建议：保持不变**

---

## 第 2 部分：包结构审查

### ❌ 安全领域特定包 - 需要重构或移除

**当前包列表：**
```
internal/
├── scanagent/      ❌ 安全特定（扫描 Agent）
├── scanstream/     ❌ 安全特定（扫描流）
├── credential/     ⚠️  安全倾向（但可用于其他领域）
├── proxy/          ⚠️  安全倾向（但通用）
├── traffic/        ⚠️  安全倾向（但通用）
```

**问题：**
- `scanagent` 和 `scanstream` 是纯安全领域包
- 如果定位是通用 ADK，这些包应该：
  - **方案 A**：移到 `plugins/security/` 作为插件
  - **方案 B**：重命名为通用术语

**推荐方案：插件化**

```
项目结构：
liusha/
├── internal/          # 核心 ADK
│   ├── planner/
│   ├── executor/
│   ├── evaluator/     # 重命名自 verifier
│   ├── orchestrator/
│   ├── worldmodel/
│   ├── roadmap/
│   └── ...
│
├── plugins/           # 领域特定扩展
│   ├── security/      # 安全测试插件
│   │   ├── scanner/   # 重命名自 scanagent
│   │   ├── exploit/
│   │   └── traffic/
│   │
│   ├── data/          # 数据分析插件
│   │   ├── analyst/
│   │   └── visualizer/
│   │
│   └── code/          # 代码生成插件
│       ├── generator/
│       └── formatter/
```

---

### ✅ 通用核心包 - 优秀

**优秀的通用包：**
```
✅ agentcore/       # 统一 Agent 框架
✅ worldmodel/      # 世界模型
✅ planner/         # 规划器
✅ executor/        # 执行器
✅ orchestrator/    # 编排器
✅ provider/        # LLM Provider
✅ registry/        # 工具注册表
✅ config/          # 配置管理
✅ controlplane/    # 控制平面
✅ task/            # 任务管理
```

---

## 第 3 部分：术语一致性审查

### ❌ 术语不一致问题

**问题 1：Move vs Action**

**当前状况：**
- Planner 的注释和工具中使用 `Move`
- WorldModel 中使用 `Action`

```go
// planner/tools.go
func (t *ProposeMovesTool) Description() string {
    return "生成新的 Move"
}

// worldmodel/model.go
const KindAction NodeKind = "action"
```

**问题：**
- Move 和 Action 是同一个概念吗？
- 如果是，应该统一术语
- 如果不是，应该明确区分

**业界对标：**

| 系统 | 术语 |
|------|------|
| PDDL | Action |
| ARTEX | Intent |
| LangChain | Action / Step |
| AutoGPT | Task |

**推荐方案：统一为 Action**

**理由：**
1. ✅ PDDL 标准术语（AI Planning 领域）
2. ✅ WorldModel 已使用 Action
3. ✅ 更通用（Move 有"移动"的歧义）

**修改：**
```
ProposeMovesTool → ProposeActionsTool
"生成新的 Move" → "生成新的 Action"
```

---

**问题 2：Task vs Assignment**

**当前状况：**
- 有 `task` 包和 `assignment` 包
- 两者关系不清晰

**推荐：明确区分**
```
Task: 用户创建的顶层任务
Assignment: 内部分配的子任务
```

或者：
```
Task: 顶层任务
Job: 执行单元（替换 Assignment）
```

---

## 第 4 部分：总结和建议

### 🔴 高优先级调整（必须）

1. **Lead → Observation**
   - 包名：`internal/lead` → `internal/observation`
   - 类型：`Lead` → `Observation`
   - 分类：优化为中性术语

2. **Verifier → Evaluator**
   - 包名：`internal/verifier` → `internal/evaluator`
   - 类型：`Verifier` → `Evaluator`
   - 职责：从"验证真假"扩展到"评估质量"

3. **Move → Action 统一**
   - Planner 工具统一使用 Action 术语
   - 删除 Move 相关命名

### 🟡 中优先级调整（建议）

4. **Complexity 注释优化**
   - 去掉"步数"等具体度量
   - 改为抽象描述

5. **插件化架构**
   - 将 `scanagent`/`scanstream` 移到 `plugins/security/`
   - 保持核心 ADK 的通用性

### 🟢 低优先级调整（可选）

6. **Task vs Assignment 明确**
   - 文档化两者的关系
   - 或者统一术语

7. **文档完善**
   - 为每个核心概念写设计文档
   - 说明跨领域适用性

---

## 第 5 部分：对标分析

### 与业界 ADK/Framework 对比

| 概念 | Liusha | LangChain | AutoGPT | ARTEX | 评估 |
|------|--------|-----------|---------|-------|------|
| **规划器** | Planner | Planner | Planner | Planner | ✅ 一致 |
| **执行器** | Executor | Executor | Executor | Worker | ✅ 一致 |
| **评估器** | Verifier | Evaluator | Critic | Verifier | ⚠️ 建议改 Evaluator |
| **编排器** | Orchestrator | Orchestrator | - | Engine | ✅ 一致 |
| **世界模型** | WorldModel | Memory | Memory | Facts | ✅ 更先进 |
| **路线图** | Roadmap | Plan | TaskList | Frontier | ✅ 更清晰 |
| **共享状态** | Lead | Memory | - | Fact | ⚠️ 建议改 Observation |
| **动作** | Action | Action | Task | Intent | ✅ 一致 |

**结论：Liusha 整体设计优秀，但需要调整 2 个核心概念（Lead、Verifier）**

---

## 第 6 部分：实施计划

### 阶段 1：核心概念重命名（1-2 天）

```
1. Lead → Observation
   - 重命名包：internal/lead → internal/observation
   - 重命名类型和文件
   - 更新所有引用

2. Verifier → Evaluator
   - 重命名包：internal/verifier → internal/evaluator
   - 重命名 Agent 和工具
   - 更新 WorldModel 引用

3. Move → Action 统一
   - ProposeMovesTool → ProposeActionsTool
   - 更新 System Prompt
```

### 阶段 2：优化和文档（1 天）

```
4. Complexity 注释优化
5. 文档更新（README、设计文档）
6. 术语表（Glossary）
```

### 阶段 3：插件化（可选，1 周）

```
7. 创建 plugins/ 目录结构
8. 迁移安全特定包
9. 插件加载机制
```

---

## 最终评分

**当前评分：8.5/10**

**优势：**
- ✅ WorldModel 设计优秀（5 节点 + 5 关系）
- ✅ Roadmap 机制先进
- ✅ 核心 Agent 角色清晰
- ✅ 大部分术语中性通用

**待改进：**
- ⚠️ Lead 术语不够中性
- ⚠️ Verifier 语义狭隘
- ⚠️ Move/Action 术语不统一
- ⚠️ 安全特定包未隔离

**完成调整后评分：9.5/10**
