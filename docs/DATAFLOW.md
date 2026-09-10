# Liusha 数据流文档

**版本**: 2.0  
**更新时间**: 2024-09-08

---

## 概述

本文档描述 Liusha 系统中的数据流动路径，包括任务创建、执行、状态更新和结果收集的完整生命周期。

---

## 核心数据流

### 1. 任务创建流程

```
用户请求
   │
   ├─ POST /api/tasks
   │   {
   │     "target": "https://example.com",
   │     "scan_type": "active",
   │     "config": {...}
   │   }
   │
   ▼
HTTP API
   │
   ├─ 创建 Task 记录
   │   INSERT INTO task (id, target, status, created_at)
   │
   ├─ 初始化 WorldModel 根节点
   │   INSERT INTO wm_node (id, task_id, kind='root', state='open')
   │
   ├─ 发送到任务队列
   │   Queue.Enqueue(task_id)
   │
   ▼
返回 Task ID
   {"task_id": "task_abc123", "status": "queued"}
```

**数据实体**:
```go
// Task 记录
{
    "id": "task_abc123",
    "target": "https://example.com",
    "status": "queued",
    "created_at": "2024-09-08T10:00:00Z"
}

// WorldModel 根节点
{
    "id": "root_abc123",
    "task_id": "task_abc123",
    "kind": "root",
    "content": {"target": "https://example.com"},
    "state": "open",
    "version": 1
}
```

---

### 2. 任务调度流程

```
任务队列 (Asynq)
   │
   ├─ Worker 取出任务
   │   task_id = Queue.Dequeue()
   │
   ▼
Dispatcher
   │
   ├─ 更新任务状态
   │   UPDATE task SET status = 'running' WHERE id = task_id
   │
   ├─ 创建 Orchestrator 实例
   │   orch = orchestrator.New(config)
   │
   ▼
启动 Orchestrator
   orch.Run(ctx)
```

**状态变化**:
```
queued → running → completed/failed
```

---

### 3. Orchestrator 启动流程

```
Orchestrator.Run()
   │
   ├─ 1. 启动 Planner Agent
   │     go planner.Start(ctx)
   │
   ├─ 2. 启动 Monitor Agent
   │     go monitor.Start(ctx)
   │
   ├─ 3. 订阅事件总线
   │     eventBus.Subscribe(handlers)
   │
   ├─ 4. 进入事件循环
   │     for event := range eventBus.Events() {
   │         handleEvent(event)
   │     }
   │
   └─ 5. 等待完成信号
         <-ctx.Done()
```

---

## 规划流程（Planner → WorldModel）

### 初始规划

```
Planner Agent
   │
   ├─ 1. 读取 WorldModel
   │     nodes = world.ListByTaskID(task_id)
   │     │
   │     └─ SELECT * FROM wm_node WHERE task_id = $1
   │
   ├─ 2. 调用 LLM 规划
   │     assessment = llm.CallTool("plan_initial", {
   │         "nodes": nodes,
   │         "target": target
   │     })
   │
   ├─ 3. 生成 Actions
   │     for action in assessment.NewActions {
   │         fingerprint = computeFingerprint(action)
   │         
   │         // 去重检查
   │         exists = world.ExistsByFingerprint(task_id, fingerprint)
   │         if exists { continue }
   │         
   │         // 插入新 Action
   │         node = {
   │             "id": generateID(),
   │             "task_id": task_id,
   │             "kind": "action",
   │             "content": action,
   │             "fingerprint": fingerprint,
   │             "state": "open",
   │             "version": 1
   │         }
   │         world.Insert(node)
   │     }
   │
   └─ 4. 发布事件
         eventBus.Publish({
             "type": "action.proposed",
             "payload": {"action_ids": [...]}
         })
```

**数据流**:
```
WorldModel (读) → LLM Provider → NewActions → 去重 → WorldModel (写) → EventBus
```

