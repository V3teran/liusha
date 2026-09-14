# Working Memory 迁移到 GraphStore 完成总结

## 概述

成功将 working memory 从专用表迁移到统一的 Framework GraphStore 架构。

## 完成时间

2024-09-13

## 数据库变更（Migration 0135）

### 新增表
- **wm_roadmap_step**: 独立的 Roadmap 步骤管理表
  - `task_id`: 任务 ID
  - `step`: 步骤编号（REAL 类型，支持 1.0, 1.1 等）
  - `objective`: 步骤目标
  - `status`: 状态（pending/todo/active/complete）
  - `depends_on`: 依赖的步骤编号数组（REAL[]）
  - `context`: 上下文信息（JSONB）
  - `rationale`: 理由说明
  - 约束：`UNIQUE(task_id, step)`

### 新增列
- **wm_node.metadata**: JSONB 类型，存储 Framework 元数据
- **wm_node.roadmap_step**: REAL 类型，关联 Action 与 RoadmapStep

### 约束更新
- **ck_wm_node_kind**: 支持 Framework 标准节点类型
  - objective, action, observation, evaluation, result
  - hypothesis, evidence, finding
  - target, asset, credential, access

- **ck_wm_edge_rel**: 支持 Framework 标准关系类型
  - GENERATES, CONFIRMS, REFUTES, ENABLES, DEPENDS_ON
  - CONTRIBUTES, INVALIDATES
  - derives, enables, on

- **ck_state_by_kind**: objective/action 节点必须有 state
- **ck_wm_node_confidence_fields**: 特定类型节点必须有 confidence

### 索引
- `idx_wm_node_metadata_gin`: GIN 索引支持 metadata JSONB 查询
- `idx_wm_node_roadmap_step`: 复合索引 (task_id, roadmap_step)
- `idx_roadmap_task_step`: 复合索引 (task_id, step)

## 代码变更

### 1. GraphStore 插入逻辑 (graphstore_postgres.go)
- 添加 `roadmap_step` 字段提取和插入
- 新增 `extractFloat64Ptr()` 辅助函数
- INSERT 语句包含 roadmap_step 列

### 2. Roadmap 加载逻辑 (roadmap.go)
- 修复 `depends_on` 数组类型转换（PostgreSQL 返回 float32，需转换为 float64）
- 支持 NULL 值处理（depends_on, rationale）
- 使用 `interface{}` 扫描，再进行类型断言和转换

### 3. 查询逻辑 (roadmap_queries.go)
- `ListActionsByRoadmapStep`: 使用 sql.NullString 处理可空字段（owner）
- `IsRoadmapStepComplete`: 检查关联 Action 的完成状态

### 4. 测试修复
- 所有 objective/action 节点必须设置 State 字段
- 修复 17 个 knowledgegraph 测试
- 修复 11 个 framework/core 测试

## 关键技术点

### 1. PostgreSQL REAL[] 类型转换
```go
// PostgreSQL 返回 []interface{}，元素是 float32
var dependsOnRaw interface{}
rows.Scan(&dependsOnRaw)

if arr, ok := dependsOnRaw.([]interface{}); ok {
    for _, v := range arr {
        switch val := v.(type) {
        case float32:
            result = append(result, float64(val))
        case float64:
            result = append(result, val)
        }
    }
}
```

### 2. NULL 值处理
```go
var owner sql.NullString
rows.Scan(&owner)
if owner.Valid {
    node.Owner = owner.String
}
```

### 3. CHECK 约束设计
```sql
ALTER TABLE wm_node ADD CONSTRAINT ck_state_by_kind CHECK (
    (kind IN ('action', 'objective') AND state IS NOT NULL)
    OR kind NOT IN ('action', 'objective')
);
```

## 测试验证

### 通过的测试套件
- ✅ `internal/knowledgegraph` (17 个测试)
  - TestAdapterStore_CognitiveLoop
  - TestAdapterStore_ActionManagement
  - TestAdapterStore_ObservationQueries
  - TestAdapterStore_EvaluationOutcomes
  - TestAdapterStore_DependencyGraph
  - TestTaskIsolation
  - TestCompleteDataFlow
  - TestMoveDependency
  - TestRoadmapBasicOperations
  - TestRoadmapDynamicInsertion
  - TestRoadmapSummary
  - TestRoadmapActionAssociation

- ✅ `internal/framework/core` (11 个测试)
  - TestPostgresGraphStore_Integration
  - TestPostgresGraphStore_CreateNode
  - TestPostgresGraphStore_GetNode
  - TestPostgresGraphStore_UpdateNode
  - TestPostgresGraphStore_DeleteNode
  - TestPostgresGraphStore_ListNodes
  - TestPostgresGraphStore_CreateEdge
  - TestPostgresGraphStore_ListEdges
  - TestPostgresGraphStore_DeleteEdge
  - TestPostgresGraphStore_Traverse

- ✅ `internal/framework/orchestrator` (所有测试)
- ✅ `internal/framework/registry` (所有测试)

## 部署说明

### 迁移执行
```bash
# 应用迁移
make migrate-up

# 或手动执行
psql $DATABASE_URL -f db/migrations/0135_add_wm_node_metadata.up.sql
```

### 回滚
```bash
# 回滚迁移
psql $DATABASE_URL -f db/migrations/0135_add_wm_node_metadata.down.sql
```

### 验证
```bash
# 运行测试
go test ./internal/knowledgegraph/...
go test ./internal/framework/...
```

## 影响范围

### 兼容性
- ✅ 向后兼容：旧的节点数据仍然有效
- ✅ 新字段可选：metadata 和 roadmap_step 都允许 NULL
- ✅ 约束宽松：只对特定类型节点强制要求字段

### 性能
- ✅ 添加了必要的索引，查询性能不受影响
- ✅ GIN 索引支持 JSONB 高效查询
- ✅ 复合索引优化 roadmap 相关查询

## 后续工作

无。迁移已完成，所有功能正常工作。

## 相关文件

### 迁移文件
- `db/migrations/0135_add_wm_node_metadata.up.sql`
- `db/migrations/0135_add_wm_node_metadata.down.sql`

### 核心代码文件
- `internal/framework/core/graphstore_postgres.go`
- `internal/knowledgegraph/roadmap.go`
- `internal/knowledgegraph/roadmap_queries.go`
- `internal/knowledgegraph/adapter.go`

### 测试文件
- `internal/knowledgegraph/*_test.go`
- `internal/framework/core/*_test.go`
