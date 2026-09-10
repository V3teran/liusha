# Liusha 架构文档

**版本**: 2.0  
**更新时间**: 2024-09-08  
**状态**: 生产就绪

---

## 系统概述

Liusha 是一个自主安全测试系统，使用多 Agent 协作架构执行 Web 应用安全扫描。系统采用 LLM 驱动的智能决策，通过 WorldModel 管理探索状态，自动发现和验证安全漏洞。

---

## 核心架构

### 系统分层

```
┌─────────────────────────────────────────────────────────┐
│                    HTTP API 层                          │
│                   (cmd/api)                             │
└────────────────────┬────────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────────┐
│                  任务调度层                              │
│              (Dispatcher, Queue)                        │
└────────────────────┬────────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────────┐
│                 Orchestrator                            │
│              (协调和编排中心)                             │
└──┬───────┬────────┬────────┬────────┬──────────────────┘
   │       │        │        │        │
   ▼       ▼        ▼        ▼        ▼
┌──────┐┌──────┐┌────────┐┌──────┐┌─────────┐
│Planner││Monitor││Executor││Verifier││EventBus │
│ Agent ││ Agent ││  Pool  ││ Agent ││         │
└──────┘└──────┘└────────┘└──────┘└─────────┘
   │       │        │        │        │
   └───────┴────────┴────────┴────────┘
                     │
         ┌───────────┴───────────┐
         │                       │
    ┌────▼──────┐        ┌──────▼─────┐
    │ WorldModel│        │  Storage   │
    │   Store   │        │  (Postgres)│
    └───────────┘        └────────────┘
```

---

## 核心组件

### 1. Orchestrator（协调器）

**位置**: `internal/orchestrator/orchestrator.go`  
**职责**: 协调所有 Agent 的执行流程

**核心功能**:
- 任务生命周期管理
- Agent 调度和协调
- 事件订阅和处理
- 并发控制

**工作流程**:
```
1. 启动 Planner Agent（持续规划）
2. 启动 Monitor Agent（定期监察）
3. 订阅事件总线
4. 循环处理事件：
   - action.proposed → 执行 Actions
   - action.completed → 验证 Hypotheses
   - monitor.kill_action → 终止 Action
   - monitor.request_replan → 触发重新规划
```

**关键特性**:
- 事件驱动架构
- 支持 Action 并行执行
- 自动重启崩溃的 Agent
- 优雅关闭机制

---

### 2. Planner Agent（规划器）

**位置**: `internal/planner/agent.go`  
**职责**: 纯规划，生成 Actions

**核心功能**:
- 分析 WorldModel 状态
- 生成探索 Actions
- 发布 Actions 到事件总线
- 响应重新规划请求

**规划策略**:
1. **初始规划**: 任务启动时生成初始 Actions
2. **增量规划**: Monitor 触发时生成新 Actions
3. **去重**: 使用 fingerprint 避免重复 Actions
4. **依赖管理**: 处理 Action 之间的依赖关系

**工具集**:
- `plan_initial`: 初始规划
- `plan_incremental`: 增量规划
- `assess_global`: 全局评估

**输出**:
```go
type GlobalAssessment struct {
    NewActions      []NewAction      // 新建的 Actions
    ActionsToSteer  []ActionSteer    // 需要调整的 Actions
    HypothesesReady []string         // 可验证的 Hypotheses
    Summary         string           // 规划总结
}
```

---

### 3. Monitor Agent（监察器）

**位置**: `internal/monitor/agent.go`  
**职责**: 独立监察，评估任务进展

**核心功能**:
- 定期（默认 6 分钟）评估任务状态
- 检测异常和卡死情况
- 发出干预决策（kill action, request replan）

**监察维度**:
1. **进展评估**: Actions 完成率、新 Findings 数量
2. **效率评估**: 资源使用、时间分布
3. **质量评估**: Findings 质量、覆盖面
4. **异常检测**: 卡死、循环、资源浪费

**干预决策**:
- `kill_action`: 终止低效或异常的 Action
- `request_replan`: 请求重新规划

**工作模式**:
```go
for {
    time.Sleep(6 * time.Minute)
    decision := m.Evaluate(ctx)
    if decision != nil {
        m.eventBus.Publish(decision)
    }
}
```

---

### 4. Executor Pool（执行池）

**位置**: `internal/executor/pool.go`  
**职责**: 并发执行 Actions

**核心功能**:
- 管理 Coordinator 工作池
- 并发执行多个 Actions
- 收割 Findings 和 Hypotheses

**架构**:
```
Pool (容量: 10)
  ├── Coordinator 1 → AgentFunc → LLM Agent
  ├── Coordinator 2 → AgentFunc → LLM Agent
  ├── Coordinator 3 → AgentFunc → LLM Agent
  └── ...
```

