# liusha 数据流文档

**最后更新**: 2026-08-30  
**版本**: v4.3（架构改进完成 - orchestrator/Action统一）

---

## 🔄 完整数据流

### 1. 任务创建流

```
用户提交目标
    │
    ▼
Planner 创建 worldmodel 节点
    │
    ├─ CREATE wm_node
    │    kind: "objective"
    │    content: {description: "...", target: "...", scope: [...]}
    │
    ├─ CREATE wm_node (多个)
    │    kind: "action"
    │    content: {instruction: "...", reasoning: "..."}
    │    state: "pending"
    │    depends_on: [...]
    │
    └─ CREATE wm_edge (多个)
         from_id: objective_id
         to_id: action_id
         rel_type: "supports"
```

---

### 2. Executor 执行流

```
Dispatcher 调度 action
    │
    ├─ 读取 action 节点
    │    SELECT * FROM wm_node WHERE id = ? AND kind = 'action'
    │
    ├─ 按 complexity 选择 Profile
    │    Profile = {tools: [...], budget: {...}}
    │
    └─ 创建 Executor 实例
         │
         ├─ [协程1: executeLoop] ─────────────────┐
         │    │                                    │
         │    ├─ Step 1: LLM 推理                  │
         │    │    provider.Complete(messages)    │
         │    │    → 返回 thought + tool_calls    │
         │    │                                    │
         │    ├─ 执行工具                          │
         │    │    registry.ExecuteParallel()     │
         │    │    → write_hypothesis()           │
         │    │        CREATE wm_node             │
         │    │        kind: "hypothesis"         │
         │    │        content: {statement: "..."} │
         │    │                                    │
         │    ├─ Step 2, 3, 4...                  │
         │    │                                    │
         │    └─ 记录完整 Step                     │
         │         Step {                         │
         │           Thought: "...",              │
         │           ToolCalls: [{id, name, args}], │
         │           ToolResults: [{output, error}] │
         │         }                               │
         │                                         │
         ├─ [协程2: monitorLoop] ◄────────────────┤
         │    │                                    │
         │    ├─ 每完成 5 步触发                   │
         │    │    if currentStep - lastEval >= 5 │
         │    │                                    │
         │    ├─ getRecentSteps(5)                │
         │    │                                    │
         │    ├─ selfEvaluate()                   │
         │    │    LLM 评估最近 5 步               │
         │    │    → SelfAssessment {             │
         │    │        status: "off_track",       │
         │    │        severity: "medium",        │
         │    │        correction: "..."          │
         │    │      }                            │
         │    │                                    │
         │    └─ 决策                              │
         │         if off_track + high → Kill     │
         │         if off_track + low/medium      │
         │            → correctionChan <- guidance │
         │         if stalled → Kill              │
         │                                         │
         └─ [协程3: eventLoop] ◄──────────────────┤
              │                                    │
              ├─ 订阅 EventBus                     │
              │    subscription.Events()          │
              │                                    │
              └─ 处理外部事件                      │
                   case "action.killed":          │
                      cancel()                    │
                   case "action.steered":         │
                      correctionChan <- guidance  │
                                                   │
最终结果 ────────────────────────────────────────┘
    │
    └─ ExecutorResult {
         Steps: [...],
         TokensUsed: 12345,
         Halt: "success"
       }
```

---

### 3. Planner 监察流

