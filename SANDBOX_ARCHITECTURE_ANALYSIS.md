# 🔍 Sandbox 架构分析

## 问题：hunter_id vs executor_id vs agent_id

### 错误现象
```
sandbox /exec: status 400: hunter_id required (subtask swarm 按 hunter 切目录隔离)
```

---

## 架构分析

### 1️⃣ 当前代码（最新）

#### ExecRequest 结构
```go
type ExecRequest struct {
    ExecutorID     string `json:"agent_id"`    // ❌ 字段名混乱
    Command        string `json:"command"`
    TimeoutSeconds int    `json:"timeout_seconds"`
    Tag            string `json:"tag,omitempty"`
}
```

#### Sandbox Server 验证
```go
if req.ExecutorID == "" {
    writeError(w, http.StatusBadRequest, 
        "executor_id required (subtask swarm 按 executor 切目录隔离)")
}
```

#### 文件隔离逻辑
```go
outputDir := filepath.Join(outputDirRoot, req.ExecutorID)  // /tmp/sandbox-output/<ExecutorID>/
workdir   := filepath.Join(workdirRoot, req.ExecutorID)    // /workspace/<ExecutorID>/
```

**设计意图**：按 **ExecutorID** 隔离不同 Agent 的工作目录

---

### 2️⃣ 运行中的 Sandbox Server（旧版）

返回错误：
```
hunter_id required (subtask swarm 按 hunter 切目录隔离)
```

**说明**：运行的是旧版代码，还在使用 `hunter_id` 概念

---

### 3️⃣ Handler 层（调用方）

#### Sandbox 生命周期管理
```go
// handleCognition
var assignmentID string
if tk, err := h.tasks.GetByID(ctx, taskID); err == nil {
    assignmentID = tk.AssignmentID
}

// Sandbox 按 Assignment 粒度管理
sandboxClient, err := h.sandboxMgr.Acquire(ctx, assignmentID)
defer h.sandboxMgr.Release(context.Background(), assignmentID)
```

**关键**：Sandbox 的生命周期确实是 **Assignment**，不是 hunter！

#### 工具调用
```go
// run_command 工具
sandbox.ExecRequest{
    ExecutorID:     deps.ExecutorID,  // ← 传递 ExecutorID
    Command:        args.Command,
    TimeoutSeconds: timeout,
    Tag:            args.Tag,
}
```

**ExecutorID** = Agent 的 ID，用于隔离不同 Agent 的工作目录

---

## 🎯 正确的架构理解

### 概念层级
```
Assignment (任务分配)
  ↓
Sandbox Container (1个容器，生命周期 = Assignment)
  ↓
多个 Executor/Agent (Planner, Exploitation 等)
  ↓
每个 Agent 有独立的工作目录
  - /tmp/sandbox-output/<ExecutorID>/
  - /workspace/<ExecutorID>/
```

### 隔离维度

1. **Container 隔离** - 按 **AssignmentID**
   - 一个 Assignment → 一个 Sandbox 容器
   - 多个 Task 可能共享同一个 Assignment

2. **目录隔离** - 按 **ExecutorID**
   - 同一容器内，不同 Agent 有独立工作目录
   - 避免 Planner 和 Exploitation 的文件互相覆盖

---

## ❌ 问题根源

### 1. 旧版 Sandbox Server 还在运行
- 代码已更新为 `executor_id`
- 但运行的容器还是旧版，使用 `hunter_id`

### 2. JSON 字段名不一致
```go
ExecutorID string `json:"agent_id"`  // ❌ 应该是 "executor_id"
```

**应该改为**：
```go
ExecutorID string `json:"executor_id"`
```

或者保持 `agent_id`，但 server 端也要匹配。

---

## ✅ 修复方案

### 方案 1：重新部署 Sandbox Server（推荐）

1. 重新构建 sandbox-server 镜像
2. 重启所有 sandbox 容器
3. 确保使用最新代码

### 方案 2：统一字段名

#### 选项 A：统一使用 `executor_id`
```go
// internal/sandbox/types.go
type ExecRequest struct {
    ExecutorID     string `json:"executor_id"`  // ✅ 统一
    Command        string `json:"command"`
    TimeoutSeconds int    `json:"timeout_seconds"`
    Tag            string `json:"tag,omitempty"`
}
```

#### 选项 B：统一使用 `agent_id`
```go
// internal/sandbox/server/exec.go
if req.ExecutorID == "" {
    writeError(w, http.StatusBadRequest, 
        "agent_id required ...")  // ✅ 匹配 JSON 字段名
}
```

---

## 🎯 推荐方案

**方案 1（重新部署）+ 选项 A（统一为 executor_id）**

理由：
1. ✅ `ExecutorID` 语义更清晰（是 Agent/Executor 的 ID）
2. ✅ 与代码中的 Go 字段名一致
3. ✅ 与错误消息一致
4. ✅ 避免与旧的 `agent_id` 混淆（task.agent_id 是另一个概念）

---

## 📋 待确认

1. **Assignment 是什么？**
   - 从代码看，Task 有 `AssignmentID` 字段
   - 多个 Task 可能共享同一个 Assignment
   - 需要查看 `task.assignment_id` 的业务含义

2. **为什么不直接用 AssignmentID 做隔离？**
   - 因为同一 Assignment 内可能有多个 Agent（Planner, Exploitation）
   - 需要 ExecutorID 进一步细分隔离

3. **hunter 的历史**
   - 旧架构概念，已废弃
   - 应该全面删除，改为 ExecutorID
