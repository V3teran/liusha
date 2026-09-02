# 🔍 Sandbox 目录结构深度分析

## 你提出的4个问题

---

## 1️⃣ agent_id vs executor_id？

### 当前状态
```go
// worker.Payload
type Payload struct {
    ExecutorID string `json:"executor_id"`  // 从 Redis 队列传入
    TaskID     string `json:"task_id"`
    ...
}

// sandbox.ExecRequest
type ExecRequest struct {
    ExecutorID string `json:"executor_id"`  // 刚修复为此
    ...
}
```

### 你的观点：Planner 不是 Executor，用 agent_id 更合理

**✅ 完全正确！**

#### 实际情况
- **Planner** 是一个 Agent（不是 Executor）
- **Exploitation Agent** 也是 Agent
- **ExecutorID** 这个名字确实有误导性

#### 建议
**统一改为 `agent_id`**：
```go
type ExecRequest struct {
    AgentID    string `json:"agent_id"`  // ✅ 更准确
    Command    string `json:"command"`
    ...
}
```

理由：
1. ✅ 语义更准确（Planner、Exploitation 都是 Agent）
2. ✅ 与 agent 表的主键一致
3. ✅ 避免与 Executor（执行层）概念混淆

---

## 2️⃣ 是否还需要 task_id 或 assignment_id？

### 当前设计
```
/tmp/sandbox-output/<ExecutorID>/
/workspace/<ExecutorID>/
```

### 你的问题：是否需要更细粒度的隔离？

让我分析实际场景：

#### Scenario A: 单 Task 单 Agent
```
Task-123
  └── Agent-A (Planner)
      └── /workspace/agent-a/
```
**当前设计 ✅ 足够**

#### Scenario B: 单 Task 多 Agent (当前架构)
```
Task-123
  ├── Agent-A (Planner)
  │   └── /workspace/agent-a/
  └── Agent-B (Exploitation)
      └── /workspace/agent-b/
```
**当前设计 ✅ 足够**（按 Agent 隔离）

#### Scenario C: 多 Task 共享 Sandbox (Assignment 模式)
```
Assignment-XYZ (共享1个容器)
  ├── Task-123
  │   └── Agent-A
  │       └── /workspace/agent-a/  ❌ 会混淆！
  └── Task-456
      └── Agent-A (同名！)
          └── /workspace/agent-a/  ❌ 冲突！
```

### 🎯 结论：需要更完整的隔离！

**建议的目录结构**：
```
/tmp/sandbox-output/<assignment_id>/<task_id>/<agent_id>/
/workspace/<assignment_id>/<task_id>/<agent_id>/
```

或者简化为（如果 agent_id 全局唯一）：
```
/tmp/sandbox-output/<agent_id>/
/workspace/<agent_id>/
```

**关键问题**：
- ❓ `agent_id` 是全局唯一的 UUID 吗？
- ❓ 还是只是 agent 模板的 code（如 "planner"、"exploitation"）？

如果是模板 code，则**必须**加 task_id：
```
/workspace/<task_id>/<agent_code>/
```

---

## 3️⃣ /home/hunter/ 是什么？

### 代码证据
```go
// internal/sandbox/server/exec.go
cmd.Env = append(os.Environ(),
    "OUTPUT_DIR="+outputDir,
    "HUNTER_ID="+req.ExecutorID,  // ← 仍在使用 HUNTER_ID！
)
```

### 分析

1. **注释说明**：
   ```go
   // browser-use wrapper（每 agent 独立 tab）用此区分
   ```

2. **推测用途**：
   - 浏览器的用户数据目录（cookies、localStorage、session）
   - 可能还有其他工具的配置文件

3. **问题**：
   - ❌ `/home/hunter/` 是固定路径，所有 Agent 共享
   - ❌ 如果真的需要"每 agent 独立 tab"，为什么不按 HUNTER_ID 隔离？

### 🎯 结论：设计不一致！

**当前矛盾**：
- 代码说要"每 agent 独立 tab"
- 但 `/home/hunter/` 没有按 agent_id 隔离
- 导致所有 Agent 共享同一个浏览器 profile

**可能的意图**：
- ✅ **共享登录状态**：Planner 登录后，Exploitation 可以直接用
- ❌ **但会互相干扰**：并发操作时会冲突

**建议**：
1. **如果要共享**：保持 `/home/hunter/`，但文档说明清楚
2. **如果要隔离**：改为 `/home/hunter/<agent_id>/`

---

## 4️⃣ 功能是否重复？

### 当前三个目录
```
1. /tmp/sandbox-output/<agent_id>/  - 命令产出的附件
2. /workspace/<agent_id>/           - 命令执行的 cwd
3. /home/hunter/                    - 共享的用户数据
```

### 功能对比

| 目录 | 用途 | 隔离级别 | 典型内容 |
|------|------|----------|----------|
| OUTPUT_DIR | 工具产出 | 按 agent | 截图、抓包、扫描报告 |
| workspace | 工作目录 | 按 agent | 临时文件、脚本 |
| /home/hunter | 用户数据 | **共享** | cookies、配置 |

### 🎯 结论：不重复，但设计有问题