**Coordinator 职责**:
1. 快照运行前的 Findings
2. 执行 AgentFunc（调用 LLM Agent）
3. 差集收割新 Findings
4. 转换为 Attempts 返回

**并发安全**:
- 使用 Channel 通信
- CAS 乐观锁防止状态冲突
- 每个 Action 独立执行

---

### 5. Verifier Agent（验证器）

**位置**: `internal/verifier/agent.go`  
**职责**: LLM 自主验证 Hypotheses

**核心功能**:
- 接收 Hypothesis ID
- 使用 LLM 分析和验证
- 生成 Verified Finding 或 Refuted 结果

**验证流程**:
1. 读取 Hypothesis 详情
2. 分析相关 Traffic 和上下文
3. LLM 推理验证
4. 生成最终结论

**输出**:
- `*finding.VulnFinding`: 验证通过
- `nil`: 验证失败（refuted）
- `error`: 验证过程出错

---

### 6. WorldModel（世界模型）

**位置**: `internal/worldmodel/store.go`  
**职责**: 管理探索状态

**数据模型**:
```
Node (节点)
  ├── Action (待执行的探索动作)
  ├── Hypothesis (待验证的假设)
  └── Finding (已验证的漏洞)
```

**核心字段**:
```go
type Node struct {
    ID          string          // 节点 ID
    TaskID      string          // 任务 ID
    Kind        NodeKind        // 节点类型
    Content     json.RawMessage // 节点内容
    Version     int             // 乐观锁版本（CAS）
    Fingerprint string          // 去重指纹
    State       *State          // 状态（open/running/done）
    Complexity  *Complexity     // 复杂度
    DependsOn   []string        // 依赖的其他节点
    CreatedAt   time.Time
    UpdatedAt   time.Time
}
```

**关键特性**:

#### CAS 乐观锁
```go
// 原子性状态转换
affected, err := store.CASTransition(ctx, nodeID, 
    StateOpen, StateRunning, expectedVersion)
```

**用途**:
- 防止多个 Orchestrator 实例同时执行同一个 Action
- 防止并发更新冲突

#### 指纹去重
```go
fingerprint := fmt.Sprintf("%s:%s:%s",
    instruction,
    strings.Join(sortedDeps, ","),
    complexity)
```

**用途**:
- 避免生成重复的 Actions
- 快速查找相似的 Actions

---

### 7. EventBus（事件总线）

**位置**: `internal/eventbus/bus.go`  
**职责**: 组件间异步通信

**事件类型**:
```go
type EventType string

const (
    // Planner 事件
    EventActionProposed      = "action.proposed"
    
    // Executor 事件
    EventActionStarted       = "action.started"
    EventActionCompleted     = "action.completed"
    EventActionFailed        = "action.failed"
    
    // Monitor 事件
    EventMonitorKillAction   = "monitor.kill_action"
    EventMonitorRequestReplan = "monitor.request_replan"
    
    // Verifier 事件
    EventFindingVerified     = "finding.verified"
)
```

**事件结构**:
```go
type Event struct {
    Type      EventType
    ActionID  string
    Payload   map[string]interface{}
    Timestamp time.Time
}
```

**发布-订阅模式**:
```go
// 发布事件
bus.Publish(Event{
    Type: EventActionCompleted,
    Payload: map[string]interface{}{
        "action_id": actionID,
        "reports": reports,
    },
})

// 订阅事件
bus.Subscribe(func(event Event) {
    // 处理事件
})
```

---

## 数据存储

### PostgreSQL 表结构

#### wm_node（WorldModel 节点）
```sql
CREATE TABLE wm_node (
    id          TEXT PRIMARY KEY,
    task_id     TEXT NOT NULL,
    kind        TEXT NOT NULL,
    content     JSONB NOT NULL,
    version     INTEGER DEFAULT 1,        -- CAS 乐观锁
    fingerprint TEXT,                     -- 去重指纹
    state       TEXT,
    complexity  TEXT,
    depends_on  TEXT[],
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

-- 索引
CREATE INDEX idx_wm_node_task_kind ON wm_node(task_id, kind);
CREATE INDEX idx_wm_node_state ON wm_node(task_id, state);
CREATE INDEX idx_wm_node_version ON wm_node(task_id, version);
CREATE INDEX idx_wm_node_fingerprint ON wm_node(task_id, fingerprint);
CREATE INDEX idx_wm_node_fingerprint_pending 
    ON wm_node(task_id, fingerprint) 
    WHERE state IN ('open', 'blocked');
```

