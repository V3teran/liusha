# Store 接口统一分析

## 全景图

系统中存在 7 个 Store 相关接口，分布于不同层级，各有专属职责。

---

## 详细分析

### 1. persistence/interface.go:Store - 统一持久化接口
**职责**：框架层通用存储抽象
```go
type Store interface {
    StateManager() core.StateManager[any]
    Checkpointer() core.Checkpointer
    EventStore() core.EventStore
    Close() error
    Health(ctx context.Context) error
}
```
**实现策略**：组合模式
- 组合 StateManager、Checkpointer、EventStore
- 支持多实现：memory/postgres/redis
- 配置支持缓存、连接池、超时

**扩展接口**：
- `TransactionalStore`：事务支持
- `CachedStore`：缓存管理
- `MigrationRunner`：数据库迁移
- `BackupManager`：备份恢复

**使用场景**：应用全局存储引擎

---

### 2. framework/core/eventbus.go:EventStore - 事件存储
**职责**：框架层通用事件持久化
```go
type EventStore interface {
    Save(ctx context.Context, event Event) error
    Load(ctx context.Context, actionID string) ([]Event, error)
    LoadByType(ctx context.Context, typ EventType) ([]Event, error)
}
```
**事件类型**：
- `action.killed` / `action.steered` / `action.completed`
- `human.input.required` / `human.input.received`

**使用场景**：
- 由 Store 接口组合
- 可选实现（内存或数据库）
- 支持按 actionID 或 EventType 查询

**特点**：
- 被 Store 接口集成
- 与 audit.Event 完全不同（架构层 vs 审计层）
- 与 executor.Event 不同（通用事件 vs Planner 驱动）

---

### 3. framework/core/graphstore.go:GraphStore - 知识图谱存储
**职责**：动态图存储（与 DAG 执行图不同）
```go
type GraphStore interface {
    // 节点操作
    CreateNode(ctx context.Context, node *GraphNode) error
    GetNode(ctx context.Context, id string) (*GraphNode, error)
    UpdateNode(ctx context.Context, id string, update GraphNodeUpdate) error
    DeleteNode(ctx context.Context, id string) error
    ListNodes(ctx context.Context, query GraphNodeQuery) ([]*GraphNode, error)
    CompareAndSwapState(ctx context.Context, id string, expectedState, newState string) (bool, error)
    
    // 边操作
    CreateEdge(ctx context.Context, edge *GraphEdge) error
    ListEdges(ctx context.Context, query GraphEdgeQuery) ([]*GraphEdge, error)
    DeleteEdge(ctx context.Context, from, to, relation string) error
    
    // 图遍历
    Traverse(ctx context.Context, startID string, query GraphTraverseQuery) ([]*GraphNode, error)
}
```
**核心特性**：
- CAS 原子更新（乐观锁）
- 支持 BFS/DFS 遍历
- 节点状态管理
- 关系类型灵活

**使用场景**：
- 知识图谱持久化
- 运行时图结构存储
- 独立于 Store 接口

**特点**：
- 专用图数据库接口
- 与 Store 无关（不是必须集成的）
- 支持未来迁移到 Neo4j/ArangoDB

---

### 4. framework/middleware/human.go:HumanInputStore - 人工输入存储
**职责**：人工交互请求/响应持久化
```go
type HumanInputStore interface {
    SaveRequest(ctx context.Context, req HumanInputRequest) error
    SaveResponse(ctx context.Context, resp HumanInputResponse) error
    GetRequest(ctx context.Context, requestID string) (*HumanInputRequest, error)
    GetResponse(ctx context.Context, requestID string) (*HumanInputResponse, error)
    ListRequests(ctx context.Context, filter HumanInputFilter) ([]HumanInputRequest, error)
}
```
**数据对象**：
- `HumanInputRequest`：提示、超时、默认值
- `HumanInputResponse`：用户输入、批准状态、提交者
- `HumanInputFilter`：按 TaskID/Status 过滤

**使用场景**：
- 人机交互流程（SSE 推送）
- 请求/响应匹配追踪
- 超时和默认值处理

**特点**：
- Middleware 层专用
- 与 Store 无关（独立数据模型）
- 支持分页查询

---

### 5. framework/rag/types.go:VectorStore - 向量存储
**职责**：向量化文档的相似度搜索
```go
type VectorStore interface {
    Add(ctx context.Context, documents []Document, vectors [][]float64) error
    Search(ctx context.Context, queryVector []float64, topK int) ([]Document, error)
    Delete(ctx context.Context, ids []string) error
    Clear(ctx context.Context) error
}
```
**使用场景**：
- RAG（检索增强生成）
- 语义相似度搜索
- 文档向量索引

**特点**：
- 专用向量数据库接口
- 与 Store 无关
- 支持向量操作（Add/Search/Delete）

---

### 6. framework/llm/router.go:RouterStore - LLM 路由存储
**职责**：LLM 提供者路由策略存储
```go
type RouterStore interface {
    // 获取路由配置
    GetRouter(ctx context.Context, tierKey string) (*Router, error)
    
    // 保存路由配置
    SaveRouter(ctx context.Context, router *Router) error
    
    // 列出所有路由
    ListRouters(ctx context.Context) ([]*Router, error)
}
```
**数据对象**：Router（tier → provider 映射）

**使用场景**：
- LLM 提供者选择（OpenAI/Anthropic/等）
- 多层级路由策略
- 动态配置管理

**特点**：
- LLM 层专用
- 与 Store 无关
- 支持多提供者路由

---

### 7. cache/store.go + llmstore/store.go - 内部缓存 Store
**职责**：缓存层内部存储
```go
type executorStore interface {
    // 缓存 executor 相关数据
}

type skillStore interface {
    // 缓存 skill 相关数据
}

type llmStore interface {
    // 缓存 LLM 响应
}
```
**特点**：
- 未导出接口（小写）
- 缓存层专用
- 实现细节

---

## 结论

**七个 Store 接口不应统一**

| 接口 | 层级 | 职责 | 依赖关系 |
|------|------|------|---------|
| **Store** | 框架 | 统一持久化入口 | 组合 EventStore |
| **EventStore** | 框架 | 通用事件存储 | 被 Store 组合 |
| **GraphStore** | 框架 | 知识图谱存储 | 独立 |
| **HumanInputStore** | Middleware | 人工交互存储 | 独立 |
| **VectorStore** | RAG | 向量搜索 | 独立 |
| **RouterStore** | LLM | 路由配置存储 | 独立 |
| **executorStore** | Cache | 缓存实现 | 内部专用 |

### 设计原则

1. **分层职责**：每个层级定义自己的 Store 接口
2. **依赖倒置**：接口定义在消费方（middleware/rag/llm）
3. **复合而非继承**：Store 通过组合集成 EventStore
4. **独立演进**：各 Store 独立迭代不互相影响

### 下一步检查清单

- ✅ Message 类型（已统一）
- ✅ AgentConfig 类型（已补全）
- ✅ EventBus 接口（已提取）
- ✅ Event 类型（已验证）
- ✅ Store 接口（已验证，合理分离）
- ⏳ Config 类型（待深入）
- ⏳ Provider 接口（待检查）
