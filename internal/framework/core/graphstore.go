package core

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// GraphStore 是通用的图数据库接口（知识图谱存储）。
//
// 注意：这与 Graph 接口（执行图 DAG）不同：
// - Graph: 任务的执行依赖图（有向无环图 DAG）
// - GraphStore: 知识图谱的持久化存储（通用图数据库）
//
// 设计原则：
// - 只包含通用图操作（节点、边、查询）
// - 不包含业务特定逻辑（如 Action/Roadmap/Verification）
// - 业务逻辑由上层（knowledgegraph）组合实现
//
// 适用场景：
// - PostgreSQL + JSON（当前实现）
// - Neo4j / ArangoDB（未来可能）
// - 内存图数据库（测试）
type GraphStore interface {
	// ========================================
	// 节点操作
	// ========================================

	// CreateNode 创建新节点
	CreateNode(ctx context.Context, node *GraphNode) error

	// GetNode 获取节点（不存在返回 ErrGraphNodeNotFound）
	GetNode(ctx context.Context, id string) (*GraphNode, error)

	// UpdateNode 更新节点（部分更新）
	UpdateNode(ctx context.Context, id string, update GraphNodeUpdate) error

	// DeleteNode 删除节点（及其所有边）
	DeleteNode(ctx context.Context, id string) error

	// ListNodes 查询节点列表
	ListNodes(ctx context.Context, query GraphNodeQuery) ([]*GraphNode, error)

	// CompareAndSwapState 原子更新节点状态（使用乐观锁）
	// 只有当前状态为 expectedState 时才更新为 newState
	// 返回 (true, nil) 表示更新成功
	// 返回 (false, nil) 表示状态不匹配（CAS 失败）
	// 返回 (false, err) 表示发生错误
	CompareAndSwapState(ctx context.Context, id string, expectedState, newState string) (bool, error)

	// ========================================
	// 边操作
	// ========================================

	// CreateEdge 创建边（幂等：重复创建不报错）
	CreateEdge(ctx context.Context, edge *GraphEdge) error

	// ListEdges 查询边列表
	ListEdges(ctx context.Context, query GraphEdgeQuery) ([]*GraphEdge, error)

	// DeleteEdge 删除边
	DeleteEdge(ctx context.Context, from, to, relation string) error

	// ========================================
	// 图遍历
	// ========================================

	// Traverse 图遍历（BFS/DFS）
	// 从 startID 开始，按照 query 指定的关系遍历
	Traverse(ctx context.Context, startID string, query GraphTraverseQuery) ([]*GraphNode, error)
}

// GraphNode 是图节点的通用表示。
type GraphNode struct {
	// ID 是节点的唯一标识符（UUID）
	ID string `json:"id"`

	// Kind 是节点类型（如 "action", "hypothesis", "finding"）
	Kind string `json:"kind"`

	// Content 是节点的业务数据（JSON）
	Content json.RawMessage `json:"content"`

	// Metadata 是节点的元数据（任意 key-value）
	Metadata map[string]interface{} `json:"metadata,omitempty"`

	// State 是节点状态（如 "open", "running", "completed"）
	State string `json:"state,omitempty"`

	// Confidence 是置信度（0.0-1.0）
	Confidence float64 `json:"confidence,omitempty"`

	// CreatedAt 是创建时间
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt 是更新时间
	UpdatedAt time.Time `json:"updated_at"`

	// Version 是乐观锁版本号（每次更新自增）
	Version int64 `json:"version"`
}

// GraphEdge 是图边的通用表示。
type GraphEdge struct {
	// From 是起始节点 ID
	From string `json:"from"`

	// To 是目标节点 ID
	To string `json:"to"`

	// Relation 是边的关系类型（如 "depends_on", "verifies", "produces"）
	Relation string `json:"relation"`

	// Metadata 是边的元数据（可选）
	Metadata map[string]interface{} `json:"metadata,omitempty"`

	// CreatedAt 是创建时间
	CreatedAt time.Time `json:"created_at"`
}

