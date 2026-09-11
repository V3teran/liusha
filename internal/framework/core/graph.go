package core

import (
	"context"
	"encoding/json"
)

// Graph 是任务的执行图（有向图，可能有环）。
// 节点是执行单元，边是依赖关系。
type Graph interface {
	// AddNode 添加节点
	AddNode(node Node) error

	// AddEdge 添加边（from -> to）
	AddEdge(from, to string, edgeType EdgeType) error

	// GetNode 获取节点
	GetNode(id string) (Node, error)

	// ListNodes 列出所有节点
	ListNodes(filter NodeFilter) ([]Node, error)

	// ListEdges 列出所有边
	ListEdges() ([]Edge, error)

	// NextNodes 获取下一步可执行的节点（依赖已满足）
	NextNodes(ctx context.Context) ([]Node, error)

	// UpdateNodeState 更新节点状态
	UpdateNodeState(ctx context.Context, nodeID string, state NodeState) error

	// Clone 克隆图（用于子任务）
	Clone() (Graph, error)
}

// Node 是图中的节点（执行单元）。
type Node struct {
	// 节点 ID（全局唯一）
	ID string `json:"id"`

	// 节点类型（业务自定义）
	Type string `json:"type"`

	// 节点内容（业务数据）
	Content json.RawMessage `json:"content"`

	// 节点状态
	State NodeState `json:"state"`

	// 依赖的节点 ID 列表
	DependsOn []string `json:"depends_on,omitempty"`

	// 并发组（同组节点可并发执行）
	ParallelGroup string `json:"parallel_group,omitempty"`

	// 节点元数据
	Metadata NodeMetadata `json:"metadata"`
}

// NodeState 是节点的执行状态。
type NodeState string

const (
	NodeStatePending   NodeState = "pending"   // 等待执行
	NodeStateReady     NodeState = "ready"     // 依赖满足，可执行
	NodeStateRunning   NodeState = "running"   // 执行中
	NodeStateCompleted NodeState = "completed" // 已完成
	NodeStateFailed    NodeState = "failed"    // 失败
	NodeStateSkipped   NodeState = "skipped"   // 跳过
	NodeStateBlocked   NodeState = "blocked"   // 阻塞（依赖失败）
)

// NodeMetadata 是节点的元信息。
type NodeMetadata struct {
	// 创建时间（Unix 毫秒）
	CreatedAt int64 `json:"created_at"`

	// 开始时间（Unix 毫秒）
	StartedAt int64 `json:"started_at,omitempty"`

	// 完成时间（Unix 毫秒）
	CompletedAt int64 `json:"completed_at,omitempty"`

	// 执行耗时（毫秒）
	DurationMs int64 `json:"duration_ms,omitempty"`

	// 重试次数
	RetryCount int `json:"retry_count,omitempty"`

	// 错误信息
	Error string `json:"error,omitempty"`

	// 自定义标签
	Labels map[string]string `json:"labels,omitempty"`
}

// Edge 是图中的边（依赖关系）。
type Edge struct {
	// 源节点 ID
	From string `json:"from"`

	// 目标节点 ID
	To string `json:"to"`

	// 边类型
	Type EdgeType `json:"type"`

	// 条件（可选，用于条件分支）
	Condition string `json:"condition,omitempty"`
}

// EdgeType 是边的类型。
type EdgeType string

const (
	EdgeTypeDependency EdgeType = "dependency" // 依赖关系（A 完成后才能执行 B）
	EdgeTypeDataFlow   EdgeType = "dataflow"   // 数据流（A 的输出是 B 的输入）
	EdgeTypeTrigger    EdgeType = "trigger"    // 触发器（A 触发 B）
	EdgeTypeConditional EdgeType = "conditional" // 条件分支
)

// NodeFilter 是节点过滤器。
type NodeFilter struct {
	// 按类型过滤
	Type string

	// 按状态过滤
	State NodeState

	// 按标签过滤
	Labels map[string]string

	// 按并发组过滤
	ParallelGroup string
}

// GraphBuilder 是图的构建器（链式 API）。
type GraphBuilder interface {
	// Node 添加节点
	Node(id, nodeType string, content json.RawMessage) GraphBuilder

	// Edge 添加边
	Edge(from, to string, edgeType EdgeType) GraphBuilder

	// ParallelGroup 设置并发组
	ParallelGroup(nodeIDs []string, groupName string) GraphBuilder

	// Build 构建图
	Build() (Graph, error)
}

// GraphExecutor 是图的执行引擎。
type GraphExecutor interface {
	// Execute 执行图（阻塞直到完成）
	Execute(ctx context.Context, graph Graph) error

	// ExecuteNode 执行单个节点
	ExecuteNode(ctx context.Context, node Node) error

	// Resume 从 checkpoint 恢复执行
	Resume(ctx context.Context, graph Graph, checkpointID CheckpointID) error
}

// NodeExecutor 是节点的执行器（业务层实现）。
type NodeExecutor interface {
	// Execute 执行节点，返回结果
	Execute(ctx context.Context, node Node) (json.RawMessage, error)

	// CanExecute 判断节点是否可执行
	CanExecute(ctx context.Context, node Node) bool
}
