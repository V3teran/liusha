# 🔍 执行模式验证

## 你提出的3个问题

---

## 1️⃣ Exploitation Agent 是什么？是否命名错误？

### 代码中的证据
```go
// cmd/runner/main.go
// planner/exploitation 在沙箱里用 chromium 登录目标 + 跑 CLI 工具
// planner一旦被 asynq 路由到本进程，它派的所有exploitation也只在本进程内跑
```

### 理解
- **Planner**: 规划 Agent（制定攻击计划）
- **Exploitation**: 执行 Agent（执行具体的漏洞利用）
- 这是架构设计的术语，不是命名错误

### 架构
```
Task
  └── Planner Agent
      ├── 分析目标
      ├── 制定 Move（攻击步骤）
      └── 派生 Exploitation Agents 执行具体步骤
```

**结论**：✅ 命名正确，Exploitation 是执行层的 Agent

---

## 2️⃣ 串行执行？应该是并发执行啊

### 数据库证据

#### World Model 节点创建时间
```
id                                   | created_at
9c67486a-2478-48d6-997a-6cf1246d8354 | 2026-09-02 06:37:06.993634+00
41a206b1-e278-40fd-9cee-cf4743c09b59 | 2026-09-02 06:37:07.000482+00  ← 几乎同时
3efc59dc-5f44-4836-81de-24c512468e6b | 2026-09-02 06:37:07.002327+00  ← 几乎同时
4e20663a-72c8-4f89-aef7-fe12ba7d2ce3 | 2026-09-02 06:37:07.00449+00   ← 几乎同时
```
**多个 action 几乎同时创建** → 并发规划

#### 工具调用时间
```
agent_id                             | created_at
480a665e-e47c-4712-a1a8-2eeda74a2e35 | 2026-09-02 06:37:59.061213+00
480a665e-e47c-4712-a1a8-2eeda74a2e35 | 2026-09-02 06:38:05.303396+00
480a665e-e47c-4712-a1a8-2eeda74a2e35 | 2026-09-02 06:38:31.366526+00
```
**同一个 agent_id，不同时间** → 单个 Agent 串行执行命令

#### 只有 1 个 Agent ID
```sql
SELECT DISTINCT agent_id FROM tool_invocation WHERE task_id = '...'
→ 只有 1 个 agent_id
```

### 结论

**当前实际情况**：
- ❌ 不是多 Agent 并发
- ✅ 是单个 Agent 串行执行多个 action

**可能的并发**：
- Action 规划是并发的（World Model 层面）
- 但执行是由单个 Agent 串行完成的

---

## 3️⃣ 如果并发，是否应该保留 agent_id？

### 情况 A：当前架构（单 Agent 串行）

**实际**：
```
Task-123
  └── Agent-1 (UUID: 480a665e...)
      ├── 执行 action-1
      ├── 执行 action-2
      ├── 执行 action-3
      └── ...
```

**需要 agent_id 吗？**
- Workspace: 只有 1 个 Agent，不需要隔离
- Output: 只有 1 个 Agent，不需要隔离
- Profile: 只有 1 个 Agent，共享没问题

**结论**：❌ 不需要 agent_id

---

### 情况 B：如果未来多 Agent 并发

**假设**：
```
Task-123
  ├── Planner Agent (UUID: aaa-111) [同时运行]
  └── Exploitation Agent (UUID: bbb-222) [同时运行]
```

**问题 1：并发写文件冲突**
```bash
# Planner
nmap -oN scan.txt target.com

# Exploitation (同时)
nmap -oN scan.txt target.com/admin
# 文件内容混乱！
```

**问题 2：无法区分产出归属**
```
/liusha/task-123/output/
  └── scan.txt  ← 是 Planner 的？还是 Exploitation 的？
```

**如果有 agent_id 隔离**：
```
/liusha/task-123/
  ├── planner-uuid/output/scan.txt      ← 清晰
  └── exploitation-uuid/output/scan.txt ← 清晰
```

**结论**：✅ 如果并发，需要 agent_id

---

## 🔍 检查代码：是否有多 Agent 并发？

### 需要检查的地方