**SQL 操作**:
```sql
-- 读取现有节点
SELECT * FROM wm_node WHERE task_id = $1;

-- 去重检查
SELECT id FROM wm_node 
WHERE task_id = $1 AND fingerprint = $2 AND state IN ('open', 'blocked')
LIMIT 1;

-- 插入新 Action
INSERT INTO wm_node (id, task_id, kind, content, fingerprint, state, version)
VALUES ($1, $2, 'action', $3, $4, 'open', 1);
```

---

### 增量规划

```
Monitor → Orchestrator → Planner
   │
   ├─ Monitor 发出重新规划请求
   │   eventBus.Publish({
   │       "type": "monitor.request_replan",
   │       "payload": {"reason": "进展缓慢"}
   │   })
   │
   ├─ Orchestrator 接收事件
   │   handleEvent(event)
   │
   ├─ 发送信号给 Planner
   │   planner.TriggerReplan()
   │
   └─ Planner 执行增量规划
       assessment = llm.CallTool("plan_incremental", {
           "recent_findings": recentFindings,
           "stuck_actions": stuckActions
       })
```

**触发条件**:
- Monitor 检测到进展缓慢
- 新 Findings 发现
- Actions 大量失败
- 用户手动触发

---

## 执行流程（Orchestrator → Executor → WorldModel）

### Action 执行

```
EventBus: action.proposed
   │
   ├─ Orchestrator 接收事件
   │   event = {"type": "action.proposed", "payload": {"action_ids": [...]}}
   │
   ├─ 读取 Actions
   │   actions = []
   │   for id in action_ids {
   │       node = world.GetByID(id)
   │       actions.append(node)
   │   }
   │
   ├─ CAS 状态转换: open → running
   │   for action in actions {
   │       success = world.CASTransition(
   │           action.ID, 
   │           "open", 
   │           "running", 
   │           action.Version
   │       )
   │       if !success {
   │           // 被其他实例抢占，跳过
   │           continue
   │       }
   │       runnableActions.append(action)
   │   }
   │
   ├─ 提交到 Executor Pool
   │   reports = executorPool.ExecuteAll(ctx, runnableActions)
   │
   └─ 发布完成事件
       eventBus.Publish({
           "type": "action.completed",
           "payload": {"reports": reports}
       })
```

**CAS 状态转换 SQL**:
```sql
UPDATE wm_node 
SET state = 'running', version = version + 1, updated_at = NOW()
WHERE id = $1 AND version = $2
RETURNING version;

-- 如果返回空：version 不匹配，更新失败（被其他实例修改）
-- 如果返回新 version：更新成功
```

---

### Executor Pool 执行

```
ExecutorPool.ExecuteAll(actions)
   │
   ├─ 并发执行（10 个 workers）
   │   results = make(chan Result, len(actions))
   │   
   │   for action in actions {
   │       go func(a) {
   │           coordinator = pool.Get()
   │           result = coordinator.Execute(ctx, a)
   │           results <- result
   │           pool.Put(coordinator)
   │       }(action)
   │   }
   │
   └─ 收集结果
       reports = []
       for i := 0; i < len(actions); i++ {
           result := <-results
           reports.append(result)
       }
       return reports
```

---

### Coordinator 执行详情