```
Planner 心跳循环（每6分钟）
    │
    ├─ getGlobalState()
    │    │
    │    ├─ SELECT * FROM wm_node 
    │    │    WHERE task_id = ? AND kind = 'objective'
    │    │
    │    ├─ SELECT * FROM wm_node
    │    │    WHERE task_id = ? AND kind = 'action'
    │    │
    │    └─ SELECT * FROM wm_node
    │         WHERE task_id = ? AND kind = 'finding'
    │
    ├─ evaluateGlobal()
    │    │
    │    ├─ 构建评估 prompt
    │    │    "Objective: ...\n"
    │    │    "Actions:\n"
    │    │    "  - action_1: running, goal: ...\n"
    │    │    "  - action_2: failed, goal: ...\n"
    │    │    "Findings: ...\n"
    │    │
    │    ├─ provider.Complete()
    │    │
    │    └─ 解析为 GlobalAssessment {
    │          strategy: "adjust",
    │          reasoning: "...",
    │          actions_to_kill: ["action_2"],
    │          actions_to_steer: {
    │            "action_1": "focus on authentication"
    │          },
    │          new_actions: [
    │            {goal: "explore backup mechanism"}
    │          ]
    │        }
    │
    └─ executeDecisions()
         │
         ├─ Kill(action_2, "repeated failures")
         │    │
         │    ├─ SELECT * FROM wm_node WHERE id = 'action_2'
         │    │
         │    ├─ 解析 metadata
         │    │    var meta ActionMetadata
         │    │    json.Unmarshal(node.Metadata, &meta)
         │    │
         │    ├─ 添加 KilledReason
         │    │    meta.KilledReason = {
         │    │      timestamp: now(),
         │    │      source: "planner",
         │    │      reason: "repeated failures"
         │    │    }
         │    │
         │    ├─ UPDATE wm_node
         │    │    SET metadata = ?, updated_at = now()
         │    │    WHERE id = 'action_2'
         │    │
         │    ├─ UPDATE wm_node
         │    │    SET state = 'aborted', updated_at = now()
         │    │    WHERE id = 'action_2'
         │    │
         │    ├─ EventBus.Publish({
         │    │    type: "action.killed",
         │    │    action_id: "action_2"
         │    │  })
         │    │
         │    └─ logger.Warn("planner killing action")
         │
         ├─ Steer(action_1, "focus on authentication")
         │    │
         │    ├─ SELECT * FROM wm_node WHERE id = 'action_1'
         │    │
         │    ├─ 解析 metadata
         │    │
         │    ├─ 添加 SteeringMessage
         │    │    meta.SteeringMessages = append(..., {
         │    │      timestamp: now(),
         │    │      source: "planner",
         │    │      guidance: "focus on authentication",
         │    │      applied: false
         │    │    })
         │    │
         │    ├─ UPDATE wm_node
         │    │    SET metadata = ?, updated_at = now()
         │    │    WHERE id = 'action_1'
         │    │
         │    ├─ EventBus.Publish({
         │    │    type: "action.steered",
         │    │    action_id: "action_1",
         │    │    payload: {guidance: "..."}
         │    │  })
         │    │
         │    └─ logger.Info("planner steering action")
         │
         └─ CreateAction("explore backup mechanism")
              │
              └─ INSERT INTO wm_node
                   (id, task_id, kind, content, state, priority)
                   VALUES (?, ?, 'action', ?, 'pending', 3)
```

---

### 4. 工具调用流

#### write_hypothesis

```
Executor 调用 write_hypothesis(statement: "admin panel at /admin")
    │
    ├─ Tool 验证参数
    │    require: statement (string)
    │
    ├─ 构造 content
    │    content = {
    │      statement: "admin panel at /admin",
    │      evidence: "from context"
    │    }
    │
    ├─ INSERT INTO wm_node
    │    (id, task_id, kind, content, confidence)
    │    VALUES (?, ?, 'hypothesis', ?, 'possible')
    │
    ├─ INSERT INTO wm_edge
    │    (from_id, to_id, rel_type)
    │    VALUES (action_id, hypothesis_id, 'derived_from')
    │
    └─ 返回 "Hypothesis created: hyp_abc123"
```

#### write_finding

```
Executor 调用 write_finding(
  title: "SQL Injection",
  severity: "critical"
)
    │
    ├─ Tool 验证参数
    │    require: title, severity
    │    validate: severity in [critical, high, medium, low]
    │
    ├─ 构造 content
    │    content = {
    │      title: "SQL Injection",
    │      description: "...",
    │      severity: "critical",
    │      evidence: [...],
    │      detected_at: now()
    │    }
    │
    ├─ INSERT INTO wm_node
    │    (id, task_id, kind, content, confidence)
    │    VALUES (?, ?, 'finding', ?, 'confirmed')
    │
    ├─ INSERT INTO wm_edge (可选)
    │    (from_id, to_id, rel_type)
    │    VALUES (hypothesis_id, finding_id, 'verifies')
    │
    └─ 返回 "Finding created: find_xyz789"
```

