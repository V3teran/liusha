# ADR-003: 动态图 vs 静态 DAG

## 状态
已接受 (2026-01-09)

## 上下文

系统曾探索两种编排模型：
1. **WorkflowEngine**: Compile-Execute 两阶段静态 DAG 引擎
2. **KnowledgeGraph**: 运行时可修改的动态图存储

## 系统要求

### 必须支持动态规划

系统采用 ReAct（Reasoning-Action-Observation）循环模式，Planner Agent 需要在运行时根据观察结果动态生成新的 Actions：

```
时刻 T0: Planner 生成 Action A1, A2
时刻 T1: Orchestrator 执行 A1 → 产生 Observation O1
时刻 T2: Planner 基于 O1 分析后动态生成新 Action A3（依赖 A1）
时刻 T3: Orchestrator 执行 A3
```

这种动态规划能力是安全测试场景的核心需求：
- 根据侦察结果决定下一步攻击向量
- 根据漏洞验证结果决定是否深入利用
- 根据防御响应动态调整策略

### 技术约束分析

#### WorkflowEngine（静态图引擎）

**架构设计**：
```go
type WorkflowEngine interface {
    // 阶段1：编译期 - 构建静态 ExecutableGraph
    Compile(ctx context.Context, workflow *Workflow) (*ExecutableGraph, error)
    
    // 阶段2：执行期 - 执行已编译的图
    Execute(ctx context.Context, graph *ExecutableGraph, input any) (*ExecutionResult, error)
}
```

**静态约束**：
- ❌ Compile 阶段执行拓扑排序，生成分层结构 `Layers [][]*Node`
- ❌ Execute 按预编译的层级执行，图结构已锁死
- ❌ 运行时新增节点需重新 Compile，破坏执行状态
- ❌ 无 AddNode/RemoveNode/UpdateGraph 动态修改接口

**适用场景**：
- 固定流程编排（ETL、数据管道）
- CI/CD 预定义工作流
- 批处理任务

#### KnowledgeGraph（动态图存储）

**架构设计**：
```go
type Store interface {
    CreateNode(ctx context.Context, node *Node) error
    UpdateNode(ctx context.Context, id string, update NodeUpdate) error
    CreateEdge(ctx context.Context, edge *Edge) error
    CompareAndSwapState(ctx context.Context, id string, old, new State) (bool, error)
    ListOpenActions(ctx context.Context, taskID string) ([]Node, error)
}
```

**动态能力**：
- ✅ 无预编译阶段，实时查询最新状态
- ✅ CreateNode/UpdateNode/CreateEdge 运行时修改
- ✅ CAS 并发控制支持分布式安全
- ✅ Orchestrator 轮询查询，天然获取最新图状态

**适用场景**：
- AI Agent 动态规划（ReAct、AutoGPT、LangGraph）
- 自适应系统（根据反馈调整行为）
- 安全测试（根据侦察结果动态生成攻击链）

### 实际使用验证

**Planner 运行时生成 Actions**（`internal/planner/tools.go:187`）：
```go
func (t *ProposeActionsTool) Execute(ctx context.Context, argsJSON json.RawMessage) {
    // LLM 实时决策生成新 Actions
    for _, a := range input.Actions {
        node := &knowledgegraph.Node{
            ID:        uuid.New().String(),  // 运行时生成
            Kind:      KindAction,
            State:     StateOpen,            // 动态加入图
            DependsOn: a.DependsOn,          // 动态依赖
        }
        world.CreateNode(ctx, node)  // 直接写入动态图
    }
}
```

**Orchestrator 实时查询**（`internal/orchestrator/orchestrator.go:613`）：
```go
func (o *Orchestrator) getExecutableActions(ctx context.Context) {
    // 每次轮询都读取最新状态
    allActions := world.ListOpenActions(ctx, taskID)
    // 无需预编译，直接过滤可执行节点
}
```

## 决策

**移除 WorkflowEngine，KnowledgeGraph 作为唯一编排基础**

### 理由

1. **技术匹配度**：WorkflowEngine 的 Compile-Execute 两阶段架构从根本上无法支持动态图
2. **使用现状**：WorkflowEngine 零引用，从未被集成到系统中
3. **架构清晰**：单一真实来源避免概念混淆和维护成本
4. **行业对齐**：ReAct、LangGraph 等 AI Agent 框架均采用动态图架构

## 后果

### 正面影响

1. **架构简化**：移除 150 行未使用代码，消除认知负担
2. **概念统一**：KnowledgeGraph 作为唯一编排基础，降低理解成本
3. **保持灵活**：Planner 可随时生成新 Actions，支持复杂决策链

### 负面影响

1. **性能权衡**：Orchestrator 手写依赖解析为 O(n)，在极大规模下可能不如专用 DAG 引擎
2. **功能缺失**：缺少专用 DAG 引擎的高级特性（条件分支、子工作流嵌套）

### 风险缓解

**性能监控**：
- 设置 Action 数量阈值告警（100/500/1000）
- 记录依赖解析耗时，超过 2 秒告警
- 实现在 `internal/orchestrator/metrics.go`

**优化已完成**：
- 依赖解析从 O(n²) 优化为 O(n)
- 添加性能监控埋点

**未来扩展路径**：
如满足以下条件，考虑引入动态 DAG 引擎：
1. 单任务 Action 数常态化 >500
2. 需要复杂条件分支（if-else，非简单依赖）
3. 需要嵌套子工作流

引入时选择：
- 支持动态修改的 DAG 库（非静态编译）
- 支持 PostgreSQL CAS 的分布式协调
- 不复用被删除的 WorkflowEngine（其架构不支持动态修改）

## 参考

### 学术基础
- ReAct 论文（Yao et al., 2022）：Thought-Action-Observation 动态循环
- PDDL（Planning Domain Definition Language）：AI 规划标准

### 行业实践
- **LangGraph**：支持动态节点添加的 Agent 图框架
- **AutoGPT**：运行时根据反馈生成任务
- **Temporal**：静态预定义工作流（对比案例，不适用于动态场景）

### 相关决策
- ADR-002: ReAct 循环架构（假设存在）
- 知识图谱设计文档：5 种节点、5 种关系的语义定义
