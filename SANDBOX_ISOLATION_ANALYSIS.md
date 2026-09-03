# 🔍 Sandbox 隔离粒度严谨分析

## 核心问题：只需要 agent_id 吗？

---

## 📊 数据库关系分析

### 已验证的数据
```
task_id:       650bcab6-8839-4990-851b-aed77d69c9a9
assignment_id: 8ebde604-f49f-48aa-8970-6baa423fb1d4
agent_id:      480a665e-e47c-4712-a1a8-2eeda74a2e35
```

### 关系链
```
Assignment (1)
  └── Task (N)
      └── Agent Instance (N)
```

---

## 🎯 关键问题

### 1. agent_id 是否全局唯一？

**验证**：agent_id 是运行时生成的 UUID
- ✅ 是的，每次执行生成新的 UUID
- ✅ 不同 Task 的 Agent 有不同的 UUID
- ✅ 同一 Task 的不同 Agent 有不同的 UUID

**结论**：agent_id 天然全局唯一！

---

## 🔬 场景分析

### 场景 A：单 Task，单 Agent
```
Task-123
  └── Agent-A (UUID: aaa-111)
```
**隔离需求**：`/liusha/aaa-111/` ✅ 足够

---

### 场景 B：单 Task，多 Agent（串行）
```
Task-123
  ├── Agent-A (UUID: aaa-111) [完成]
  └── Agent-B (UUID: bbb-222) [运行中]
```
**隔离需求**：
- Agent-A: `/liusha/aaa-111/`
- Agent-B: `/liusha/bbb-222/`
- ✅ UUID 不同，天然隔离

---

### 场景 C：单 Task，多 Agent（并发）
```
Task-123
  ├── Agent-A (UUID: aaa-111) [同时运行]
  └── Agent-B (UUID: bbb-222) [同时运行]
```
**隔离需求**：
- Agent-A: `/liusha/aaa-111/`
- Agent-B: `/liusha/bbb-222/`
- ✅ UUID 不同，天然隔离

---

### 场景 D：多 Task，共享 Sandbox（Assignment）
```
Assignment-XYZ (共享 1 个容器)
  ├── Task-123
  │   └── Agent-A (UUID: aaa-111)
  └── Task-456
      └── Agent-B (UUID: bbb-222)
```
**隔离需求**：
- Agent-A: `/liusha/aaa-111/`
- Agent-B: `/liusha/bbb-222/`
- ✅ UUID 不同，天然隔离

---

### 场景 E：理论极端情况（UUID 碰撞）
```
Task-123: Agent (UUID: aaa-111)
Task-456: Agent (UUID: aaa-111)  ← UUID 碰撞（几乎不可能）
```
**概率**：UUID v4 碰撞概率 ≈ 1 / 2^122 ≈ 10^-37

**即使碰撞**：
- 不同 Task 不会共享 Sandbox 容器
- 即使共享，也是串行执行（先后顺序）
- **结论**：实际上不可能发生

---

## 🤔 为什么设计中有 assignment_id？

### Assignment 的作用

#### 1. **Sandbox 容器生命周期**
```go
sandboxClient, err := h.sandboxMgr.Acquire(ctx, assignmentID)
defer h.sandboxMgr.Release(context.Background(), assignmentID)
```
- Assignment 决定哪些 Task 共享容器
- 容器池按 `assignmentID` 管理

#### 2. **共享登录状态**
- 同一 Assignment 的多个 Task 共享浏览器 cookies
- 例如：Planner 登录后，Exploitation 直接用

#### 3. **资源管理**
- 按 Assignment 清理容器
- 按 Assignment 计费/限流

### 但是！
**这些都是容器管理层面的，与文件隔离无关！**

---

## 💡 严谨结论

### 文件隔离只需要 agent_id

**理由**：

1. **agent_id 全局唯一**（UUID）
   - 不需要 task_id 或 assignment_id 来保证唯一性

2. **清理无歧义**
   - 删除 `/liusha/aaa-111/` 只删除这个 Agent 的数据
   - 不会误删其他 Agent

3. **调试直观**
   ```bash
   ls /liusha/
   # 直接看到所有 Agent ID，清晰明了
   ```

4. **扩展性好**
   - 未来如果需要跨容器共享，只需复制目录
   - 不依赖层级结构

---

## ⚠️ 但需要保留 assignment_id 在哪里？

### 1. **ExecRequest 结构**

**当前设计**：
```go
type ExecRequest struct {
    AssignmentID string  // 用于？
    AgentID      string  // 用于文件隔离
    ...
}
```

**问题**：为什么需要 AssignmentID？

#### 可能的原因 A：未来清理
```bash
# 按 Assignment 清理所有相关 Agent
find /liusha -name "*" -path "*/assignment-xyz/*" -delete
```
❌ 但我们的设计是 `/liusha/<agent_id>/`，没有 assignment 层级！

