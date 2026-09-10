# Hunters 核心原则 → 新 ADK 架构映射分析

## 🎯 分析目标

验证 hunters 提取的核心原则：
1. 是否适用于新 ADK 架构？
2. 分别对应哪个概念？
3. 是否 1对1 映射？
4. 需要如何调整？

---

## 📐 新 ADK 架构回顾

### **核心架构**

```
Assignment（下发容器）
  └── Task（执行会话）
       └── KnowledgeGraph（知识图谱）
            ├── Objective → Action → Observation → Evaluation → Result
            ├── Roadmap（探索式规划）
            └── Insight（共享洞察）

执行层：
  - Planner Agent（规划）
  - Executor Agent（执行）
  - Evaluator Agent（评估）
  - Orchestrator（编排）
```

### **五个核心概念（ReAct 模式）**
1. **Objective**（目标）
2. **Action**（动作）
3. **Observation**（观察）
4. **Evaluation**（评估）
5. **Result**（结果）

---

## 🔍 原则 1：防幻觉原则

### **Hunters 原文（安全倾向）**
```markdown
1. 必须引用实际证据（不凭记忆）
2. 锚定来源（流量 id）
3. 可复现 > 工具输出
4. 不夸大、不臆造
5. 失败如实说
```

---

### **映射到新架构**

#### **1.1 必须引用实际证据**

**适用性：** ✅ 完全适用

**映射关系：** **N对N（多个概念都需要）**

| 新架构概念 | 如何应用 | 示例 |
|-----------|---------|------|
| **Observation** | ✅ 必须引用实际执行输出 | "观察到 API 返回 200" → 必须有实际 HTTP 响应 |
| **Evaluation** | ✅ 必须引用实际 Observation | "评估为成功" → 必须基于具体 Observation |
| **Result** | ✅ 必须引用实际 Evaluation | "结果确认" → 必须有 Evaluation 支撑 |
| **Insight** | ✅ 必须引用实际数据源 | "发现凭据" → 必须有文件路径/响应 ID |

**结论：** 核心原则，适用于所有需要产出数据的节点

---

#### **1.2 锚定来源**

**适用性：** ✅ 完全适用

**映射关系：** **N对N + 扩展**

| 新架构概念 | 锚定什么 | 字段名 |
|-----------|---------|--------|
| **Observation** | 执行的 Action ID | `源自 action_id: xxx` |
| **Evaluation** | 评估的 Observation ID | `评估 observation_id: xxx` |
| **Result** | 确认的 Evaluation ID | `确认 evaluation_id: xxx` |
| **Insight** | 数据源 | `来源: file_path / response_id / traffic_id` |
| **Action** | 来自哪个 Objective | `目标: objective_id: xxx` |

**新增需求：** KnowledgeGraph 的关系边天然支持！
- `GENERATES`: action → observation（天然锚定）
- `CONFIRMS`: evaluation → result（天然锚定）
- `REFUTES`: evaluation → observation（天然锚定）

**结论：** 完美映射，且新架构的图结构天然支持来源追溯

---

#### **1.3 可复现 > 工具输出**

**适用性：** ✅ 完全适用

**映射关系：** **主要在 Observation → Evaluation**

| 阶段 | 如何应用 |
|------|---------|
| **Action** | 记录执行命令/参数 |
| **Observation** | 记录原始输出（不解读） |
| **Evaluation** | ⭐ 核心：不能只信工具说的，要人读 Observation 确认 |
| **Result** | 基于已确认的 Evaluation |

**示例：**

```markdown
# ❌ 错误（只信工具）
Action: 运行 sqlmap
Observation: "sqlmap 报告: SQL injection found"
Evaluation: "确认存在 SQL 注入" ← 直接信了
Result: "SQL 注入漏洞"

# ✅ 正确（可复现验证）
Action: 运行 sqlmap
Observation: "sqlmap 输出: [payload], 响应: [actual response]"
Evaluation: "人读响应，确认数据库错误泄露，SQL 注入确认" ← 基于实际响应
Result: "SQL 注入漏洞"
```

**通用化：**

```markdown
# 数据分析
Action: 运行统计查询
Observation: "查询结果: [raw data]"
Evaluation: "验证数据质量，确认结论" ← 不能只信查询说的
Result: "统计结论"

# 代码生成
Action: 生成代码
Observation: "生成的代码: [code]"
Evaluation: "运行测试，确认功能正确" ← 不能只信生成说的
Result: "代码通过测试"
```