#### finding（漏洞）
```sql
CREATE TABLE finding (
    id             TEXT PRIMARY KEY,
    task_id        TEXT NOT NULL,
    host           TEXT NOT NULL,
    vuln_type      TEXT NOT NULL,
    severity       TEXT NOT NULL,
    title          TEXT NOT NULL,
    description    TEXT,
    payload        TEXT,
    evidence_url   TEXT,
    http_req       TEXT,
    http_resp      TEXT,
    remediation    TEXT,
    confidence     REAL,
    created_at     TIMESTAMPTZ DEFAULT NOW()
);
```

#### traffic（HTTP 流量）
```sql
CREATE TABLE traffic (
    id           SERIAL PRIMARY KEY,
    task_id      TEXT NOT NULL,
    agent_id     TEXT NOT NULL,
    method       TEXT NOT NULL,
    url          TEXT NOT NULL,
    req_headers  JSONB,
    req_body     TEXT,
    resp_status  INTEGER,
    resp_headers JSONB,
    resp_body    TEXT,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);
```

---

## 并发控制

### CAS 乐观锁

**原理**:
```sql
UPDATE wm_node 
SET state = $1, version = version + 1, updated_at = NOW()
WHERE id = $2 AND version = $3
RETURNING version;
```

**流程**:
1. 读取节点和当前 version
2. 执行业务逻辑
3. 更新时检查 version
4. 如果 version 匹配，更新成功并递增 version
5. 如果 version 不匹配，更新失败（被其他实例修改）

**使用场景**:
- Action 状态转换: `open → running`
- Action 完成: `running → done`
- Monitor kill: `running → aborted`

**冲突处理**:
- 更新失败时跳过该 Action（已被其他实例处理）
- 记录日志用于监控
- 不重试（避免重复执行）

---

### 去重机制

**指纹生成**:
```go
func (s *Store) ComputeFingerprint(ctx context.Context, node Node) (string, error) {
    // 提取关键字段
    instruction := extractInstruction(node.Content)
    deps := node.DependsOn
    complexity := string(*node.Complexity)
    
    // 标准化
    sort.Strings(deps)
    
    // 生成指纹
    return fmt.Sprintf("%s:%s:%s", 
        instruction, 
        strings.Join(deps, ","),
        complexity), nil
}
```

**去重查询**:
```sql
SELECT id FROM wm_node
WHERE task_id = $1 
  AND fingerprint = $2
  AND state IN ('open', 'blocked')
LIMIT 1;
```

**工作流程**:
1. Planner 生成新 Actions
2. 计算每个 Action 的 fingerprint
3. 查询是否存在相同 fingerprint 的待执行 Action
4. 如果存在，跳过该 Action
5. 如果不存在，插入新 Action

---

## 配置

### Orchestrator 配置
```go
type Config struct {
    TaskID           string
    World            *worldmodel.Store
    Traffic          *traffic.AgentStore
    Findings         *finding.Store
    EventBus         *eventbus.Bus
    PlannerConfig    planner.Config
    MonitorConfig    monitor.Config
    ExecutorConfig   executor.Config
    VerifierConfig   verifier.Config
    ExecutorPoolSize int    // 并发执行数，默认 10
    Logger           zerolog.Logger
}
```

### Planner 配置
```go
type Config struct {
    TaskID       string
    EventBus     *executor.PlannerEventBus
    ActionBus    *eventbus.Bus
    World        *worldmodel.Store
    ControlPlane *controlplane.Store
    Router       *provider.Router
    Logger       zerolog.Logger
}
```

### Monitor 配置
```go
type Config struct {
    TaskID   string
    World    *worldmodel.Store
    EventBus *eventbus.Bus
    Provider provider.Provider
    Interval time.Duration  // 评估间隔，0 = 默认 6 分钟
    Logger   zerolog.Logger
}
```

### Executor 配置
```go
type Config struct {
    TaskID       string
    Host         string
    World        *worldmodel.Store
    TrafficStore *traffic.AgentStore
    ProxyStore   *traffic.ProxyStore
    FindingStore *finding.Store
    ControlPlane *controlplane.Store
    Router       *provider.Router
    Registry     *registry.Registry
    AgentFunc    AgentFunc  // 执行函数
    Logger       zerolog.Logger
}
```

---

## 部署架构

### 单实例部署
```
┌────────────────────────────────────┐
│         Single Instance            │
│                                    │
│  ┌──────────────────────────────┐ │
│  │      HTTP API (8080)         │ │
│  └────────────┬─────────────────┘ │
│               │                    │
│  ┌────────────▼─────────────────┐ │
│  │      Orchestrator            │ │
│  │  (所有 Agents 在同一进程)      │ │
│  └────────────┬─────────────────┘ │
│               │                    │
│  ┌────────────▼─────────────────┐ │
│  │      PostgreSQL              │ │
│  └──────────────────────────────┘ │
└────────────────────────────────────┘
```

**特点**:
- 简单易部署
- 适合小规模测试
- 资源消耗低

---

