# 世界模型重构后续优化待办

**创建日期**: 2026-08-27  
**状态**: 待评审  
**优先级**: P2（非阻塞，架构优化）

---

## 概述

本文档记录世界模型统一重构（Migration 0121）完成后，用户反馈的五个架构问题及其优化方案。

---

## TODO 1: Actor 层架构冗余评估

### 问题描述

当前 `actor.Move` 结构体作为 worldmodel 和 dispatcher 之间的适配层，但实际只做简单字段映射，可能存在过度抽象。

### 现状代码

```go
// internal/actor/types.go
type Move struct {
    ID          string
    Complexity  worldmodel.Complexity  // 来自 worldmodel
    Target      LandmarkRef
    Instruction string
    Cues        []string
    Constraints []registry.Constraint
    Priority    int
    DependsOn   []string
}

// cmd/runner/handler_run.go
func nodeToActorMove(node worldmodel.Node, userPrompt string) actor.Move {
    // 只做字段映射，无业务逻辑转换
}

// internal/dispatcher/dispatcher.go
func (d *Dispatcher) Execute(ctx context.Context, move actor.Move) ([]actor.Execution, error)
```

### 待办事项

- [ ] **评估 actor.Move 存在的必要性**
  - 是否可以让 `dispatcher.Execute` 直接接受 `worldmodel.Node`？
  - 当前 `nodeToActorMove` 只做字段映射，无复杂业务逻辑

- [ ] **Complexity 字段位置评估**
  - Complexity 是执行资源分配策略，不是 Move 的固有属性
  - 考虑作为 `Execute(node, complexity)` 方法参数传递
  - 或者从 worldmodel.Node 中读取（已有 node.Complexity 字段）

- [ ] **清理遗留注释**
  - `internal/actor/types.go:209` 提到 "per MoveKind"
  - 代码中已无 MoveKind，需删除此注释

- [ ] **LandmarkRef 冗余评估**
  - `actor.LandmarkRef` vs `worldmodel.TargetRef`
  - 两者结构完全相同（Domain/RefKind/Locator）
  - 是否需要两套定义？

### 推荐方案

**方案 A（激进）**：删除 actor.Move，dispatcher 直接使用 worldmodel.Node
```go
// dispatcher.Execute 直接接受 Node
func (d *Dispatcher) Execute(ctx context.Context, node worldmodel.Node) ([]actor.Execution, error)
```

**方案 B（保守）**：保留 actor.Move，但明确其为"执行上下文"而非"数据传输对象"
```go
// Move 改为 ExecutionContext，包含执行所需的派生信息
type ExecutionContext struct {
    SourceNode  worldmodel.Node
    Instruction string // 从 Node.Content 解析
    Constraints []registry.Constraint // 从外部注入
}
```

**方案 C（中庸）**：保持现状，但文档化设计意图
- actor.Move 是 dispatcher 层的执行单元抽象
- 与 worldmodel.Node 解耦，便于未来扩展非 worldmodel 来源的执行请求

---

## TODO 2: Assignment vs Task 语义澄清 ✅ 已确认正确

### 用户反馈

用户要求重新理解 assignment vs task 的关系。

### 正确的架构（已确认）

```
Assignment (下发容器)
  └─> Task (测试任务，独立世界模型)
  
隔离维度：
- 世界模型：按 task_id 隔离（每个 task 有自己的认知图）
- Lead 黑板：按 assignment_id 隔离（跨 task 共享情报）
- Finding：按 task_id 归属（单次扫描独立漏洞）

关系：
- 1 assignment → 1~N task
- task.assignment_id → assignment.id (强外键)
- worldmodel.Node.TaskID → task.id（正确）
```

### 现状验证 ✅

- ✅ `worldmodel.Node.TaskID` 指向 `task.id` 是**正确的**
- ✅ Lead 黑板按 `assignment_id` 隔离（跨 task 共享情报）
- ✅ Finding 按 `task_id` 归属（单次扫描独立漏洞）
- ✅ `cmd/runner/handler.go:103` 使用 `taskID` 是**正确的**

### 结论

**无需修改**，当前设计是正确的：
- 世界模型 = task 维度（每个 task 独立的认知图）
- Lead 黑板 = assignment 维度（跨 task 情报共享）
- 两者职责清晰，不混淆