---

### 5. 事件流

```
EventBus (进程内)
    │
    ├─ subscribers: map[actionID]*subscriber
    │
    ├─ Publish(event) ─────────────────────┐
    │    for each subscriber                │
    │      if subscriber.actionID == event.ActionID
    │        subscriber.events <- event     │
    │                                        │
    └─ Subscribe(actionID) ◄────────────────┤
         │                                   │
         └─ return Subscription {           │
              events: chan Event             │
            }                                │
                                             │
Executor.eventLoop ◄────────────────────────────┘
    │
    └─ for event := range subscription.Events() {
         switch event.Type {
           case "action.killed":
             cancel()
           case "action.steered":
             correctionChan <- event.Payload["guidance"]
         }
       }
```

---

### 6. Metadata 读写流

#### 写入（Planner）

```
Planner.Kill(actionID, reason)
    │
    ├─ node := worldmodel.GetNode(actionID)
    │
    ├─ var meta ActionMetadata
    │    json.Unmarshal(node.Metadata, &meta)
    │
    ├─ meta.KilledReason = &KilledReason{
    │      Timestamp: now(),
    │      Source: "planner",
    │      Reason: reason
    │    }
    │
    ├─ metadataBytes := json.Marshal(meta)
    │
    └─ UPDATE wm_node
         SET metadata = metadataBytes
         WHERE id = actionID
```

#### 读取（Executor - 未实现）

```
Executor.applySteeringMessages(actionID)
    │
    ├─ node := worldmodel.GetNode(actionID)
    │
    ├─ var meta ActionMetadata
    │    json.Unmarshal(node.Metadata, &meta)
    │
    └─ for _, msg := range meta.SteeringMessages {
         if !msg.Applied {
           messages = append(messages, {
             role: "user",
             content: "[STEERING from " + msg.Source + "] " + msg.Guidance
           })
         }
       }

注：当前 Executor 没有 worldmodel 引用，此功能未实现
```

---

### 7. 日志流

#### Planner 日志

```json
// Kill
{
  "level": "warn",
  "time": "2026-08-29T10:30:00Z",
  "action_id": "action_abc",
  "reason": "Global assessment: repeated failures",
  "message": "planner killing action"
}

// Steer
{
  "level": "info",
  "time": "2026-08-29T10:31:00Z",
  "action_id": "action_abc",
  "guidance": "focus on port 443",
  "message": "planner steering action"
}
```

#### Executor 日志

```json
// Self Kill
{
  "level": "warn",
  "time": "2026-08-29T10:32:00Z",
  "status": "off_track",
  "severity": "high",
  "reason": "repeatedly calling same failed tool",
  "message": "executor killing itself"
}

// Self Steer
{
  "level": "info",
  "time": "2026-08-29T10:33:00Z",
  "status": "off_track",
  "severity": "medium",
  "correction": "try different approach",
  "message": "executor steering itself"
}
```

---

## 📊 数据查询流

### 查询任务进度

```sql
SELECT 
    kind,
    state,
    count(*) as count
FROM wm_node
WHERE task_id = 'task-1'
GROUP BY kind, state;

-- 结果：
-- objective, NULL, 1
-- action, running, 3
-- action, done, 5
-- action, aborted, 2
-- hypothesis, NULL, 20
-- finding, NULL, 8
```

### 查询 Kill 原因

```sql
SELECT 
    id,
    content->>'instruction' as goal,
    state,
    metadata->'killed_reason'->>'source' as killed_by,
    metadata->'killed_reason'->>'reason' as reason
FROM wm_node
WHERE task_id = 'task-1' 
  AND kind = 'action'
  AND state = 'aborted';

-- 结果：
-- action_1, "scan ports", aborted, planner, "Global assessment: repeated failures"
-- action_2, "exploit vuln", aborted, executor, "stalled: no progress in 10 steps"
```

### 查询 Steer 历史

```sql
SELECT 
    id,
    content->>'instruction' as goal,
    jsonb_array_length(metadata->'steering_messages') as steer_count
FROM wm_node
WHERE task_id = 'task-1' 
  AND kind = 'action'
  AND metadata ? 'steering_messages';

-- 结果：
-- action_3, "enumerate users", 5
-- action_4, "find credentials", 2
```