```
Coordinator.Execute(action)
   │
   ├─ 1. 快照运行前的 Findings
   │     before = findings.ListByTaskAndHost(task_id, host, 0)
   │     seenIDs = {f.ID for f in before}
   │
   ├─ 2. 执行 AgentFunc（调用 LLM Agent）
   │     err = agentFunc(ctx, action)
   │     │
   │     └─ LLM Agent 执行
   │         ├─ 分析 Action
   │         ├─ 生成 HTTP 请求
   │         ├─ 发送请求（保存到 Traffic）
   │         ├─ 分析响应
   │         └─ 发现漏洞时调用 write_finding
   │             findings.Insert(finding)
   │
   ├─ 3. 差集收割新 Findings
   │     after = findings.ListByTaskAndHost(task_id, host, 0)
   │     newFindings = [f for f in after if f.ID not in seenIDs]
   │
   ├─ 4. 转换为 Attempts
   │     attempts = []
   │     for f in newFindings {
   │         attempt = {
   │             "finding_id": f.ID,
   │             "action_id": action.ID,
   │             "confidence": f.Confidence
   │         }
   │         attempts.append(attempt)
   │     }
   │
   ├─ 5. 更新 Action 状态: running → done
   │     world.UpdateState(action.ID, "done")
   │
   └─ 6. 返回 Report
       return {
           "Steps": 1,
           "Promoted": 0,
           "Attempts": len(attempts),
           "StopWhy": "completed",
           "Hypotheses": extractHypotheses(attempts)
       }
```

**数据流**:
```
Action → AgentFunc → LLM → HTTP Request → Traffic Store
                                              │
                                              ▼
                                      Finding Store ← write_finding
                                              │
                                              ▼
                                        差集收割 → Attempts → Report
```

**SQL 操作**:
```sql
-- 快照前
SELECT * FROM finding WHERE task_id = $1 AND host = $2;

-- LLM Agent 写入 Traffic
INSERT INTO traffic (task_id, agent_id, method, url, req_headers, req_body, 
                     resp_status, resp_headers, resp_body)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- LLM Agent 写入 Finding
INSERT INTO finding (id, task_id, host, vuln_type, severity, title, 
                     description, payload, evidence_url, confidence)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- 快照后
SELECT * FROM finding WHERE task_id = $1 AND host = $2;

-- 更新 Action 状态
UPDATE wm_node SET state = 'done', updated_at = NOW() WHERE id = $1;
```

---

## 验证流程（Verifier → Finding）

### Hypothesis 验证

```
EventBus: action.completed
   │
   ├─ Orchestrator 接收事件
   │   reports = event.Payload["reports"]
   │
   ├─ 收集 Hypotheses
   │   hypotheses = []
   │   for report in reports {
   │       hypotheses.extend(report.Hypotheses)
   │   }
   │
   ├─ 并发验证
   │   for hypothesis_id in hypotheses {
   │       go func(hypID) {
   │           finding = verifier.Verify(ctx, hypID)
   │           if finding != nil {
   │               eventBus.Publish({
   │                   "type": "finding.verified",
   │                   "payload": {
   │                       "finding_id": finding.ID,
   │                       "hypothesis_id": hypID
   │                   }
   │               })
   │           }
   │       }(hypothesis_id)
   │   }
   │
   └─ 等待所有验证完成
       wg.Wait()
```

---

### Verifier Agent 详情

```
Verifier.Verify(hypothesis_id)
   │
   ├─ 1. 读取 Hypothesis
   │     hyp = world.GetByID(hypothesis_id)
   │
   ├─ 2. 读取相关上下文
   │     relatedTraffic = traffic.ListByHypothesis(hypothesis_id)
   │     relatedFindings = findings.ListRelated(hypothesis_id)
   │
   ├─ 3. 调用 LLM 验证
   │     result = llm.Complete({
   │         "system": "你是漏洞验证专家...",
   │         "messages": [
   │             {"role": "user", "content": buildPrompt(hyp, traffic)}
   │         ]
   │     })
   │
   ├─ 4. 解析验证结果
   │     if result.verdict == "CONFIRMED" {
   │         finding = {
   │             "id": generateID(),
   │             "task_id": hyp.TaskID,
   │             "vuln_type": result.vuln_type,
   │             "severity": result.severity,
   │             "title": result.title,
   │             "description": result.description,
   │             "confidence": 0.95
   │         }
   │         findings.Insert(finding)
   │         return finding
   │     } else {
   │         return nil  // 验证失败
   │     }
   │
   └─ 5. 更新 Hypothesis 状态
       world.UpdateConfidence(hypothesis_id, "verified")
```

