# Event 类型统一分析

## 三个 Event 定义的对比

### 1. framework/core/eventbus.go - Event
**职责**：框架层通用事件总线
```go
type Event struct {
    ID        string                 // 事件唯一标识
    Type      EventType              // 事件类型
    ActionID  string                 // 关联的 action ID
    Payload   map[string]interface{} // 事件载荷
    Timestamp time.Time              // 事件时间戳
}
```
**事件类型**：
- `action.killed` - action 被终止
- `action.steered` - action 被纠偏
- `action.completed` - action 完成
- `human.input.required` - 需要人工输入
- `human.input.received` - 收到人工输入

**使用场景**：
- 框架层全系统消息总线
- Middleware/HumanInteractionManager 发布输入请求
- StreamEventBusImpl 转换事件流
- 通用 EventFilter 过滤

**依赖者**：
- `middleware/human_manager.go`
- `middleware/stream_adapter.go`

---

### 2. audit/model.go - Event
**职责**：系统级敏感操作审计追踪
```go
type Event struct {
    ID         int64
    Actor      string                 // 操作者（system/api_user/runner）
    Action     string                 // 操作类型（task.abort/credential.delete）
    TargetKind string                 // 目标类型（task/credential）
    TargetID   string                 // 目标 ID
    Metadata   json.RawMessage        // 任意上下文 jsonb
    CreatedAt  time.Time
}
```
**操作类型**：
- `task.abort` - 中止任务
- `task.create` - 创建任务
- `credential.set` - 设置凭证
- `credential.delete` - 删除凭证

**使用场景**：
- 审计日志持久化到 audit_log 表
- 合规需求（谁做了什么）
- 误操作复盘（凭证被删/task 被 abort）
- 显式调用 `Append()`，不自动触发

**特点**：
- 业务侧明确感知"这是审计事件"才调用
- 不靠 trigger 全自动，避免隐式行为难维护

---

### 3. executor/event.go - Event
**职责**：Planner 规划驱动事件
```go
type Event struct {
    Type      EventType              // 事件类型
    TaskID    string                 // 任务 ID
    Timestamp time.Time              // 时间戳
    Payload   map[string]interface{} // 事件载荷
}
```
**事件类型**：
- `action_completed` - Action 执行完成
- `finding_discovered` - 发现新 Finding
- `verification_passed` - 验证通过
- `manual_guidance` - 用户手动介入
- `task_started` - 任务启动
- `heartbeat` - 定期心跳

**使用场景**：
- 驱动 Planner Agent 重新规划
- 事件驱动而非轮询
- 包含 PlannerEventBus 事件总线实现
- 支持 TaskID 级别的事件订阅

**特点**：
- 与 Planner 循环紧密耦合
- 每个 TaskID 有独立事件通道（50 缓冲）
- 背压处理（通道满时丢弃）

---

## 结论

**三个 Event 类型不应统一**

它们的语义和职责完全不同：

| 方面 | framework/core | audit | executor |
|------|---|---|---|
| **职责** | 框架事件总线 | 审计追踪 | 规划驱动 |
| **持久化** | 可选（EventStore） | 必须（audit_log） | 否（内存） |
| **事件来源** | 全系统 | 业务显式调用 | Planner 驱动 |
| **范围** | ActionID 级别 | Task/Credential 级别 | TaskID 级别 |
| **频率** | 低频（关键事件） | 低频（敏感操作） | 高频（规划循环） |
| **消费者** | Middleware/UI | 审计系统 | Planner Agent |

### 设计正当性

1. **framework/core/Event**
   - 框架通用消息传递
   - 支持全系统事件通知
   - 与业务逻辑解耦

2. **audit/Event**
   - 审计特定需求（Actor/Action/TargetKind）
   - 显式控制，避免隐式副作用
   - 合规要求单独管理

3. **executor/Event**
   - Planner 循环特定（TaskID/Type/Payload）
   - 事件驱动架构核心
   - 高频率更新需要独立优化

### 后续检查清单

- ✅ Message 类型（已统一）
- ✅ AgentConfig 类型（已补全）
- ✅ EventBus 接口（已提取）
- ✅ Event 类型（已验证，不应统一）
- ⏳ 其他 Config 类型（待检查）
- ⏳ Store 接口（待检查）