**结论：** 完全适用，Evaluation 阶段的核心职责

---

#### **1.4 不夸大、不臆造**

**适用性：** ✅ 完全适用

**映射关系：** **N对所有（全局原则）**

| 新架构概念 | 如何应用 |
|-----------|---------|
| **所有节点** | Content 字段必须基于实际数据，不能编造 |
| **Observation** | 如实记录，不添油加醋 |
| **Evaluation** | 如实评估，不夸大严重性/重要性 |
| **Result** | 最终结论与实际一致 |

**通用化：**
- ❌ "这个问题非常严重" → ✅ "这个问题影响范围: X, 严重程度: Y"
- ❌ "完全解决了" → ✅ "解决了 A 和 B，C 仍存在"

**结论：** 全局原则，适用所有节点

---

#### **1.5 失败如实说**

**适用性：** ✅ 完全适用

**映射关系：** **主要在 Action.State**

| 新架构概念 | 如何应用 |
|-----------|---------|
| **Action.State** | ⭐ 核心：`failed` / `blocked` 如实记录 |
| **Action.BlockedReason** | 记录失败原因 |
| **Observation** | 记录失败的输出（error message） |
| **Evaluation** | 评估时承认局限性 |

**示例：**

```markdown
# ✅ 正确
Action: 运行测试
State: failed
BlockedReason: "缺少依赖 libfoo"
Observation: "ImportError: No module named 'foo'"

# ❌ 错误（掩盖失败）
Action: 运行测试
State: done ← 撒谎
Observation: "测试通过" ← 编造
```

**结论：** 完全适用，State 枚举已支持（failed, blocked, exhausted）

---

## 🔍 原则 2：验证方法论

### **Hunters 原文**
```markdown
1. 区分"可能"和"确认"
2. 依赖链清晰表达
3. 动态值处理（resolve）
4. 先确认再断言
```

---

### **映射到新架构**

#### **2.1 区分"可能"和"确认"**

**适用性：** ✅ 完全适用

**映射关系：** **Confidence 字段（1对1）**

| 新架构概念 | 字段 | 枚举值 |
|-----------|------|--------|
| **Observation** | `Confidence` | `unverified` / `verified` / `refuted` |
| **Result** | `Confidence` | `verified` (only) |

**完美映射：**
```markdown
# Hunters
"可能存在" vs "确认可利用"

# 新架构
Observation.Confidence = unverified  # 可能
Observation.Confidence = verified    # 确认（经过 Evaluation）
```

**结论：** 完美 1对1 映射，已有字段支持

---

#### **2.2 依赖链清晰表达**

**适用性：** ✅ 完全适用

**映射关系：** **关系边 + DependsOn 字段（1对1）**

| 新架构支持 | 如何表达 |
|-----------|---------|
| **Action.DependsOn** | `depends_on: ["action_id_1", "action_id_2"]` |
| **关系边** | `DEPENDS_ON: action → action` |
| **ENABLES** | `result → action` (前置结果使能后续动作) |

**Hunters 场景：**
```markdown
# Hunters（安全场景）
"需要先获取 admin 凭证，才能触发此 RCE"
→ finding 传 depends_on=["<前置 finding id>"]
```

**新架构（通用化）：**
```markdown
# 数据分析
Action: "分析用户留存"
DependsOn: ["action_id_extract_users"]  # 依赖先提取用户数据

# 代码生成
Action: "生成集成测试"
DependsOn: ["action_id_generate_api"]   # 依赖先生成 API

# API 测试
Action: "测试更新接口"
DependsOn: ["action_id_create_resource"] # 依赖先创建资源
```

**结论：** 完美 1对1 映射，DependsOn 字段已支持

---

#### **2.3 动态值处理（resolve）**

**适用性：** ⚠️ 部分适用（需要扩展）

**Hunters 场景：**
```markdown
主请求某字段的值在源流量里已失效，须 replay 时现从服务器取：
- opaque-id: 先 POST 建资源拿新 `{id}` 再 GET 该 id
- 一次性 nonce / 过期 token

解决：repro 里加 `resolve`（准备请求），主请求用 `{{占位名}}` 引用
```

**新架构分析：**

这是 **Observation 的 Metadata 需求**，不是核心架构概念