1. **Worker Payload**: 是否有多个 agent_id？
2. **Agent 启动**: 是否并发启动多个 Agent？
3. **Sandbox 管理**: 是否支持多个 Agent 共享容器？

### 代码注释中的线索
```go
// cmd/runner/main.go
// planner一旦被 asynq 路由到本进程，
// 它派的所有exploitation也只在本进程内跑
```

**理解**：
- Planner 派生 Exploitation
- 它们在同一进程内运行
- 可能共享 Sandbox 容器

### Sandbox Server 的注释
```go
// v1.4 subtask swarm：planner / exploitation 共享同一容器
// （避免账号 cookie 顶掉），但planner / exploitation 并发跑命令会
// 互相串扰——modtime 过滤无法分清"planner 刚写的 vs exploitation 刚写的"；
// wget -O ./x.html 类命令会互覆。
```

**关键信息**：
- ✅ v1.4 确实有 "subtask swarm"
- ✅ planner / exploitation 共享容器
- ✅ 并发跑命令会互相串扰
- ✅ 文件会互相覆盖

**结论**：✅ 架构设计确实支持多 Agent 并发！

---

## 🎯 最终结论

### 1. Exploitation Agent 是什么？
**答**：执行层的 Agent，由 Planner 派生，执行具体的漏洞利用步骤。命名正确。

### 2. 串行还是并发？
**答**：
- **当前测试数据**：只有 1 个 Agent（串行执行多个 action）
- **架构设计**：支持多 Agent 并发（planner + exploitation）
- **代码注释明确说明**：v1.4 subtask swarm 并发会互相串扰

### 3. 是否需要 agent_id？
**答**：✅ **需要！**

**理由**：
1. ✅ 架构设计支持多 Agent 并发
2. ✅ 代码注释明确提到并发串扰问题
3. ✅ 文件隔离是解决串扰的正确方案
4. ✅ 即使当前测试只有单 Agent，未来会有多 Agent

---

## 🏗️ 正确的架构

### 目录结构
```
/liusha/<task_id>/
  ├── profile/              ← Task 共享（浏览器登录态）
  └── <agent_id>/
      ├── workspace/        ← Agent 隔离（避免并发冲突）
      └── output/           ← Agent 隔离（清晰归属）
```

### ExecRequest
```go
type ExecRequest struct {
    TaskID         string `json:"task_id"`     // 用于 profile 共享
    AgentID        string `json:"agent_id"`    // 用于 workspace/output 隔离
    Command        string `json:"command"`
    TimeoutSeconds int    `json:"timeout_seconds"`
    Tag            string `json:"tag,omitempty"`
}
```

### 环境变量
```go
agentRoot := filepath.Join(liushaRoot, req.TaskID, req.AgentID)
workspaceDir := filepath.Join(agentRoot, "workspace")
outputDir := filepath.Join(agentRoot, "output")
profileDir := filepath.Join(liushaRoot, req.TaskID, "profile")  // Task 共享

cmd.Env = append(os.Environ(),
    "LIUSHA_TASK_ID="+req.TaskID,
    "LIUSHA_AGENT_ID="+req.AgentID,
    "LIUSHA_WORKSPACE="+workspaceDir,
    "LIUSHA_OUTPUT="+outputDir,
    "LIUSHA_PROFILE="+profileDir,
    "HOME="+profileDir,
)
cmd.Dir = workspaceDir
```

---

## 📊 对比修正

| 维度 | 我之前的判断 | 实际情况 |
|------|------------|---------|
| 执行模式 | ❌ 串行 | ✅ 支持并发 |
| Agent 数量 | ❌ 单个 | ✅ 多个（planner + exploitation） |
| 需要 agent_id | ❌ 不需要 | ✅ 需要 |

---

## ✅ 最终确认

**保留 task_id + agent_id 的完整隔离方案**

理由：
1. ✅ 代码注释明确提到并发串扰
2. ✅ v1.4 subtask swarm 设计就是多 Agent
3. ✅ 即使当前测试是单 Agent，架构必须支持多 Agent
4. ✅ agent_id 隔离是解决并发冲突的正确方案

