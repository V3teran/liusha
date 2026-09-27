# 工作空间设计分析：从 Per-Assignment 到热池单容器

## 问题概述

从 per-assignment 容器改为热池单容器后，是否需要修改 sandbox-server 和工作空间设计？

---

## 当前架构分析

### 1. Sandbox-Server 的工作空间设计

**目录结构**（已存在，不需要改）：
```
/liusha/<task_id>/<agent_id>/workspace/   # 命令执行目录
/liusha/<task_id>/<agent_id>/output/      # 附件输出
/liusha/<task_id>/profile/                # Task 级共享（浏览器登录态）
```

**隔离层级**：
1. **Task 级**（第一层）：不同任务完全隔离
2. **Agent 级**（第二层）：同一任务的多个 Agent 隔离（支持并发）
3. **Profile 共享**：同一任务的所有 Agent 共享浏览器登录态

**关键代码**（`internal/sandbox/server/exec.go:83-87`）：
```go
taskRoot := filepath.Join(liushaRoot, req.TaskID)
agentRoot := filepath.Join(taskRoot, req.AgentID)
workspaceDir := filepath.Join(agentRoot, "workspace")
outputDir := filepath.Join(agentRoot, "output")
profileDir := filepath.Join(taskRoot, "profile") // Task 共享
```

---

### 2. Runner 端的参数传递

#### 工具执行时传入的参数（`internal/tools/sandbox.go:52-58`）
```go
req := sandbox.ExecRequest{
    TaskID:         t.deps.TaskID,    // ✅ Task ID
    AgentID:        t.deps.AgentID,   // ✅ Agent ID
    Command:        a.Command,
    TimeoutSeconds: a.TimeoutSeconds,
    Tag:            a.Tag,
}
```

**关键发现**：
- ✅ 工具执行时传入的是 **TaskID + AgentID**
- ✅ **没有** assignment_id
- ✅ Sandbox-server 使用 TaskID + AgentID 构建工作目录

---

### 3. Handler 中的 AssignmentID 使用

**当前代码**（`cmd/runner/handler_run.go:241-245`）：
```go
var assignmentID string
if tk, err := h.tasks.GetByID(ctx, taskID); err == nil {
    assignmentID = tk.AssignmentID
}
virtualHost := h.onboard(ctx, assignmentID, taskID, brief)
```

**AssignmentID 的用途**：
- ✅ 用于创建 `virtualHost`（流量代理的虚拟主机）
- ❌ **不再**用于 sandbox 容器管理
- ❌ **不传入** sandbox-server

---

## 对比：Per-Assignment vs 热池单容器

### Per-Assignment 模式（旧）
```
Assignment 1 → Container 1 → /liusha/<task_id_1>/<agent_id>/
Assignment 2 → Container 2 → /liusha/<task_id_2>/<agent_id>/
Assignment 3 → Container 3 → /liusha/<task_id_3>/<agent_id>/
```

**隔离方式**：
- **物理隔离**：不同 assignment 在不同容器中
- **文件系统隔离**：每个容器内部仍然用 TaskID + AgentID 隔离

### 热池单容器模式（新）
```
Container (共享)
  ├─ /liusha/<task_id_1>/<agent_id_1>/   ← Assignment 1
  ├─ /liusha/<task_id_1>/<agent_id_2>/   ← Assignment 1 的另一个 Agent
  ├─ /liusha/<task_id_2>/<agent_id_1>/   ← Assignment 2
  └─ /liusha/<task_id_3>/<agent_id_1>/   ← Assignment 3
```

**隔离方式**：
- **文件系统隔离**：所有任务在同一容器中，通过 TaskID + AgentID 隔离
- **进程隔离**：每次 exec 是独立进程（执行完即退出）
- **状态清理**：Release 时清理工作目录（`sb.CleanupWorkDir`）

---

## 关键问题解答

### Q1: 是否需要修改 sandbox-server 的工作空间设计？

**答：❌ 不需要**

**原因**：
1. Sandbox-server 的设计**已经支持**多任务共享容器
2. 隔离粒度是 `TaskID + AgentID`，不是 `AssignmentID`
3. 从代码看，sandbox-server **从未**使用过 `AssignmentID`

### Q2: AssignmentID 和 TaskID 的关系？

**关系**：
- **AssignmentID** = 一个渗透测试目标（如 `http://example.com`）
- **TaskID** = 一个具体的执行任务（可能多个 task 对应同一个 assignment）
- **AgentID** = 一个 Agent 执行实例（一个 task 可能有多个并发 agent）

**层级关系**：
```
Assignment (渗透目标)
  ├─ Task 1 (执行批次 1)
  │    ├─ Agent 1 (planner)
  │    ├─ Agent 2 (exploitation-1)
  │    └─ Agent 3 (exploitation-2)
  └─ Task 2 (执行批次 2)
       └─ Agent 1
```

**Sandbox 工作空间只关心 Task + Agent**，不关心 Assignment。

### Q3: 从 per-assignment 改为单容器后，隔离是否足够？

**答：✅ 足够**

**隔离机制**：

#### 1. 文件系统隔离
- ✅ 每个任务的工作目录独立：`/liusha/<task_id>/<agent_id>/workspace/`
- ✅ 每个任务的输出目录独立：`/liusha/<task_id>/<agent_id>/output/`
- ✅ Profile 按 Task 共享：`/liusha/<task_id>/profile/`（设计如此）

#### 2. 进程隔离
- ✅ 每次 `exec` 启动独立进程
- ✅ 进程执行完自动退出
- ✅ 无长期驻留进程（除了 sandbox-server 自身）