**数据流**:
```
Hypothesis → 相关上下文 → LLM → 验证结果 → Finding Store
                                              │
                                              ▼
                                        EventBus (finding.verified)
```

---

## 监察流程（Monitor → Orchestrator）

### Monitor 评估

```
Monitor Agent (每 6 分钟)
   │
   ├─ 1. 读取 WorldModel 状态
   │     nodes = world.ListByTaskID(task_id)
   │     findings = findings.ListByTaskID(task_id)
   │
   ├─ 2. 统计关键指标
   │     metrics = {
   │         "total_actions": count(nodes where kind='action'),
   │         "done_actions": count(nodes where state='done'),
   │         "running_actions": count(nodes where state='running'),
   │         "total_findings": len(findings),
   │         "recent_findings": count(findings where created_at > 30min)
   │     }
   │
   ├─ 3. 调用 LLM 评估
   │     decision = llm.Complete({
   │         "system": "你是任务监察专家...",
   │         "messages": [
   │             {"role": "user", "content": buildPrompt(metrics)}
   │         ]
   │     })
   │
   ├─ 4. 解析决策
   │     if decision.action == "kill_action" {
   │         eventBus.Publish({
   │             "type": "monitor.kill_action",
   │             "payload": {
   │                 "action_id": decision.target_action_id,
   │                 "reason": decision.reason
   │             }
   │         })
   │     } else if decision.action == "request_replan" {
   │         eventBus.Publish({
   │             "type": "monitor.request_replan",
   │             "payload": {
   │                 "reason": decision.reason
   │             }
   │         })
   │     }
   │
   └─ 5. 记录日志
       logger.Info().Msg("Monitor evaluation completed")
```

**决策类型**:
- `no_action`: 一切正常，继续
- `kill_action`: 终止某个 Action
- `request_replan`: 请求重新规划

---

### Kill Action 处理

```
EventBus: monitor.kill_action
   │
   ├─ Orchestrator 接收事件
   │   action_id = event.Payload["action_id"]
   │   reason = event.Payload["reason"]
   │
   ├─ CAS 状态转换: running → aborted
   │   success = world.CASTransition(
   │       action_id,
   │       "running",
   │       "aborted",
   │       expectedVersion
   │   )
   │
   ├─ 记录原因
   │   world.UpdateBlockedReason(action_id, reason)
   │
   └─ 日志
       logger.Warn().
           Str("action_id", action_id).
           Str("reason", reason).
           Msg("Action killed by Monitor")
```

**SQL 操作**:
```sql
-- CAS 终止 Action
UPDATE wm_node 
SET state = 'aborted', 
    blocked_reason = $1, 
    version = version + 1,
    updated_at = NOW()
WHERE id = $2 AND version = $3 AND state = 'running'
RETURNING version;
```

---

## 完整数据流示例

### 端到端流程

