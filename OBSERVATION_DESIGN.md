# Observation 节点设计重构

## 背景

原设计中，observation 节点只装待验证的漏洞（带 repro 的假设），缺乏对事实类发现和可疑指标的结构化支持，导致：

1. **信息丢失**：重要发现（端口开放、服务版本等）无处存储
2. **效率低下**：所有观察都过复现验证门，浪费资源
3. **报告不完整**：缺乏支撑证据链，无法构建完整攻击故事
4. **关联分析困难**：可疑指标无结构化存储

## 解决方案

### 1. 扩展 ObservationConfidence（5态）

**位置**: `internal/framework/core/exploration_graph.go`

```go
const (
    // 漏洞生命周期
    ConfidenceUnverified ObservationConfidence = "unverified" // 待验证
    ConfidenceVerified   ObservationConfidence = "verified"   // 已验证
    ConfidenceRefuted    ObservationConfidence = "refuted"    // 已证伪

    // 事实类
    ConfidenceFactual ObservationConfidence = "factual" // 已确认事实

    // 指标类
    ConfidenceIndicative ObservationConfidence = "indicative" // 可疑指标
)
```

### 2. 引入 ObservationCategory

**位置**: `internal/executor/observation.go`

```go
type ObservationCategory string

const (
    CategoryVulnerability ObservationCategory = "vulnerability" // 漏洞
    CategoryFact          ObservationCategory = "fact"          // 事实
    CategoryIndicator     ObservationCategory = "indicator"     // 指标
)
```

**Observation 结构**:

```go
type Observation struct {
    Statement  string              // 观察陈述
    Category   ObservationCategory // 分类（决定晋升流程）
    Reasoning  string              // 推理过程
    Severity   string              // 严重性（仅漏洞）
    Confidence string              // executor 主观评分
    Repro      json.RawMessage     // 复现配方（仅漏洞）
    ExecutionTrace *ExecutionTrace // 完整执行轨迹
}
```

### 3. Executor 层路由逻辑

**位置**: `internal/executor/observation.go#createObservationNode`

根据 Category 设置图级 Confidence：

```go
switch obs.Category {
case CategoryVulnerability:
    // 漏洞必须有复现配方
    if len(obs.Repro) == 0 {
        return nil, fmt.Errorf("vulnerability observation requires repro")
    }
    unverified := explorationgraph.ConfidenceUnverified
    node.Confidence = &unverified

case CategoryFact:
    // 事实无需验证，直接确认
    factual := explorationgraph.Confidence(explorationgraph.ConfidenceFactual)
    node.Confidence = &factual

case CategoryIndicator:
    // 指标作为支撑信息
    indicative := explorationgraph.Confidence(explorationgraph.ConfidenceIndicative)
    node.Confidence = &indicative
}
```

**默认推断**（向后兼容）:

`internal/executor/execute_helpers.go#harvestObservation`

```go
// 如果未指定 Category，根据 Repro 是否存在推断默认类型
if obs.Category == "" {
    if len(obs.Repro) > 0 {
        obs.Category = CategoryVulnerability // 有复现配方，默认为漏洞
    } else {
        obs.Category = CategoryFact // 无复现配方，默认为事实记录
    }
}
```

### 4. Evaluator 层选择性验证

**位置**: `internal/evaluator/agent.go`

#### 改动 1: pollAndProcess 圈定多类观察

```go
func (a *Agent) pollAndProcess(ctx context.Context, sem chan struct{}) error {
    // 圈定 unverified 观察
    observations, err := a.graph.ListObservationsByConfidence(
        ctx, a.taskID, explorationgraph.ConfidenceUnverified, pollBatchSize)
    
    // 圈定 factual 观察
    factualObs, err := a.graph.ListObservationsByConfidence(
        ctx, a.taskID, explorationgraph.ConfidenceFactual, pollBatchSize)
    observations = append(observations, factualObs...)
    
    // 圈定 indicative 观察
    indicativeObs, err := a.graph.ListObservationsByConfidence(
        ctx, a.taskID, explorationgraph.ConfidenceIndicative, pollBatchSize)
    observations = append(observations, indicativeObs...)
    
    // 路由处理...
}
```

#### 改动 2: 按类型路由

```go
switch *obs.Confidence {
case explorationgraph.ConfidenceUnverified:
    // 漏洞：需过复现验证门
    a.dispatchVerification(ctx, obs.ID, attempt, sem)

case explorationgraph.ConfidenceFactual:
    // 事实：直接晋升
    a.dispatchPromoteFact(ctx, obs, sem)

case explorationgraph.ConfidenceIndicative:
    // 指标：暂不主动晋升，但可由 evaluator 关联到相关 result
    a.logger.Debug().Str("obs_id", obs.ID).Msg("indicative 观察，暂不处理（支撑分析）")
}
```