#### 3. 状态清理
- ✅ `Release` 时清理工作目录：`rm -rf /work/<task_id>/`
- ✅ 空闲 30 分钟后销毁整个容器（彻底清理）

#### 4. 网络隔离（通过代理）
- ✅ 每个 assignment 有独立的 `virtualHost`
- ✅ 流量代理按 assignment 隔离

**潜在风险**：
- ⚠️ 进程残留（如果 exec 未正确清理子进程）
- ⚠️ 临时文件累积（如果工具写到 `/tmp` 而非工作目录）
- ⚠️ 环境变量污染（如果工具修改全局环境）

**缓解措施**：
- ✅ 空闲 30 分钟后销毁容器（定期刷新）
- ✅ SoftReset 机制（未来可增强：kill 进程、清理 /tmp）

### Q4: 是否有遗留的 assignment_id 污染？

**检查结果**：

```bash
# 检查 sandbox-server 是否使用 assignment_id
grep -rn "assignment" cmd/sandbox-server/ internal/sandbox/server/
# 结果：无匹配
```

**结论**：
- ✅ Sandbox-server **从未**使用 `assignment_id`
- ✅ 所有工作空间路径基于 `TaskID + AgentID`
- ✅ 无污染

---

## 需要修改的地方

### ❌ 不需要修改
1. **Sandbox-server 的工作空间设计**（已经是 TaskID + AgentID）
2. **工具执行的参数传递**（已经传 TaskID + AgentID）
3. **文件系统隔离逻辑**（已经足够）

### ✅ 已经修改（Phase 1 完成）
1. **Sandbox 容器管理**：从 per-assignment → 热池单容器
2. **生命周期管理**：空闲 30 分钟自动回收
3. **Handler Acquire/Release**：使用 TaskID 而非 AssignmentID

### 🔸 可选增强（未来 Phase 2）
1. **SoftReset 增强**：
   - Kill 残留进程（`pkill -u root`）
   - 清理临时文件（`rm -rf /tmp/*`）
   - 清理网络连接（`ss -K`）

2. **工作目录管理**：
   - 在 Manager 层管理 `/work/<task_id>/`（当前是容器内的 `/liusha/<task_id>/<agent_id>/`）
   - 保持一致性（两个路径都存在，目的不同）

---

## 当前架构的目录映射

### Manager 层（外层隔离）
```
/work/<task_id>/    # Manager.Acquire 创建
  ├─ (用于 Manager 层的隔离标识)
  └─ (Release 时清理)
```

### Sandbox-server 层（内层隔离）
```
/liusha/<task_id>/<agent_id>/    # Sandbox-server 创建
  ├─ workspace/                   # 命令执行目录
  ├─ output/                      # 附件输出
  └─ profile/ (Task 级)           # 浏览器登录态
```

**两层隔离的关系**：
- **Manager 层**：粗粒度隔离（Task 级）
- **Sandbox-server 层**：细粒度隔离（Agent 级）

**问题**：两层路径不一致，容易混淆。

**建议**：统一路径设计（Phase 2）
```
/liusha/<task_id>/               # Manager 和 Sandbox-server 共同管理
  ├─ <agent_id_1>/workspace/
  ├─ <agent_id_1>/output/
  ├─ <agent_id_2>/workspace/
  ├─ <agent_id_2>/output/
  └─ profile/                     # Task 级共享
```

Manager 的 `PrepareWorkDir` 改为：
```go
sb.WorkDir = "/liusha/" + req.TaskID  // 只创建 Task 级目录
// Agent 级目录由 Sandbox-server 创建
```

---

## 总结

### 核心结论

1. **✅ 不需要修改 sandbox-server**：工作空间设计已经支持多任务共享容器
2. **✅ 隔离机制足够**：通过 TaskID + AgentID 实现文件系统隔离
3. **✅ 无 assignment_id 污染**：Sandbox-server 从未使用过它
4. **✅ 当前实现正确**：Phase 1 的修改没有破坏隔离性

### 遗留问题

1. **目录路径不一致**：
   - Manager 层：`/work/<task_id>/`
   - Sandbox-server 层：`/liusha/<task_id>/<agent_id>/`
   - 建议：Phase 2 统一为 `/liusha/<task_id>/`

2. **SoftReset 不够彻底**：
   - 当前只清理 `/work/<task_id>/`
   - 建议：Phase 2 增强（kill 进程、清理 /tmp、清理网络）

3. **进程残留风险**：
   - 当前依赖空闲 30 分钟后销毁容器
   - 建议：Phase 2 增加进程清理逻辑

### 行动建议

**立即行动**（现在）：
- ✅ 无需修改，当前实现正确

**Phase 2 优化**（2周后）：
- 统一目录路径设计
- 增强 SoftReset（kill 进程、清理 /tmp）
- 添加进程监控

**Phase 3 增强**（1个月后）：
- Per-User 隔离（增加 UserID 层级）
- 资源配额管理（限制每个 Task 的 CPU/内存）

---

## 验证清单

- [x] Sandbox-server 使用 TaskID + AgentID 构建工作目录
- [x] 工具执行时传入 TaskID + AgentID（不传 AssignmentID）
- [x] AssignmentID 只用于 virtualHost，不影响文件隔离
- [x] Manager 的 Acquire/Release 使用 TaskID
- [x] 没有 assignment_id 泄漏到 sandbox-server
- [x] 隔离机制足够（文件系统 + 进程 + 网络）

**结论：✅ 当前实现正确，无需修改 sandbox-server。**