#### 可能的原因 B：日志/审计
- 记录哪个 Assignment 执行了哪些命令
✅ 这是合理的

#### 可能的原因 C：安全隔离
- 不同 Assignment 不能访问彼此的数据
❌ 但 agent_id 已经保证了隔离！

---

## 🎯 最终建议

### 方案 A：彻底简化（推荐）

#### 目录结构
```
/liusha/<agent_id>/
    ├── workspace/
    ├── output/
    └── profile/
```

#### ExecRequest
```go
type ExecRequest struct {
    AgentID        string  // 唯一必需
    Command        string
    TimeoutSeconds int
    Tag            string
}
```

#### 优点
1. ✅ 最简单
2. ✅ agent_id 已保证唯一性
3. ✅ 无冗余层级
4. ✅ 调试直观

#### 缺点
1. ⚠️ 无法按 Assignment 批量清理
2. ⚠️ 无法按 Task 批量清理

---

### 方案 B：保留 assignment_id（仅用于元数据）

#### 目录结构（不变）
```
/liusha/<agent_id>/
    ├── workspace/
    ├── output/
    └── profile/
```

#### ExecRequest
```go
type ExecRequest struct {
    AssignmentID   string  // 仅用于日志/审计，不用于路径
    AgentID        string  // 用于文件隔离
    Command        string
    TimeoutSeconds int
    Tag            string
}
```

#### 实现
```go
// sandbox-server 只用 agent_id 构建路径
agentRoot := filepath.Join(liushaRoot, req.AgentID)  // 不用 AssignmentID

// 但记录到日志
log.Info().
    Str("assignment_id", req.AssignmentID).
    Str("agent_id", req.AgentID).
    Msg("exec command")
```

#### 优点
1. ✅ 简单的目录结构
2. ✅ 保留 Assignment 追踪能力
3. ✅ 灵活性：未来可改

#### 缺点
1. ⚠️ 多传一个字段（轻微冗余）

---

### 方案 C：保留层级结构（不推荐）

#### 目录结构
```
/liusha/<assignment_id>/<task_id>/<agent_id>/
```

#### 优点
1. ✅ 可按 Assignment/Task 批量清理

#### 缺点
1. ❌ 复杂
2. ❌ 路径太长
3. ❌ task_id 和 assignment_id 都是冗余（agent_id 已唯一）
4. ❌ 调试麻烦

---

## 📊 对比总结

| 维度 | 方案 A (仅 agent_id) | 方案 B (保留 assignment_id 元数据) | 方案 C (完整层级) |
|------|---------------------|--------------------------------|-----------------|
| 简洁性 | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐ |
| 唯一性保证 | ✅ UUID | ✅ UUID | ✅ UUID |
| 批量清理 | ❌ 无 | ❌ 无 | ✅ 有 |
| 可追踪性 | ⚠️ 需查数据库 | ✅ 日志中有 | ✅ 路径中有 |
| 路径长度 | 短 | 短 | 长 |
| 扩展性 | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ |

---

## 🎯 我的推荐：**方案 A（仅 agent_id）**

### 理由

1. **agent_id 已经全局唯一**
   - UUID 碰撞概率可忽略
   - 不需要 assignment_id 或 task_id 来保证唯一性

2. **KISS 原则**
   - 最简单的方案往往是最好的
   - `/liusha/<agent_id>/` 清晰直观

3. **批量清理不是刚需**
   - 容器销毁时整个 `/liusha/` 自动清理
   - 不需要细粒度的按 Assignment/Task 清理

4. **可追踪性通过数据库**
   - `tool_invocation.agent_id` 已经关联到 task_id
   - 需要审计时查数据库即可

### 实现

#### ExecRequest
```go
type ExecRequest struct {
    AgentID        string `json:"agent_id"`
    Command        string `json:"command"`
    TimeoutSeconds int    `json:"timeout_seconds"`
    Tag            string `json:"tag,omitempty"`
}
```

#### 目录
```
/liusha/
  └── <agent_id>/
      ├── workspace/
      ├── output/
      └── profile/
```

#### 环境变量
```go
cmd.Env = append(os.Environ(),
    "LIUSHA_AGENT_ID="+req.AgentID,
    "LIUSHA_WORKSPACE="+workspaceDir,
    "LIUSHA_OUTPUT="+outputDir,
    "LIUSHA_PROFILE="+profileDir,
    "HOME="+profileDir,
)
```

---

## ✅ 最终答案

**只需要 agent_id！**

- ❌ 不需要 assignment_id（容器管理层面的，与文件无关）
- ❌ 不需要 task_id（agent_id 已关联到 task）
- ✅ agent_id 是 UUID，全局唯一，足够了

