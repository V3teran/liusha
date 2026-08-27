# 世界模型重构后续优化 - 最终结论

**创建日期**: 2026-08-27  
**状态**: ✅ 已全部确认  
**结论**: 当前架构设计合理，无需修改

---

## 概述

本文档记录世界模型统一重构（Migration 0121）完成后，用户提出的五个架构问题的最终结论。
经过深入分析，**所有设计都是合理的，无需修改**。

---

## TODO 1: Actor 层架构 ✅ 已确认合理

### 用户质疑

Actor 层 `actor.Move` 是否冗余？`nodeToActorMove` 只做字段映射，是否可以让 dispatcher 直接使用 `worldmodel.Node`？

### 最终结论：保持现状，架构合理

**理由**：

1. **职责分离**
   - `worldmodel.Node` = 存储模型（数据库 schema）
   - `actor.Move` = 执行单元抽象（dispatcher 输入）
   - 两者职责不同，不应混为一谈

2. **解耦设计**
   - dispatcher 不直接依赖 worldmodel 的存储细节
   - 未来 worldmodel schema 变更不影响 dispatcher
   - 符合依赖倒置原则（DIP）

3. **适配层的必要性**
   - `nodeToActorMove` 虽然简单，但是必要的边界转换
   - 从"存储态"转为"执行态"
   - 可以注入额外的执行上下文（Cues、Constraints）

4. **扩展性**
   - 未来可能有非 worldmodel 来源的执行请求
   - actor.Move 作为统一执行接口，保持稳定

### 代码验证

```go
// worldmodel.Node - 存储模型
type Node struct {
    ID         string
    TaskID     string
    Kind       NodeKind
    Content    json.RawMessage  // 灵活载荷
    State      *State
    Complexity *Complexity
    ...
}

// actor.Move - 执行模型
type Move struct {
    ID          string
    Complexity  worldmodel.Complexity
    Target      LandmarkRef          // 结构化目标
    Instruction string               // 解析后的指令
    Cues        []string             // 执行提示
    Constraints []registry.Constraint // 运行时约束
    DependsOn   []string
}
```

**结论**：两者形状不同、职责不同，适配层是必要的。

---

## TODO 2: Assignment vs Task 语义 ✅ 已确认正确

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

## TODO 3: Complexity 分级 ✅ 已确认合理

### 用户质疑

既然世界模型 Move 是通用的，为什么还需要 Complexity 分级？

### 最终结论：Complexity 是必要的

**理由**：

1. **Move 的通用性是正确的**
   - worldmodel.Node(kind=move) 是统一的数据结构
   - 不存在 MoveKind（已删除）
   - 所有 Move 使用相同的 schema

2. **Complexity 不是类型，是策略**
   - Complexity 不是 Move 的"类型分类"
   - 是"执行资源分配策略"的度量
   - 类比：同一个 SQL 查询，可以用不同的执行计划

3. **5 级差异显著**
   ```
   trivial  (<5 步)   → 快速查询、单次工具调用
   simple   (~10 步)  → 基础信息收集、简单枚举
   moderate (~30 步)  → 漏洞测试、流量重放
   complex  (~50 步)  → 漏洞利用、深度分析
   extreme  (~100 步) → 权限提升、横向移动
   ```

4. **Profile 选择器**
   ```
   Move (数据) ──[Complexity]──> Profile (执行策略)
                                 ├─ SystemPrompt
                                 ├─ Tools
                                 ├─ Budget
                                 └─ Settle
   ```

### 结论

**无需修改**，当前设计合理：
- Move 是通用的（数据层）
- Complexity 是执行策略选择器（行为层）
- 两者职责清晰，不冗余

---

## TODO 4: Lead 黑板注释修正 ✅ 已完成

### 问题描述

Lead 黑板是长期记忆，按 assignment 划分，需要删除所有"短期"、"TTL"、"Redis"相关字眼。

### 已完成的修改

1. **internal/lead/model.go**
   - ✅ 删除"租户隔离"等误导表述
   - ✅ 强调"长期记忆"、"PostgreSQL 持久化"、"无 TTL"

2. **cmd/runner/main.go**
   - ✅ 修正初始化注释
   - ✅ 明确"assignment 级别的长期情报共享"

