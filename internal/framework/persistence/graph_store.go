package persistence

import (
	"context"

	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// GraphStore 是知识图谱的持久化接口。
// 负责 Node/Edge/Verification 的 CRUD 操作。
type GraphStore interface {
	// ========== Node 操作 ==========

	// CreateNode 创建节点
	CreateNode(ctx context.Context, node knowledgegraph.Node) (string, error)

	// GetNode 获取节点
	GetNode(ctx context.Context, taskID, nodeID string) (*knowledgegraph.Node, error)

	// UpdateNode 更新节点（支持部分更新）
	UpdateNode(ctx context.Context, node knowledgegraph.Node) error

	// CompareAndSwapState 原子性地比较并交换节点状态（CAS操作）
	// 只有当前状态等于 expectedState 时才更新为 newState
	// 返回 (true, nil) 表示更新成功
	// 返回 (false, nil) 表示状态不匹配，未更新
	CompareAndSwapState(ctx context.Context, taskID, nodeID string, expectedState, newState string) (bool, error)

	// DeleteNode 删除节点
	DeleteNode(ctx context.Context, taskID, nodeID string) error

	// ListNodes 列出节点（支持过滤）
	ListNodes(ctx context.Context, filter NodeFilter) ([]knowledgegraph.Node, error)

	// ========== Edge 操作 ==========

	// CreateEdge 创建边
	CreateEdge(ctx context.Context, edge knowledgegraph.Edge) error

	// GetEdge 获取边
	GetEdge(ctx context.Context, taskID, srcID string, rel knowledgegraph.Relation, dstID string) (*knowledgegraph.Edge, error)

	// DeleteEdge 删除边
	DeleteEdge(ctx context.Context, taskID, srcID string, rel knowledgegraph.Relation, dstID string) error

	// ListEdges 列出边（支持过滤）
	ListEdges(ctx context.Context, filter EdgeFilter) ([]knowledgegraph.Edge, error)

	// ========== Verification 操作 ==========

	// RecordVerification 记录验证结果
	RecordVerification(ctx context.Context, v knowledgegraph.Verification) (string, error)

	// GetVerification 获取验证记录
	GetVerification(ctx context.Context, taskID, verificationID string) (*knowledgegraph.Verification, error)

	// ListVerifications 列出验证记录
	ListVerifications(ctx context.Context, taskID string) ([]knowledgegraph.Verification, error)

	// ========== 图查询 ==========

	// GetSubgraph 获取子图（从指定节点开始的所有可达节点和边）
	GetSubgraph(ctx context.Context, taskID, startNodeID string, maxDepth int) (*Subgraph, error)

	// GetActionsByState 获取指定状态的所有 action 节点
	GetActionsByState(ctx context.Context, taskID string, state knowledgegraph.State) ([]knowledgegraph.Node, error)

	// GetDependencyChain 获取依赖链（action 的所有依赖）
	GetDependencyChain(ctx context.Context, taskID, actionID string) ([]knowledgegraph.Node, error)
}

// NodeFilter 是节点查询过滤器
type NodeFilter struct {
	TaskID     string                  // 必填
	Kind       *string                 // 节点类型（action/observation/evaluation/result）
	State      *knowledgegraph.State   // action 状态
	Confidence *knowledgegraph.Confidence // observation/result 置信度
	Priority   *knowledgegraph.Priority   // 优先级
	Owner      *string                 // 所有者
	SourceType *knowledgegraph.SourceType // 来源类型
	Tags       []string                // 标签（AND 关系）
	Limit      int                     // 限制数量（0 表示无限制）
	Offset     int                     // 偏移量
}

// EdgeFilter 是边查询过滤器
type EdgeFilter struct {
	TaskID string                  // 必填
	SrcID  *string                 // 源节点 ID
	DstID  *string                 // 目标节点 ID
	Rel    *knowledgegraph.Relation // 关系类型
	Limit  int                     // 限制数量（0 表示无限制）
	Offset int                     // 偏移量
}

// Subgraph 是子图结构
type Subgraph struct {
	Nodes []knowledgegraph.Node
	Edges []knowledgegraph.Edge
}