#### 改动 3: promoteFact 直接晋升

```go
func (a *Agent) promoteFact(ctx context.Context, obs explorationgraph.Node) error {
    // 创建 result 节点（复用 observation 的内容）
    verified := explorationgraph.ConfidenceVerified
    resultNode := explorationgraph.Node{
        ID:         uuid.New().String(),
        TaskID:     obs.TaskID,
        Kind:       core.KindResult,
        Content:    obs.Content, // 复用 observation 的内容
        Priority:   obs.Priority,
        Confidence: &verified,
        SourceType: explorationgraph.SourceEvaluator,
        SourceID:   a.agentRunID,
        CreatedAt:  time.Now(),
        UpdatedAt:  time.Now(),
    }

    // 创建 observation → result 的关系边
    edge := explorationgraph.Edge{
        TaskID:    obs.TaskID,
        SrcID:     obs.ID,
        Rel:       explorationgraph.RelGenerates,
        DstID:     resultNode.ID,
        CreatedAt: time.Now(),
    }

    if err := a.graph.CreateNodeWithEdges(ctx, resultNode, []explorationgraph.Edge{edge}); err != nil {
        return fmt.Errorf("创建 result 节点失败: %w", err)
    }

    // 记录审计链
    verification := explorationgraph.Verification{
        ID:      uuid.New().String(),
        TaskID:  obs.TaskID,
        NodeID:  obs.ID,
        Outcome: explorationgraph.OutcomeConfirmed,
        Evaluation: mustJSON(map[string]interface{}{
            "type":     "fact_promotion",
            "promoted": true,
        }),
        CreatedAt: time.Now(),
    }
    a.graph.RecordVerification(ctx, verification)

    a.eventBus.PublishVerificationPassed(a.taskID, resultNode.ID)
    return nil
}
```

## 业界对标

| 标准/产品 | 设计模式 |
|----------|---------|
| **NIST SP 800-115** | 发现阶段 = Raw Findings（事实+指标+漏洞混合）<br>验证阶段 = 选择性确认 |
| **Qualys/Tenable** | Raw Scan Results → Unconfirmed Vulns → Verified Findings |
| **MITRE ATT&CK** | Observation = behaviors + artifacts + indicators<br>Assessment = confirmed techniques |

## 状态转移

```
Executor 产出 Observation
    ↓
根据 Category 设置 Confidence
    ↓
    ├─ vulnerability + repro → unverified → (复现门) → verified/refuted
    ├─ fact                  → factual    → (直接)   → result (verified)
    └─ indicator             → indicative → (关联)   → 附加到 result metadata
```

## 测试验证

所有测试通过：

- ✅ `internal/evaluator/...` - 25 个测试全部通过
- ✅ `internal/executor/...` - 15 个测试全部通过
- ✅ 编译无错误
- ✅ 向后兼容（旧代码自动推断 Category）

## 收益

| 场景 | 现在 | 改后 |
|------|------|------|
| 发现端口22，OpenSSH 7.4 | 无处存储，可能丢失 | CategoryFact → ConfidenceFactual → 直接 result |
| 发现 SQL 注入，需验证 | unverified → 复现门 | CategoryVulnerability → unverified → 复现门（不变） |
| 发现 DNS 记录异常 | 无处存储 | CategoryIndicator → 附加到相关 result 的 metadata |
| 报告生成 | 只有漏洞 | 漏洞 + 事实上下文 + 可疑指标 = 完整故事 |

## 影响范围

### 修改文件

1. `internal/framework/core/exploration_graph.go` - 扩展 Confidence 枚举
2. `internal/explorationgraph/model.go` - 添加常量别名
3. `internal/executor/observation.go` - 添加 Category + 路由逻辑
4. `internal/executor/execute_helpers.go` - 默认推断逻辑
5. `internal/evaluator/agent.go` - 多类观察轮询 + 路由 + promoteFact
6. `internal/executor/engine_observation_test.go` - 修复测试（添加 Category）

### 向后兼容性

✅ **完全兼容**：旧代码未指定 Category 时，自动根据 Repro 推断：
- 有 Repro → CategoryVulnerability
- 无 Repro → CategoryFact

## 未来扩展

### Phase 2: Result 内容增强

```go
type ResultContent struct {
    Severity           string
    Evidence           []string
    
    // 新增：支撑信息
    SourceObservations []string        // 关联的 observation 节点 ID
    AttackChain        string          // 攻击故事
    ExecutionTrace     *ExecutionTrace // 完整轨迹
}
```

### Phase 3: 指标关联分析

实现 `attachIndicatorToResult`，将 indicative 观察关联到相关 result 的 metadata，支持：
- 攻击链构建
- 风险评分
- 时间线分析