// GraphNodeUpdate 是节点的部分更新。
type GraphNodeUpdate struct {
	// Content 更新业务数据（nil 表示不更新）
	Content json.RawMessage `json:"content,omitempty"`

	// Metadata 更新元数据（nil 表示不更新，空 map 表示清空）
	Metadata map[string]interface{} `json:"metadata,omitempty"`

	// State 更新状态（空字符串表示不更新）
	State string `json:"state,omitempty"`

	// Confidence 更新置信度（nil 表示不更新）
	Confidence *float64 `json:"confidence,omitempty"`

	// ExpectedVersion 是乐观锁的期望版本号（nil 表示不检查版本）
	// 如果提供，则只有当前版本与 ExpectedVersion 匹配时才会更新
	ExpectedVersion *int64 `json:"expected_version,omitempty"`
}

// GraphNodeQuery 是节点查询条件。
type GraphNodeQuery struct {
	// Kind 按节点类型过滤（空字符串表示不过滤）
	Kind string `json:"kind,omitempty"`

	// State 按状态过滤（空字符串表示不过滤）
	State string `json:"state,omitempty"`

	// Filters 是自定义过滤条件（key 是字段路径，value 是期望值）
	// 例如：{"metadata.task_id": "task-123"}
	Filters map[string]interface{} `json:"filters,omitempty"`

	// MinConfidence 最小置信度（0 表示不过滤）
	MinConfidence float64 `json:"min_confidence,omitempty"`

	// Limit 限制返回数量（0 表示不限制）
	Limit int `json:"limit,omitempty"`

	// Offset 分页偏移（0 表示从头开始）
	Offset int `json:"offset,omitempty"`

	// OrderBy 排序字段（如 "created_at", "-confidence" 表示降序）
	OrderBy string `json:"order_by,omitempty"`
}

// GraphEdgeQuery 是边查询条件。
type GraphEdgeQuery struct {
	// From 起始节点 ID（空字符串表示不过滤）
	From string `json:"from,omitempty"`

	// To 目标节点 ID（空字符串表示不过滤）
	To string `json:"to,omitempty"`

	// Relation 关系类型（空字符串表示不过滤）
	Relation string `json:"relation,omitempty"`

	// Limit 限制返回数量（0 表示不限制）
	Limit int `json:"limit,omitempty"`
}

// GraphTraverseQuery 是图遍历查询条件。
type GraphTraverseQuery struct {
	// Relations 要遍历的关系类型列表（空表示所有关系）
	Relations []string `json:"relations,omitempty"`

	// Direction 遍历方向："out"（出边）/"in"（入边）/"both"（双向）
	Direction GraphTraverseDirection `json:"direction"`

	// MaxDepth 最大遍历深度（0 表示不限制）
	MaxDepth int `json:"max_depth,omitempty"`

	// NodeFilter 节点过滤条件（只返回满足条件的节点）
	NodeFilter *GraphNodeQuery `json:"node_filter,omitempty"`

	// Strategy 遍历策略："bfs"（广度优先）/"dfs"（深度优先）
	Strategy GraphTraverseStrategy `json:"strategy"`
}

// GraphTraverseDirection 是遍历方向。
type GraphTraverseDirection string

const (
	// GraphTraverseOut 沿出边遍历
	GraphTraverseOut GraphTraverseDirection = "out"

	// GraphTraverseIn 沿入边遍历
	GraphTraverseIn GraphTraverseDirection = "in"

	// GraphTraverseBoth 双向遍历
	GraphTraverseBoth GraphTraverseDirection = "both"
)

// GraphTraverseStrategy 是遍历策略。
type GraphTraverseStrategy string

const (
	// GraphTraverseBFS 广度优先遍历
	GraphTraverseBFS GraphTraverseStrategy = "bfs"

	// GraphTraverseDFS 深度优先遍历
	GraphTraverseDFS GraphTraverseStrategy = "dfs"
)

// 错误定义
var (
	// ErrGraphNodeNotFound 节点不存在
	ErrGraphNodeNotFound = errors.New("graph node not found")

	// ErrGraphEdgeNotFound 边不存在
	ErrGraphEdgeNotFound = errors.New("graph edge not found")

	// ErrVersionMismatch 版本不匹配（乐观锁冲突）
	ErrVersionMismatch = errors.New("version mismatch: optimistic lock conflict")

	// ErrGraphInvalidQuery 查询条件无效
	ErrGraphInvalidQuery = errors.New("invalid graph query")
)