### 查询推理链

```sql
-- 从 hypothesis 到 finding 的验证链
SELECT 
    h.id as hypothesis_id,
    h.content->>'statement' as hypothesis,
    e.rel_type,
    f.id as finding_id,
    f.content->>'title' as finding
FROM wm_node h
JOIN wm_edge e ON e.from_id = h.id
JOIN wm_node f ON f.id = e.to_id
WHERE h.task_id = 'task-1'
  AND h.kind = 'hypothesis'
  AND e.rel_type = 'verifies'
  AND f.kind = 'finding';

-- 结果：
-- hyp_1, "admin panel at /admin", verifies, find_1, "Admin Interface Exposed"
-- hyp_2, "default credentials work", verifies, find_2, "Weak Authentication"
```

---

## 🔄 状态机

### Action 状态转换

```
    pending
       │
       ▼
    running ──────┐
       │          │
       ├─ done   │
       ├─ failed │
       └─ aborted ◄──── planner/executor kill
```

### Confidence 晋升

```
hypothesis (possible)
       │
       ▼
evidence collected
       │
       ▼
hypothesis (probable)
       │
       ▼
verified
       │
       ▼
finding (confirmed)
```

---

## 📈 性能数据流

### Token 消耗

```
Executor executeLoop:
  Step 1: 1500 tokens (input) + 300 tokens (output)
  Step 2: 2000 tokens (input) + 400 tokens (output)
  ...
  Total: 累积到 ExecutorResult.TokensUsed

Executor monitorLoop:
  每5步评估: ~500 tokens (input) + 100 tokens (output)

Planner evaluateGlobal:
  每6分钟: ~2000 tokens (input) + 500 tokens (output)
```

### 数据库写入

```
每个 action 执行:
  - write_hypothesis: 2-5 次 INSERT
  - write_evidence: 3-8 次 INSERT
  - write_finding: 1-3 次 INSERT
  - wm_edge: 5-15 次 INSERT

每个 Planner 心跳:
  - Kill: 2 次 UPDATE (metadata + state)
  - Steer: 1 次 UPDATE (metadata)
  - CreateAction: 1 次 INSERT
```

---

## 🎯 数据流优化

### 批量操作

```go
// 不推荐：逐个插入
for _, hyp := range hypotheses {
    worldmodel.CreateNode(ctx, hyp)
}

// 推荐：批量插入
worldmodel.CreateNodesBatch(ctx, hypotheses)
```

### 缓存查询

```go
// Planner 心跳时缓存全局状态
globalState := planner.getGlobalState(ctx, taskID)
// 在 executeDecisions 中复用，避免重复查询
```

### 事件合并

```go
// EventBus 自动合并 200ms 内的事件
// 避免频繁唤醒 Executor
```

---

## 🔍 调试数据流

### 查看 Executor 执行细节

```bash
# 查看某个 action 的所有 Step
grep "action_abc123" executor.log | jq '.step_index, .thought'

# 查看工具调用
grep "tool_call" executor.log | jq '.tool_name, .args'
```

### 查看监察决策

```bash
# Planner 决策
grep "planner killing\|planner steering" planner.log

# Executor 自我决策
grep "executor killing\|executor steering" executor.log
```

### 查看事件流

```bash
# 事件发布
grep "eventbus.*Publish" app.log

# 事件接收
grep "eventbus.*received" app.log
```

---

## 未实现的数据流（TODO）

### 1. Executor 读取 Metadata 中的 Steering 消息

**当前状态**：Planner 的 Steer 消息存到了 Metadata，但 Executor 没有读取

**需要**：
- Executor 添加 worldmodel 引用
- executeLoop 启动时调用 applySteeringMessages()

### 2. Planner 配置限流

**当前状态**：Config 定义了 MaxSteersPerCycle 等，但 executeDecisions 未使用

**需要**：
- executeDecisions 检查限流配置
- 超过限制时按优先级排序

---

**版本**: v4.0  
**状态**: ✅ 核心数据流完整  
**待完善**: Executor 读取 Metadata、Planner 限流配置
