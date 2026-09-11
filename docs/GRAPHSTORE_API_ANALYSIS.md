# KnowledgeGraph API 使用分析

## 方法分类

### 1. 节点 CRUD（10 个方法）
- `CreateNode` - 创建节点 ✅ 核心
- `GetNode` - 获取节点 ✅ 核心
- `DeleteNode` - 删除节点 ✅ 核心
- `UpdateNodeMetadata` - 更新节点元数据 ✅ 核心
- `UpdateNodeConfidence` - 更新节点置信度
- `ListNodesByKind` - 按类型列出节点 ✅ 核心
- `CompareAndSwapState` - CAS 状态更新（并发控制）
- `CompareAndSwapStateWithMetadata` - CAS + 元数据
- `CountActionsByFingerprint` - 统计（去重）
- `CountActionsByRoadmapStep` - 统计（按步骤）

### 2. 边 CRUD（5 个方法）
- `CreateEdge` - 创建边 ✅ 核心
- `ListEdgesFrom` - 列出出边 ✅ 核心
- `ListEdgesTo` - 列出入边 ✅ 核心
- `ListEdgesByRelation` - 按关系类型列出边 ✅ 核心

### 3. Action 专用（12 个方法）⚠️ 业务特定
- `UpdateActionState` - 更新 Action 状态
- `ListOpenActions` - 列出待执行 Action
- `ListRunningActions` - 列出运行中 Action
- `ListCompletedActions` - 列出已完成 Action
- `ListActionsByState` - 按状态列出 Action
- `ListActionsByRoadmapStep` - 按 Roadmap 步骤列出
- `ListAllActions` - 列出所有 Action
- `GetNextExecutableStep` - 获取下一个可执行步骤
- `HasPendingAction` - 检查是否有待执行 Action
- `FindNextStepNumber` - 查找下一个步骤编号
- `InsertStepBetween` - 在两步之间插入
- `GetActiveSteps` - 获取活跃步骤

### 4. Roadmap 专用（8 个方法）⚠️ 业务特定
- `LoadRoadmap` - 加载路线图
- `SaveRoadmap` - 保存路线图
- `GetRoadmapSummary` - 获取路线图摘要
- `HasRoadmap` - 检查是否有路线图
- `IsRoadmapStepComplete` - 检查步骤是否完成
- `GetStepByNumber` - 按编号获取步骤
- `UpdateStepStatus` - 更新步骤状态
- `UpdateStepContext` - 更新步骤上下文
- `CountStepsByStatus` - 按状态统计步骤

### 5. Verification 专用（5 个方法）⚠️ 业务特定
- `RecordVerification` - 记录验证结果
- `GetVerification` - 获取验证结果
- `ListHypotheses` - 列出假设
- `ListUnverifiedHypotheses` - 列出未验证假设
- `FindVerifiedObservation` - 查找已验证观察

### 6. Results 专用（3 个方法）⚠️ 业务特定
- `ListResults` - 列出结果
- `ListVerifiedResults` - 列出已验证结果
- `GetObjective` - 获取目标

---

## 核心操作统计

### 通用图操作（GraphStore 接口应包含）
```
节点：
- CreateNode: 13 次
- GetNode: 8 次
- UpdateNodeMetadata: 2 次
- ListNodesByKind: 5 次

边：
- CreateEdge: 3 次
- ListEdgesFrom/To: 少量
```

### 业务特定操作（不应在 GraphStore 接口中）
```
Action 相关: 12 个方法
Roadmap 相关: 8 个方法
Verification 相关: 5 个方法
Results 相关: 3 个方法
```

---

## 接口设计建议

### 方案 1：纯净 GraphStore（推荐）✅

**只包含通用图操作：**
```go
type GraphStore interface {
    // 节点 CRUD
    CreateNode(ctx context.Context, node *Node) error
    GetNode(ctx context.Context, id string) (*Node, error)
    UpdateNode(ctx context.Context, id string, updates NodeUpdate) error
    DeleteNode(ctx context.Context, id string) error
    
    // 节点查询
    ListNodes(ctx context.Context, query NodeQuery) ([]*Node, error)
    
    // 边 CRUD
    CreateEdge(ctx context.Context, edge *Edge) error
    GetEdges(ctx context.Context, query EdgeQuery) ([]*Edge, error)
    
    // 图遍历
    Traverse(ctx context.Context, start string, query TraverseQuery) ([]*Node, error)
}
```

**优点：**
- 接口干净，职责单一
- 适用于任何图数据库
- 业务逻辑在上层（knowledgegraph 层）

**缺点：**
- 业务层需要自己组合查询（如 ListOpenActions）

### 方案 2：混合接口（不推荐）❌

包含业务特定方法（ListOpenActions、GetRoadmapSummary 等）

**缺点：**
- 接口臃肿，难以复用
- 绑定到 Liusha 的业务逻辑
- 换图数据库时需要重新实现所有业务逻辑

---

## 结论

**推荐方案 1：纯净 GraphStore**

1. Framework 提供通用图操作接口
2. knowledgegraph 实现接口 + 提供业务方法
3. 业务方法内部组合调用 GraphStore 接口

**示例：**
```go
// internal/knowledgegraph/store.go

type Store struct {
    graph core.GraphStore  // 通用图接口
    pool  *pgxpool.Pool     // 数据库连接池
}

// 实现 core.GraphStore 接口
func (s *Store) CreateNode(...) { ... }
func (s *Store) GetNode(...) { ... }

// 业务特定方法（不在接口中）
func (s *Store) ListOpenActions(ctx context.Context) ([]*Action, error) {
    // 内部使用 s.graph.ListNodes() 组合查询
    query := NodeQuery{
        Kind: "action",
        Filters: map[string]interface{}{
            "state": "open",
        },
    }
    nodes, err := s.graph.ListNodes(ctx, query)
    // 转换为 Action 类型
    return toActions(nodes), nil
}
```

---

## 下一步

**Phase 10B-PoC Step 2：设计 GraphStore 接口**

根据上述分析，设计包含以下方法的接口：
- CreateNode / GetNode / UpdateNode / DeleteNode
- ListNodes (支持按 Kind/Filters 查询)
- CreateEdge / GetEdges
- Traverse (图遍历)

**预计耗时：半天**