| 是否通用？ | 分析 |
|-----------|------|
| ❌ 不完全通用 | resolve 是 **Web 流量重放专用**（mitmproxy replay） |
| ✅ 有通用思想 | "动态值 / 依赖前置步骤的值" 是通用需求 |

**通用化：**

```markdown
# 通用思想：变量解析
Action 1: 创建资源
Observation 1: {"id": "abc123"}

Action 2: 更新资源
Content: {
  "instruction": "更新资源 {{resource_id}}",
  "variables": {
    "resource_id": {
      "source": "observation_id_1",
      "path": "$.id"
    }
  }
}
```

**映射关系：** **不是 1对1，需要设计变量解析机制**

**建议：**
- ✅ 提取思想：动态值依赖前置 Observation
- ⚠️ 不提取 resolve 实现细节（太专用）
- ✅ 在 Orchestrator 设计中考虑变量传递

**结论：** 思想通用，但实现需要重新设计

---

#### **2.4 先确认再断言**

**适用性：** ✅ 完全适用

**映射关系：** **工作流顺序（流程原则）**

| 新架构流程 | 对应 |
|-----------|------|
| Action → **Observation** | 先获取实际数据 |
| Observation → **Evaluation** | 再评估/断言 |
| Evaluation → **Result** | 最后确认结论 |

**示例：**

```markdown
# ❌ 错误（直接断言）
Action: 查询数据库
Evaluation: "数据库包含 X" ← 没有 Observation 支撑

# ✅ 正确
Action: 查询数据库
Observation: "查询结果: [raw rows]" ← 先确认
Evaluation: "验证结果包含 X" ← 再断言
Result: "确认数据库包含 X"
```

**结论：** 完全适用，ReAct 流程天然支持

---

## 🔍 原则 3：任务分解策略

### **Hunters 原文**
```markdown
1. 识别范围
2. 拆分子任务
3. 派发执行（任务要具体）
4. 汇总结果
```

---

### **映射到新架构**

**适用性：** ✅ 完全适用

**映射关系：** **Orchestrator 职责（1对1）**

| Hunters | 新架构 | 对应概念 |
|---------|--------|---------|
| 识别范围 | Orchestrator 解析 Assignment | Assignment.Payload |
| 拆分子任务 | Orchestrator 创建多个 Task | Task (1:N) |
| 派发执行 | 分配给 Planner/Executor | Agent 调度 |
| 汇总结果 | 读取所有 Task 的 Result | Result 汇总 |

**完美映射示例：**

```markdown
# Hunters（安全场景）
Orchestrator:
  1. 识别目标站点范围（example.com + 子域名）
  2. 拆分：reconnaissance task + exploitation tasks
  3. 派发：reconnaissance → executor, exploitation → executor
  4. 汇总：所有 findings

# 新架构（通用场景）
Orchestrator:
  1. 识别数据分析范围（时间段 + 数据源）
  2. 拆分：extract task + transform tasks + analyze task
  3. 派发：各 task 分配给 executor
  4. 汇总：所有 results
```

**结论：** 完美 1对1 映射，Orchestrator 的核心职责

---

## 🔍 原则 4：韧性执行原则

### **Hunters 原文**
```markdown
遇到失败：
- 不直接放弃
- 尝试重试/降级/绕过
- 调整策略
```

---

### **映射到新架构**

**适用性：** ✅ 完全适用

**映射关系：** **Executor 行为 + Action.State（流程原则）**

| 失败场景 | 新架构处理 |
|---------|-----------|
| **401/403** | Executor 查 Insight（凭据），重试 |
| **工具失败** | Executor 尝试其他工具 |
| **依赖未满足** | Action.State = blocked, 等待依赖完成 |
| **超时** | Action.State = failed, Planner 调整策略 |
| **尝试次数用完** | Action.State = exhausted |

**完美映射示例：**

```markdown
# Hunters（安全场景）
❌ 401/403 直接放弃
✅ 调 read_credentials 拿 token 重试

# 新架构（通用场景）
❌ API 调用失败直接放弃
✅ Executor 从 Insight 获取凭据，重试

# 数据分析
❌ 查询超时直接放弃
✅ Executor 调整查询参数（缩小范围），重试
```

**State 枚举支持：**
```go
StateBlocked   State = "blocked"   // 阻塞（等待依赖）
StateFailed    State = "failed"    // 失败（可重试）
StateExhausted State = "exhausted" // 耗尽（不再重试）
```

**结论：** 完全适用，State 枚举已支持

---

## 📊 映射汇总表

