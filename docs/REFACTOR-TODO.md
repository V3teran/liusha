# 重构待办事项

**创建时间**: 2026-08-27  
**状态**: 待处理

---

## 1. Actor 层架构冗余问题 ⚠️ 高优先级

### 问题描述
当前架构存在过渡期冗余：
- **世界模型 Move**：已统一为通用 `wm_node (kind=move)`，有 `complexity` 字段
- **Actor Move**：`internal/actor/types.go` 中仍有独立的 `Move` 结构，带 `Complexity` 字段
- **Dispatcher Profile**：按 `Complexity` 分档（trivial/simple/moderate/complex/extreme）

### 当前使用情况
```
worldmodel.Node (kind=move) 
  → ExecutionLoop 读取
  → 转换为 actor.Move （handler_run.go:nodeToActorMove）
  → Dispatcher.Run(move) 
  → 按 move.Complexity 选 Profile
```

### 疑问
1. **Actor Move 是否冗余？** 
   - 世界模型 Move 已经通用，为什么还需要 Actor 层的 Move 结构？
   - 是否可以直接用 `worldmodel.Node` 传递？

2. **MoveKind 是否冗余？**
   - 之前重构说"Move 是通用的"，为什么还需要 `actor.MoveKind`？
   - `Complexity` 已经区分了任务难度，`MoveKind` 的作用是什么？

### 建议方向
- [ ] 明确 Actor Move 的必要性
- [ ] 如果冗余，考虑删除 `actor.Move`，直接用 `worldmodel.Node`
- [ ] 重新审视 `MoveKind` 的设计意图

---

## 2. AssignmentID vs TaskID 混用问题 ⚠️ 高优先级

### 问题描述
当前代码中 `assignmentID` 和 `taskID` 存在混用，需要明确边界。

### 架构设计
- **Assignment**：任务集合（一次测试任务）
  - 一个 Assignment 可包含**多个 Task**（多目标）
  - 一个 Assignment 也可能只有**一个 Task**（单目标）
- **Task**：单个测试目标
  - 每个 Task 代表一个独立的测试目标（如一个域名）

### 当前混用位置
需要逐一排查以下场景：

#### 2.1 世界模型键
```go
// wm_node.task_id 应该是 assignmentID 还是 taskID？
worldmodel.Node{
    TaskID: ???  // 当前可能混用
}
```

**决策**：世界模型应该按 **Assignment** 隔离（一次测试任务共享一个图）
- [ ] 重命名 `wm_node.task_id` → `wm_node.assignment_id`
- [ ] 更新所有世界模型查询接口

#### 2.2 Lead 黑板键
```go
// lead 表的 assignment_id 是正确的（已按 assignment 隔离）
// 但代码中可能有地方传错了
```
- [ ] 排查所有 `lead.Store` 调用，确保传入 `assignmentID`

#### 2.3 ExecutionLoop
```go
// ExecutionLoop 轮询 Move 时用的键
func (l *Loop) Run(ctx context.Context, taskID string)
```
- [ ] 明确 `taskID` 参数是 Task.ID 还是 Assignment.ID
- [ ] 如果是后者，重命名为 `assignmentID`

#### 2.4 Handler 层
```go
// handler.handle 入口
func (h *handler) handle(ctx context.Context, t *asynq.Task)
```
- [ ] 排查 Task 到 Assignment 的映射逻辑
- [ ] 确保向下传递正确的 ID

### 排查清单
- [ ] `internal/worldmodel/store.go` - 所有方法的 `taskID` 参数
- [ ] `internal/cognition/execution_loop.go` - Run 方法
- [ ] `internal/lead/store.go` - 所有方法的 `assignmentID` 参数
- [ ] `cmd/runner/handler.go` - onboard 和 handle 逻辑
- [ ] `internal/planneragent/agent.go` - PlannerAgent 初始化

---

## 3. Finding vs Discovery 命名 ✅ 无需修改

### 结论
经过分析，Finding 和 Discovery **不应该统一**，它们服务不同层次：

- **VulnFinding** (`internal/finding`)：报告层的漏洞记录
  - 用户可见的漏洞列表
  - 有 severity/summary/evidence/repro 等字段
  - 按 Task 记录

- **KindDiscovery** (`worldmodel.Node`)：世界模型的知识节点
  - 内部推理用的坐实发现
  - 经过 Verifier 验证后晋升
  - 按 Assignment 记录

保持当前设计，无需修改。

---

## 4. 已完成事项 ✅

### Step 1: 删除旧 Complexity 相关代码
- ✅ 删除 `internal/config/agent/tier.go`（旧 Tier 概念）
- ✅ 更新 `model.go` 和 `store.go` 引用
- ✅ 编译通过

### Step 2: 删除 ingester 包
- ✅ 删除 `internal/ingester/` 包（过渡期遗留）
- ✅ 编译通过

### Step 3: 清理旧 NodeKind 残留
- ✅ `KindTarget` → `KindObjective`
- ✅ `KindFinding` → `KindDiscovery`
- ✅ 更新所有测试文件
- ✅ 编译通过

### Step 4: 删除 Lead TTL 配置
- ✅ Lead 已改为 PostgreSQL 持久化，按 assignment 隔离
- ✅ 删除 `LeadTTLHours` 配置项
- ✅ 删除 config.yaml 中的 `lead_ttl_hours`
- ✅ 编译通过

---

## 优先级

1. **高优先级** - 影响架构清晰度和正确性
   - [ ] Actor 层冗余问题（问题 1）
   - [ ] AssignmentID vs TaskID 混用（问题 2）

2. **已完成** - 清理工作
   - [x] 旧 Complexity 代码
   - [x] ingester 包
   - [x] 旧 NodeKind
   - [x] Lead TTL

3. **无需处理** - 设计合理
   - [x] Finding vs Discovery（保持分离）

---

## 下一步行动

1. **先理清概念**：与团队讨论 Actor Move 的设计意图
2. **排查混用**：建立 AssignmentID/TaskID 使用规范
3. **逐步重构**：在明确设计后再动手修改
4. **保持测试**：每步修改后运行集成测试

---

**备注**：本文档记录架构审查中发现的待办事项，优先级基于对系统正确性和可维护性的影响。