### 正确的定位

```go
// Package lead 实现情报黑板：assignment 级别的跨 task 情报共享。
//
// 定位：同一 assignment 下的多个 task 共享情报黑板，子代理通过黑板同步过程情报。
//
// 隔离维度：assignment_id（按批次隔离）
// 存储：PostgreSQL（持久化，无 TTL）
// 生命周期：与 assignment 一致，长期记忆，不自动过期
```

---

## TODO 5: Finding vs Discovery 命名 ✅ 已确认合理

### 用户质疑

Finding 和 Discovery 都是指"发现"，在 liusha 中都指漏洞，是否需要统一命名？

### 最终结论：保持现状，双命名合理

**理由**：

1. **不同层次的抽象**
   - Finding = 数据库持久化层（业务实体）
   - Discovery = 世界模型节点层（认知状态）
   - 类比：MySQL 的 Row vs 领域模型的 Entity

2. **Finding 是行业标准**
   - CVE、OWASP、Burp、Nessus 都用 Finding
   - 改名会破坏业务领域的一致性
   - 改动成本高（数据库表、API、前端）

3. **Discovery 更通用**
   - 世界模型中不仅有漏洞，还可能有凭据、立足点等
   - Discovery 涵盖所有"重要发现"
   - 不限于漏洞（security finding）

4. **转换路径清晰**
   ```
   LLM write_finding → finding 表 (VulnFinding)
                        ↓ (可选)
                    Verifier 验证
                        ↓
                 Discovery 节点（已验证）
   ```

### 代码验证

**Finding（数据库层）**：
```go
// internal/finding/model.go
type VulnFinding struct {
    ID              string
    TaskID          string
    Severity        string
    Summary         string
    Evidence        json.RawMessage
    CWEID           string
    OWASPCategory   string
    Status          string  // triage 处置态
    ...
}
```

**Discovery（世界模型层）**：
```go
// internal/worldmodel/model.go
const (
    KindObjective   NodeKind = "objective"
    KindMove        NodeKind = "move"
    KindObservation NodeKind = "observation"
    KindDiscovery   NodeKind = "discovery"  // 重要发现
)
```

### 架构说明

```
层次划分：
- 业务层（Finding）: 交付报告、漏洞管理、Triage 处置
- 认知层（Discovery）: 知识图谱、推理依赖、因果关系

数据流：
1. LLM 通过 write_finding 工具写入 finding 表
2. Verifier 可选地验证 finding，晋升为 Discovery 节点
3. Finding 可以独立存在（不一定进入世界模型）
4. Discovery 节点可能来源于 Finding，也可能来自其他途径
```

**结论**：双命名是合理的架构分层，统一反而会造成混淆。

---

## 最终总结

| 问题 | 结论 | 状态 |
|------|------|------|
| TODO 1: Actor 层架构 | 保持现状，职责分离合理 | ✅ 无需修改 |
| TODO 2: Assignment/Task 语义 | 世界模型=task 维度，Lead=assignment 维度 | ✅ 无需修改 |
| TODO 3: Complexity 分级 | Move 通用，Complexity 是策略选择器 | ✅ 无需修改 |
| TODO 4: Lead 注释 | 已修正为"长期记忆、PostgreSQL" | ✅ 已完成 |
| TODO 5: Finding/Discovery 命名 | 不同层次，双命名合理 | ✅ 无需修改 |

**架构验证完成**：当前设计经得起推敲，逻辑自洽，概念命名优雅。

---

## 相关文件清单

### 已修改文件
- `cmd/runner/main.go` - Lead 初始化注释修正
- `internal/lead/model.go` - 包注释修正

### 架构关键文件
- `internal/actor/types.go` - Actor 层类型定义
- `internal/worldmodel/model.go` - 世界模型核心
- `internal/dispatcher/profile/profiles.go` - Complexity Profile
- `internal/finding/model.go` - Finding 业务实体
- `cmd/runner/handler.go` - onboard 逻辑

### 参考文档
- `docs/COMPLETION-SUMMARY.md` - 世界模型统一重构总结
- `db/migrations/0121_*.sql` - 世界模型 schema