```
1. 用户创建任务
   POST /api/tasks {"target": "https://example.com"}
   │
   └─ INSERT INTO task (id, target, status)
   └─ INSERT INTO wm_node (id, task_id, kind='root')
   └─ Queue.Enqueue(task_id)

2. Worker 启动 Orchestrator
   task_id = Queue.Dequeue()
   │
   └─ orch = orchestrator.New(config)
   └─ orch.Run(ctx)

3. Planner 初始规划
   Planner.Start()
   │
   └─ nodes = SELECT * FROM wm_node WHERE task_id = $1
   └─ assessment = LLM.plan_initial(nodes)
   └─ INSERT INTO wm_node (kind='action', fingerprint=...)  × 5
   └─ EventBus.Publish("action.proposed")

4. Orchestrator 执行 Actions
   handleEvent("action.proposed")
   │
   └─ actions = SELECT * FROM wm_node WHERE id IN (...)
   └─ UPDATE wm_node SET state='running' WHERE id=... AND version=...  × 5
   └─ executorPool.ExecuteAll(actions)
       │
       ├─ Worker 1: Execute action 1
       │   ├─ before = SELECT * FROM finding
       │   ├─ agentFunc() → LLM → HTTP Requests
       │   │   └─ INSERT INTO traffic (...)  × 10
       │   │   └─ INSERT INTO finding (vuln_type='XSS', ...)
       │   ├─ after = SELECT * FROM finding
       │   ├─ newFindings = after - before
       │   └─ UPDATE wm_node SET state='done'
       │
       ├─ Worker 2: Execute action 2
       │   └─ ...
       │
       └─ reports = [report1, report2, ...]
   
   └─ EventBus.Publish("action.completed", reports)

5. Verifier 验证 Hypotheses
   handleEvent("action.completed")
   │
   └─ hypotheses = [hyp1, hyp2, ...]
   └─ for hyp in hypotheses:
       ├─ hyp = SELECT * FROM wm_node WHERE id = hyp_id
       ├─ traffic = SELECT * FROM traffic WHERE ...
       ├─ result = LLM.verify(hyp, traffic)
       ├─ if confirmed:
       │   └─ INSERT INTO finding (confidence=0.95, ...)
       │   └─ EventBus.Publish("finding.verified")
       └─ UPDATE wm_node SET confidence='verified'

6. Monitor 评估
   time.Sleep(6 * time.Minute)
   │
   └─ metrics = {
       "total_actions": SELECT COUNT(*) FROM wm_node WHERE kind='action',
       "done_actions": SELECT COUNT(*) WHERE state='done',
       "findings": SELECT COUNT(*) FROM finding
   }
   └─ decision = LLM.evaluate(metrics)
   └─ if decision == "request_replan":
       └─ EventBus.Publish("monitor.request_replan")

7. Planner 增量规划
   handleEvent("monitor.request_replan")
   │
   └─ recentFindings = SELECT * FROM finding WHERE created_at > ...
   └─ assessment = LLM.plan_incremental(recentFindings)
   └─ INSERT INTO wm_node (kind='action', ...)  × 3
   └─ EventBus.Publish("action.proposed")

8. 重复 4-7 直到任务完成

9. 任务完成
   Monitor 检测到完成条件
   │
   └─ EventBus.Publish("task.completed")
   └─ UPDATE task SET status='completed', completed_at=NOW()
   └─ orch.Stop()
```

---

## 数据流图

### 主要数据流动

```
                    ┌─────────────┐
                    │   用户请求   │
                    └──────┬──────┘
                           │
                    ┌──────▼──────┐
                    │  HTTP API   │
                    └──────┬──────┘
                           │
        ┌──────────────────┼──────────────────┐
        │                  │                  │
   ┌────▼────┐      ┌─────▼─────┐     ┌─────▼─────┐
   │  Task   │      │   Queue   │     │WorldModel │
   │  Store  │      │  (Asynq)  │     │   Store   │
   └─────────┘      └─────┬─────┘     └───────────┘
                           │
                    ┌──────▼──────┐
                    │ Dispatcher  │
                    └──────┬──────┘
                           │
                    ┌──────▼──────┐
                    │Orchestrator │
                    └──────┬──────┘
                           │
        ┌──────────────────┼──────────────────┬──────────┐
        │                  │                  │          │
   ┌────▼────┐      ┌─────▼─────┐     ┌─────▼─────┐ ┌──▼──────┐
   │ Planner │      │  Monitor  │     │ Executor  │ │Verifier │
   │  Agent  │      │   Agent   │     │   Pool    │ │  Agent  │
   └────┬────┘      └─────┬─────┘     └─────┬─────┘ └────┬────┘
        │                 │                  │            │
        │                 │                  │            │
        └─────────────────┼──────────────────┼────────────┘
                          │                  │
                   ┌──────▼──────┐    ┌──────▼──────┐
                   │  EventBus   │    │    LLM      │
                   │             │    │  Provider   │
                   └─────────────┘    └─────────────┘
```