---

## TODO 3: Complexity 分级合理性验证

### 问题描述

用户质疑：既然世界模型 Move 是通用的，为什么还需要 Complexity 分级？

### 现状澄清

**Move 的通用性**：
- ✅ worldmodel.Node(kind=move) 是统一的数据结构
- ✅ 不存在 MoveKind（已删除）
- ✅ 所有 Move 使用相同的 schema

**Complexity 的作用**：
- Complexity 不是 Move 的"类型"，而是"执行资源分配策略"
- 类比：同一个 SQL 查询，可以用不同的执行计划

```
Move (数据) ──[Complexity]──> Profile (执行策略)
                              ├─ SystemPrompt
                              ├─ Tools
                              ├─ Budget
                              └─ Settle
```

### 待办事项

- [ ] **确认设计合理性**
  - Complexity 是必要的（5级差异显著）
  - 不是 Move 的类型，而是执行成本预估

- [ ] **评估 actor.Move.Complexity 字段位置**
  - 当前：Complexity 存储在 worldmodel.Node 中
  - actor.Move 从 Node 复制 Complexity 字段
  - 是否需要在 actor.Move 中保留？

- [ ] **Profile 命名优化**
  - `internal/dispatcher/profile/profiles.go` 按 Complexity 提供 Profile
  - 当前命名清晰：Trivial/Simple/Moderate/Complex/Extreme
  - 无需修改

### 结论

**无需重大修改**，当前设计合理：
- Move 是通用的（数据层）
- Complexity 是执行策略选择器（行为层）
- 两者职责清晰，不冗余

---

## TODO 4: Lead 黑板注释修正

### 问题描述

用户指出：Lead 黑板是长期记忆，按 assignment 划分，需要删除所有"短期"、"TTL"、"Redis"相关字眼。

### 现状核实

```go
// cmd/runner/main.go:115
leads := lead.NewStore(pool)  // ✅ 使用 PostgreSQL

// internal/lead/store.go
type Store struct {
    pool *pgxpool.Pool  // ✅ PostgreSQL 持久化
}
```

**错误的历史注释**（需清理）：
- ❌ "短期记忆"
- ❌ "TTL 滚动过期"
- ❌ "Redis"

### 待办事项

- [ ] **清理 lead 包注释**
  ```go
  // internal/lead/model.go
  // 错误：情报黑板（§7），与 credential 同 Redis 租户命名空间；ttl 滚动过期
  // 正确：情报黑板：assignment 级别的持久化情报共享，按 assignment_id 隔离
  ```

- [ ] **清理 cmd/runner/main.go 注释**
  ```go
  // 错误：情报黑板（§7），与 credential 同 Redis 租户命名空间；ttl 滚动过期（每次写刷新该 host TTL）。
  // 正确：情报黑板：PostgreSQL 持久化，按 assignment 隔离
  ```

- [ ] **验证数据库 schema**
  ```sql
  CREATE TABLE lead (
      assignment_id uuid NOT NULL REFERENCES assignment(id),
      ...
  );
  ```

- [ ] **确认无 TTL 清理逻辑**
  - 检查是否有定时清理 lead 表的代码
  - 如有，评估是否需要保留

### 修正方案

**注释模板**：
```go
// Package lead 实现情报黑板：assignment 级别的长期情报共享。
//
// 定位：同一 assignment 下的多个 task 共享情报黑板，子代理通过黑板同步过程情报。
//
// 隔离维度：assignment_id（按批次隔离）
// 存储：PostgreSQL（持久化，无 TTL）
// 生命周期：与 assignment 一致，不自动过期
```

---

## TODO 5: Finding vs Discovery 命名评估

### 问题描述

用户质疑：Finding 和 Discovery 都是指"发现"，是否需要统一命名？在 liusha 中都指漏洞。

### 现状分析

**Finding（数据库层）**：
- 包：`internal/finding`
- 表：`finding`
- Go 类型：`VulnFinding`
- 用途：数据库持久化的漏洞记录，交付报告

**Discovery（世界模型层）**：
- 包：`internal/worldmodel`
- 节点类型：`KindDiscovery`
- 用途：世界模型中的重要发现节点，认知状态

### 两者关系