### 多实例部署（推荐）
```
       ┌─────────────┐
       │ Load Balancer│
       └──────┬───────┘
              │
    ┌─────────┼─────────┐
    │         │         │
┌───▼───┐ ┌──▼────┐ ┌──▼────┐
│Instance│ │Instance│ │Instance│
│   1    │ │   2    │ │   3    │
└───┬───┘ └───┬───┘ └───┬───┘
    │         │         │
    └─────────┼─────────┘
              │
      ┌───────▼────────┐
      │   PostgreSQL   │
      │   (共享状态)    │
      └────────────────┘
```

**特点**:
- 高可用性
- 水平扩展
- CAS 锁保证并发安全
- 适合生产环境

**并发控制**:
- CAS 乐观锁防止重复执行
- 去重机制避免重复规划
- EventBus 在每个实例独立运行

---

## 性能特性

### 并发执行
- Executor Pool 默认 10 个并发
- 每个 Action 独立执行
- 无锁设计，高吞吐

### 乐观锁性能
- 无锁等待
- 高并发下性能优秀
- 冲突率低（Actions 通常不重复）

### 去重性能
- 指纹索引查询 O(log n)
- 避免无效计算
- 减少数据库负载

### 数据库优化
- 合理的索引设计
- 部分索引（WHERE 子句）
- JSONB 字段支持高效查询

---

## 监控指标

### 关键指标

**任务级别**:
- 任务总数
- 运行中任务数
- 平均执行时间
- 成功率

**Action 级别**:
- Action 生成速率
- Action 执行速率
- Action 平均耗时
- Action 成功率

**Finding 级别**:
- Finding 发现速率
- Finding 验证速率
- 按 severity 分布
- 按 vuln_type 分布

**资源级别**:
- CPU 使用率
- 内存使用率
- 数据库连接数
- LLM API 调用量

### 告警规则

**高优先级**:
- Orchestrator 崩溃
- 数据库连接失败
- LLM API 限流

**中优先级**:
- Action 执行失败率 > 10%
- Monitor 评估间隔 > 10 分钟
- 内存使用率 > 80%

**低优先级**:
- 任务执行时间 > 预期
- Finding 质量下降

---

## 故障处理

### 组件故障

**Planner 崩溃**:
- Orchestrator 自动重启
- 重新规划
- 影响：规划暂停

**Monitor 崩溃**:
- Orchestrator 自动重启
- 重新评估
- 影响：监察暂停

**Executor 崩溃**:
- Pool 自动恢复
- Action 状态保持
- 影响：该 Action 失败

**Verifier 崩溃**:
- 验证失败
- 记录日志
- 影响：该 Hypothesis 未验证

### 数据库故障

**连接失败**:
- 重试机制
- 指数退避
- 最终失败告警

**锁冲突**:
- CAS 更新失败
- 跳过该 Action
- 记录日志

### 网络故障

**LLM API 失败**:
- 重试 3 次
- 降级处理
- 记录日志

**目标站点不可达**:
- Action 标记为 failed
- 记录原因
- 继续其他 Actions

---

## 安全考虑

### 认证授权
- API Token 验证
- 任务隔离
- 用户权限控制

### 数据隔离
- 按 task_id 隔离
- 防止跨任务访问
- SQL 注入防护

### 敏感数据
- 密码加密存储
- Token 脱敏日志
- HTTP Body 大小限制

### 速率限制
- LLM API 调用限流
- 目标站点请求限速
- 防止 DoS

---

## 扩展点

### 自定义 Agent
- 实现 `AgentInterface`
- 注册到 Orchestrator
- 订阅事件总线

### 自定义工具
- 实现 `ToolInterface`
- 注册到 Registry
- LLM 可调用

### 自定义存储
- 实现 `Store` 接口
- 替换默认实现
- 支持其他数据库

### 自定义 LLM Provider
- 实现 `Provider` 接口
- 注册到 Router
- 支持多模型

---

## 最佳实践

### 配置建议
- Executor Pool: 10-20（根据资源）
- Monitor Interval: 5-10 分钟
- LLM Temperature: 0.7（规划）, 0.3（验证）

### 性能优化
- 合理设置 Pool 大小
- 定期清理历史数据
- 监控数据库性能

### 运维建议
- 配置日志轮转
- 启用监控告警
- 定期备份数据库
- 保留审计日志

### 故障恢复
- 实现健康检查
- 配置自动重启
- 保留现场日志
- 建立应急预案

---

## 附录

### 相关文档
- [数据流文档](DATAFLOW.md)
- [API 文档](docs/api.md)
- [部署指南](docs/deployment.md)

### 版本历史
- v2.0 (2024-09-08): 多 Agent 架构重构
- v1.0 (2024-06): 初始版本

---

*最后更新: 2024-09-08*