**正常的设计应该是**：
```
/workspace/<agent_id>/          - 工作目录（cwd）
  ├── output/                   - 产出文件（替代 /tmp/sandbox-output）
  ├── temp/                     - 临时文件
  └── .profile/                 - 用户数据（替代 /home/hunter）
      ├── .chrome/
      └── .config/
```

**当前设计的问题**：
1. ❌ 三个独立目录，管理复杂
2. ❌ `/home/hunter/` 没有隔离，共享有风险
3. ❌ 路径分散，不直观

---

## 💡 综合建议

### 推荐的新架构

```
Sandbox 容器内：
/workspace/
  └── <assignment_id>/
      └── <task_id>/
          ├── planner/              (agent_code)
          │   ├── output/           (替代 OUTPUT_DIR)
          │   ├── temp/             (工作目录)
          │   └── .profile/         (替代 /home/hunter)
          │       └── .chrome/
          └── exploitation/         (agent_code)
              ├── output/
              ├── temp/
              └── .profile/
```

### 环境变量
```go
cmd.Env = append(os.Environ(),
    "WORKSPACE_DIR=/workspace/<assignment_id>/<task_id>/<agent_code>",
    "OUTPUT_DIR=/workspace/<assignment_id>/<task_id>/<agent_code>/output",
    "PROFILE_DIR=/workspace/<assignment_id>/<task_id>/<agent_code>/.profile",
)
cmd.Dir = "/workspace/<assignment_id>/<task_id>/<agent_code>/temp"
```

### 优点
1. ✅ 结构清晰，层次分明
2. ✅ 完全隔离，无冲突风险
3. ✅ 易于清理和管理
4. ✅ 符合直觉

---

## 🔧 待确认的关键问题

### 必须先回答：

1. **agent_id 是什么？**
   - [ ] 全局唯一的 UUID（如 `550e8400-e29b-41d4-a716-446655440000`）
   - [ ] Agent 模板的 code（如 `planner`、`exploitation`）
   - [ ] Task 执行时动态生成的 ID

2. **Assignment 是什么？**
   - [ ] 一组相关的 Task
   - [ ] 一个用户会话
   - [ ] 还是其他概念？

3. **多 Task 是否共享 Sandbox？**
   - [ ] 是：需要 assignment_id + task_id 隔离
   - [ ] 否：只需要 task_id + agent_code 隔离

4. **浏览器状态是否需要共享？**
   - [ ] 需要：Planner 登录后 Exploitation 直接用
   - [ ] 不需要：每个 Agent 独立 profile

---

## 📋 建议的修复步骤

1. **澄清概念**：确认上述4个问题的答案
2. **统一命名**：`ExecutorID` → `AgentID`
3. **重新设计目录结构**：基于实际需求
4. **更新代码**：包括 sandbox-server 和 工具注册
5. **更新文档**：说明清楚每个目录的用途

---

## ✅ 关键问题的答案（已验证）

### 1. agent_id 是什么？
**答案**：✅ **全局唯一的 UUID**

证据：
```
agent_id: 480a665e-e47c-4712-a1a8-2eeda74a2e35
task_id:  650bcab6-8839-4990-851b-aed77d69c9a9
```

**含义**：每次任务执行时，动态生成一个唯一的 agent 实例 ID

### 2. Assignment 是什么？
**答案**：✅ **任务分配单元，多个 Task 可能共享一个 Assignment**

证据：
```
assignment_id: 8ebde604-f49f-48aa-8970-6baa423fb1d4
task_id:       650bcab6-8839-4990-851b-aed77d69c9a9
```

**含义**：Assignment 是 Sandbox 容器的生命周期单位

### 3. 当前目录结构是否足够？
**答案**：✅ **足够！因为 agent_id 是全局唯一的 UUID**

当前设计：
```
/tmp/sandbox-output/<agent_id>/
/workspace/<agent_id>/
```

**由于 agent_id 是 UUID，即使多个 Task 共享 Sandbox 也不会冲突！**

### 4. /home/hunter/ 的问题
**答案**：❌ **存在设计缺陷**

问题：
1. 所有 Agent 共享同一个 `/home/hunter/`
2. 浏览器 cookies/session 会互相覆盖
3. 代码中有 `HUNTER_ID` 环境变量但未使用

建议：
- 改为 `/home/hunter/<agent_id>/` 实现完全隔离
- 或者彻底删除，统一用 `/workspace/<agent_id>/.profile/`

---

## 🎯 最终建议

### ✅ 保持当前结构（简单修复）

```
/tmp/sandbox-output/<agent_id>/     ✅ UUID 保证唯一
/workspace/<agent_id>/              ✅ UUID 保证唯一
/home/hunter/<agent_id>/            ← 新增：隔离浏览器数据
```

### 修改点
1. **命名统一**：`ExecutorID` → `AgentID`
2. **添加隔离**：`/home/hunter/` → `/home/hunter/<agent_id>/`
3. **环境变量**：
   ```go
   cmd.Env = append(os.Environ(),
       "OUTPUT_DIR="+outputDir,
       "AGENT_ID="+req.AgentID,        // 重命名
       "HOME=/home/hunter/"+req.AgentID, // 隔离 home
   )
   ```

### 优点
- ✅ 最小修改
- ✅ 完全隔离
- ✅ 概念清晰