### 数据存储关系

```
┌───────────────────────────────────────────────────┐
│              PostgreSQL Database                  │
├───────────────────────────────────────────────────┤
│                                                   │
│  ┌──────────┐         ┌──────────┐              │
│  │   task   │◄────────│ wm_node  │              │
│  └──────────┘         └─────┬────┘              │
│       │                     │                    │
│       │                     │                    │
│  ┌────▼────┐           ┌────▼────┐              │
│  │ finding │           │ traffic │              │
│  └─────────┘           └─────────┘              │
│       │                                          │
│       │                                          │
│  ┌────▼────────┐                                │
│  │ verifier_   │                                │
│  │ attempt     │                                │
│  └─────────────┘                                │
│                                                   │
└───────────────────────────────────────────────────┘
```

---

## 并发数据流

### 多实例执行

```
实例 1                    实例 2                    实例 3
   │                         │                         │
   ├─ 读取 Actions           ├─ 读取 Actions           ├─ 读取 Actions
   │  (id1, id2, id3)        │  (id1, id2, id3)        │  (id1, id2, id3)
   │                         │                         │
   ├─ CAS: id1 open→running  ├─ CAS: id1 open→running  ├─ CAS: id1 open→running
   │  ✅ 成功 (version=1)    │  ❌ 失败 (version≠1)    │  ❌ 失败 (version≠1)
   │                         │                         │
   ├─ CAS: id2 open→running  ├─ CAS: id2 open→running  ├─ CAS: id2 open→running
   │  ❌ 失败                │  ✅ 成功 (version=1)    │  ❌ 失败
   │                         │                         │
   ├─ CAS: id3 open→running  ├─ CAS: id3 open→running  ├─ CAS: id3 open→running
   │  ❌ 失败                │  ❌ 失败                │  ✅ 成功 (version=1)
   │                         │                         │
   ├─ 执行 id1               ├─ 执行 id2               ├─ 执行 id3
   │  AgentFunc(id1)         │  AgentFunc(id2)         │  AgentFunc(id3)
   │                         │                         │
   └─ 更新 id1: done         └─ 更新 id2: done         └─ 更新 id3: done
```

**结果**:
- 3 个 Actions 被 3 个实例分别执行
- 无重复执行
- 无锁等待
- 高效并发

---

## 事件流

### 事件传递路径

```
Planner Agent
   │
   └─ EventBus.Publish("action.proposed")
          │
          ├─→ Orchestrator.handleEvent()
          │      │
          │      └─ executeActions()
          │            │
          │            └─ EventBus.Publish("action.completed")
          │                   │
          │                   └─→ Orchestrator.handleEvent()
          │                          │
          │                          └─ verifyHypotheses()
          │                                 │
          │                                 └─ EventBus.Publish("finding.verified")
          │
          └─→ Monitor Agent (监听所有事件)
```

### 事件数据结构

```json
{
  "type": "action.proposed",
  "action_id": "act_123",
  "payload": {
    "action_ids": ["act_123", "act_456", "act_789"]
  },
  "timestamp": "2024-09-08T10:05:00Z"
}

{
  "type": "action.completed",
  "action_id": "act_123",
  "payload": {
    "reports": [
      {
        "Steps": 1,
        "Promoted": 0,
        "Attempts": 2,
        "StopWhy": "completed",
        "Hypotheses": ["hyp_001", "hyp_002"]
      }
    ]
  },
  "timestamp": "2024-09-08T10:10:00Z"
}

{
  "type": "finding.verified",
  "action_id": "",
  "payload": {
    "finding_id": "find_abc",
    "hypothesis_id": "hyp_001"
  },
  "timestamp": "2024-09-08T10:12:00Z"
}

{
  "type": "monitor.kill_action",
  "action_id": "act_456",
  "payload": {
    "action_id": "act_456",
    "reason": "执行超时（> 30 分钟）"
  },
  "timestamp": "2024-09-08T10:35:00Z"
}

{
  "type": "monitor.request_replan",
  "action_id": "",
  "payload": {
    "reason": "进展缓慢，需要新策略"
  },
  "timestamp": "2024-09-08T10:40:00Z"
}
```