```
LLM write_finding
  └─> finding 表 (VulnFinding)
       └─> 可选：晋升为 Discovery 节点（通过 Verifier 验证）
```

### 待办事项

- [ ] **评估统一的必要性**
  - Finding 是业界标准术语（CVE、漏洞报告）
  - Discovery 是通用认知术语（知识图谱节点）
  - 两者层次不同，可能不需要统一

- [ ] **评估统一的方向**
  - 选项 A：统一为 Finding
    - `KindDiscovery` → `KindFinding`
    - 优点：符合业务领域术语
    - 缺点：Discovery 更通用（非漏洞的发现也适用）
  
  - 选项 B：统一为 Discovery
    - `VulnFinding` → `VulnDiscovery`
    - `finding` 表 → `discovery` 表
    - 优点：与世界模型术语一致
    - 缺点：Finding 是行业标准，改动成本高

  - 选项 C：保持现状
    - Finding = 数据库持久化（交付物）
    - Discovery = 世界模型节点（认知状态）
    - 优点：职责清晰，层次分明
    - 缺点：双命名可能造成混淆

- [ ] **检查代码中的混用**
  ```bash
  grep -r "finding\|discovery" --include="*.go" | grep -i "混用\|转换"
  ```

- [ ] **文档化命名约定**
  - 在架构文档中明确两者的区别
  - 提供命名决策的理由

### 推荐方案

**方案 C（保持现状）+ 文档化**

理由：
1. **Finding 是行业标准术语**
   - CVE、OWASP、漏洞扫描器都用 Finding
   - 改动成本高，破坏业务领域一致性

2. **两者层次确实不同**
   - Finding = 数据库持久化（CRUD 操作）
   - Discovery = 世界模型节点（知识图谱）
   - 类比：MySQL 的 Row vs 领域模型的 Entity

3. **转换路径清晰**
   - LLM write_finding → finding 表
   - Verifier 验证通过 → 晋升为 Discovery 节点
   - Finding 可以独立存在（不一定晋升）

**文档补充**：
```markdown
## Finding vs Discovery

### Finding（漏洞记录）
- **层次**：数据库持久化层
- **表**：`finding`
- **Go 类型**：`VulnFinding`
- **用途**：交付报告、漏洞管理、Triage 处置
- **生命周期**：持久化，支持 CRUD

### Discovery（发现节点）
- **层次**：世界模型认知层
- **节点类型**：`KindDiscovery`
- **用途**：知识图谱、推理依赖、因果关系
- **生命周期**：图节点，支持图查询

### 转换关系
```
LLM write_finding → finding 表
                     ↓ (可选)
                 Verifier 验证
                     ↓
              Discovery 节点（已验证）
```

---

## 优先级和时间线

| TODO | 优先级 | 预估工时 | 状态 |
|------|--------|---------|------|
| TODO 1: Actor 层架构评估 | P2 | 3天 | 待评审 |
| TODO 2: Assignment/Task 语义澄清 | - | - | ✅ 已确认正确 |
| TODO 3: Complexity 分级验证 | P3 | 0.5天 | ✅ 已确认合理 |
| TODO 4: Lead 注释修正 | P1 | 0.5天 | ✅ 已完成 |
| TODO 5: Finding/Discovery 命名评估 | P2 | 1天 | 待讨论 |

**实际需要处理的**：
1. ✅ TODO 4（已完成）- Lead 注释修正
2. TODO 1（架构优化，影响面大）- Actor 层冗余评估
3. TODO 5（命名讨论，需要团队共识）- Finding/Discovery 统一

---

## 附录：相关文件清单

### TODO 1 相关文件
- `internal/actor/types.go`
- `cmd/runner/handler_run.go`
- `internal/dispatcher/dispatcher.go`

### TODO 2 相关文件
- `internal/worldmodel/model.go`
- `internal/assignment/model.go`
- `internal/task/model.go`
- `cmd/runner/handler.go`
- `db/migrations/0121_*.sql`

### TODO 4 相关文件
- `internal/lead/model.go`
- `internal/lead/store.go`
- `cmd/runner/main.go`

### TODO 5 相关文件
- `internal/finding/model.go`
- `internal/worldmodel/model.go`
- `internal/executor/web/attempt.go`
- `internal/verifier/verifier.go`