| Hunters 原则 | 新架构映射 | 映射关系 | 适用性 | 需要调整 |
|-------------|-----------|---------|--------|---------|
| **1.1 引用实际证据** | Observation/Evaluation/Result/Insight | N对N | ✅ 完全 | ❌ 无 |
| **1.2 锚定来源** | 关系边 + SourceID | N对N | ✅ 完全 | ❌ 无 |
| **1.3 可复现 > 工具** | Observation → Evaluation | 流程 | ✅ 完全 | ❌ 无 |
| **1.4 不夸大臆造** | 所有节点 | 全局 | ✅ 完全 | ❌ 无 |
| **1.5 失败如实说** | Action.State | 1对1 | ✅ 完全 | ❌ 无 |
| **2.1 区分可能/确认** | Confidence | 1对1 | ✅ 完全 | ❌ 无 |
| **2.2 依赖链表达** | DependsOn + DEPENDS_ON | 1对1 | ✅ 完全 | ❌ 无 |
| **2.3 动态值处理** | 变量解析机制 | ⚠️ 思想 | ⚠️ 部分 | ✅ 需要重新设计 |
| **2.4 先确认再断言** | ReAct 流程 | 流程 | ✅ 完全 | ❌ 无 |
| **3. 任务分解** | Orchestrator | 1对1 | ✅ 完全 | ❌ 无 |
| **4. 韧性执行** | Executor + State | 流程 | ✅ 完全 | ❌ 无 |

---

## ✅ 最终结论

### **映射关系：**

| 类型 | 数量 | 占比 |
|------|------|------|
| **完美 1对1 映射** | 5 个 | 45% |
| **N对N 映射（多概念共用）** | 2 个 | 18% |
| **流程/全局原则** | 4 个 | 37% |
| **需要重新设计** | 1 个 | 9% |

### **适用性：**

| 结论 | 占比 |
|------|------|
| ✅ **完全适用** | 91% |
| ⚠️ **部分适用（需调整）** | 9% |

---

## 🎯 提取建议

### **应该提取的原则（91%）**

#### **完美 1对1 映射：**
1. ✅ 失败如实说 → Action.State
2. ✅ 区分可能/确认 → Confidence
3. ✅ 依赖链表达 → DependsOn
4. ✅ 任务分解 → Orchestrator

#### **N对N 映射（多概念共用）：**
5. ✅ 引用实际证据 → 所有产出节点
6. ✅ 锚定来源 → 关系边

#### **流程/全局原则：**
7. ✅ 可复现 > 工具 → Evaluation 职责
8. ✅ 不夸大臆造 → 全局原则
9. ✅ 先确认再断言 → ReAct 流程
10. ✅ 韧性执行 → Executor 行为

---

### **需要调整的内容（9%）**

#### **动态值处理（resolve）**
- ❌ 不直接提取实现细节（太专用于 Web 流量重放）
- ✅ 提取思想：变量依赖前置 Observation
- ✅ 在 Orchestrator 设计中考虑变量传递机制

---

## 📝 文档结构建议

### **docs/CORE_PRINCIPLES.md**

```markdown
# Liusha Agent 核心原则

## 1. 防幻觉原则（适用于所有节点）

### 1.1 引用实际证据
适用：Observation, Evaluation, Result, Insight

### 1.2 锚定来源
适用：所有节点（通过关系边）

### 1.3 不夸大、不臆造
适用：全局

### 1.4 失败如实说
适用：Action.State

## 2. 验证方法论（ReAct 流程）

### 2.1 可复现 > 工具输出
Evaluation 的核心职责

### 2.2 区分"可能"和"确认"
使用 Confidence 字段

### 2.3 依赖链清晰表达
使用 DependsOn 字段

### 2.4 先确认再断言
ReAct 流程保证

## 3. 编排原则（Orchestrator）

任务分解、派发、汇总

## 4. 执行原则（Executor）

韧性执行、重试、降级
```

---

## 🎉 总结

### **✅ 好消息：91% 完美适用！**

1. ✅ **5 个完美 1对1 映射**
2. ✅ **6 个流程/全局原则**
3. ✅ **新架构天然支持**（Confidence, DependsOn, State, 关系边）
4. ⚠️ **只有 1 个需要调整**（动态值处理 - 提取思想即可）

### **结论：可以放心提取！** 🎉

**要开始创建 `docs/CORE_PRINCIPLES.md` 吗？** 🚀