---

## 状态机

### Action 状态转换

```
        ┌──────┐
        │ open │  (初始状态)
        └───┬──┘
            │
            │ CAS transition (Executor)
            │
        ┌───▼────────┐
        │  running   │  (执行中)
        └───┬────┬───┘
            │    │
            │    │ CAS transition (Monitor kill)
            │    │
            │    ├───────────┐
            │                │
   成功完成  │                │ 被终止
            │                │
        ┌───▼──┐        ┌───▼────┐
        │ done │        │aborted │
        └──────┘        └────────┘
```

### Task 状态转换

```
    ┌─────────┐
    │ queued  │  (队列中)
    └────┬────┘
         │
         │ Worker dequeue
         │
    ┌────▼─────┐
    │ running  │  (执行中)
    └────┬─────┘
         │
         ├─────────────┬──────────────┐
         │             │              │
    ┌────▼─────┐  ┌───▼────┐    ┌───▼────┐
    │completed │  │ failed │    │ timeout│
    └──────────┘  └────────┘    └────────┘
```

---

## 性能数据

### 吞吐量

**单实例**:
- Actions/分钟: 10-15
- Findings/小时: 20-50
- HTTP Requests/秒: 5-10

**三实例**:
- Actions/分钟: 30-45
- Findings/小时: 60-150
- HTTP Requests/秒: 15-30

### 延迟

- Action 创建 → 开始执行: 1-5 秒
- Action 执行时间: 2-10 分钟
- Hypothesis 验证时间: 10-30 秒
- Monitor 评估周期: 6 分钟

### 数据量

**单次扫描**:
- Actions: 50-200 个
- Hypotheses: 20-100 个
- Findings: 5-50 个
- Traffic 记录: 500-5000 条

**存储增长**:
- 每个任务: 10-50 MB
- 每天（10 任务）: 100-500 MB
- 每月: 3-15 GB

---

## 故障场景数据流

### Executor 崩溃

```
正常流程:
   Action (open) → CAS → running → Execute → done

崩溃场景:
   Action (open) → CAS → running → Execute → 💥 崩溃
                                       │
                                       └─ Action 保持 running 状态
                                       └─ Monitor 下次评估时检测
                                       └─ Kill action → aborted
```

### 数据库连接失败

```
正常流程:
   Planner → INSERT Actions → EventBus

失败场景:
   Planner → INSERT Actions → 💥 DB 失败
                │
                ├─ 重试 3 次（指数退避）
                ├─ 仍失败 → 记录日志
                └─ 触发告警
```

### CAS 冲突

```
实例 1:
   Read Action (version=1)
   Execute...
   CAS Update (expected version=1) → ✅ 成功

实例 2:
   Read Action (version=1)
   Execute...
   CAS Update (expected version=1) → ❌ 失败 (version=2)
   │
   └─ 跳过该 Action（已被实例 1 处理）
   └─ 记录日志
   └─ 继续处理其他 Actions
```

---

## 数据一致性

### 最终一致性

- **WorldModel**: 强一致性（CAS 锁）
- **EventBus**: 最终一致性（各实例独立）
- **Finding**: 最终一致性（差集收割）

### 幂等性保证

**Action 执行**:
- CAS 锁保证只执行一次
- 重复执行会被拒绝

**Finding 写入**:
- 差集收割避免重复
- 唯一 ID 防冲突

**事件发布**:
- 允许重复发布
- 处理器需幂等

---

## 附录

### 相关文档
- [架构文档](ARCHITECTURE.md)
- [API 文档](docs/api.md)

### 数据库 Schema
参见 `db/migrations/` 目录

---

*最后更新: 2024-09-08*
