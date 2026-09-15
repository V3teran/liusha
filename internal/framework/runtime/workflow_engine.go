package runtime

import (
	"context"
	"fmt"
	"sync"
)

// WorkflowEngine 是 DAG 工作流编排引擎。
//
// 核心能力：
// 1. 拓扑排序：检测环路、确定执行顺序
// 2. 并行调度：识别可并行节点，最大化吞吐
// 3. 条件路由：基于运行时状态动态选择分支
// 4. 错误传播：单节点失败时的取消策略
type WorkflowEngine interface {
	// Compile 编译工作流为可执行图
	// 编译期检查：环检测、孤立节点、类型兼容性
	Compile(ctx context.Context, workflow *Workflow) (*ExecutableGraph, error)

	// Execute 执行已编译的图
	// 返回最终输出和执行轨迹
	Execute(ctx context.Context, graph *ExecutableGraph, input any) (*ExecutionResult, error)
}

// Workflow 是工作流的声明式定义
type Workflow struct {
	// 工作流唯一标识
	ID string

	// 节点列表
	Nodes []*Node

	// 边列表
	Edges []*Edge

	// 入口节点 ID（无前驱节点）
	EntryNodes []string

	// 出口节点 ID（无后继节点）
	ExitNodes []string

	// 全局配置
	Config *WorkflowConfig
}

// Node 是工作流中的节点
type Node struct {
	// 节点唯一 ID
	ID string

	// 节点类型（用于分类统计）
	Type NodeType

	// 节点处理器（核心执行逻辑）
	Handler NodeHandler

	// 节点元数据
	Metadata map[string]any

	// 超时配置（毫秒，0 表示无限制）
	TimeoutMs int64

	// 重试配置
	Retry *RetryPolicy
}

// NodeType 节点类型
type NodeType string

const (
	NodeTypeAction    NodeType = "action"    // 动作节点（执行具体操作）
	NodeTypeCondition NodeType = "condition" // 条件节点（分支判断）
	NodeTypeMerge     NodeType = "merge"     // 汇聚节点（多分支合并）
	NodeTypeParallel  NodeType = "parallel"  // 并行节点（fan-out）
	NodeTypeSubgraph  NodeType = "subgraph"  // 子图节点（嵌套工作流）
)

// NodeHandler 节点处理器
type NodeHandler func(ctx context.Context, input *NodeInput) (*NodeOutput, error)

// NodeInput 节点输入
type NodeInput struct {
	// 上游节点的输出（多个上游时为数组）
	Data any

	// 执行上下文（跨节点共享）
	Context *ExecutionContext

	// 当前节点信息
	Node *Node
}

// NodeOutput 节点输出
type NodeOutput struct {
	// 输出数据
	Data any

	// 下一跳提示（用于条件路由）
	NextHint string

	// 是否终止工作流
	Terminate bool

	// 附加元数据
	Metadata map[string]any
}

// Edge 是节点之间的边
type Edge struct {
	// 起始节点 ID
	From string

	// 目标节点 ID
	To string

	// 边条件（nil 表示无条件边）
	Condition EdgeCondition

	// 边权重（用于多条件分支的优先级）
	Weight int
}

// EdgeCondition 边的激活条件
type EdgeCondition func(output *NodeOutput) bool

// WorkflowConfig 工作流全局配置
type WorkflowConfig struct {
	// 最大并行度（0 表示无限制）
	MaxParallelism int

	// 失败策略
	FailurePolicy FailurePolicy

	// 执行超时（毫秒）
	TimeoutMs int64

	// 是否启用追踪
	TracingEnabled bool
}

// FailurePolicy 失败策略
type FailurePolicy string

const (
	// FailurePolicyContinue 继续执行其他分支
	FailurePolicyContinue FailurePolicy = "continue"

	// FailurePolicyCancel 取消所有正在执行的节点
	FailurePolicyCancel FailurePolicy = "cancel"

	// FailurePolicyRetry 重试失败节点
	FailurePolicyRetry FailurePolicy = "retry"
)

// RetryPolicy 重试策略
type RetryPolicy struct {
	// 最大重试次数
	MaxRetries int

	// 重试间隔（毫秒）
	IntervalMs int64

	// 指数退避因子（0 表示固定间隔）
	BackoffFactor float64
}

// ExecutableGraph 是编译后的可执行图
type ExecutableGraph struct {
	// 原始工作流
	Workflow *Workflow

	// 拓扑排序后的层级（每层内节点可并行）
	Layers [][]*Node

	// 节点索引（ID -> Node）
	NodeIndex map[string]*Node

	// 邻接表（前驱和后继）
	Predecessors map[string][]*Edge
	Successors   map[string][]*Edge

	// 入口和出口节点
	EntryNodes []*Node
	ExitNodes  []*Node
}

// ExecutionContext 执行上下文（跨节点共享）
type ExecutionContext struct {
	// 工作流 ID
	WorkflowID string

	// 执行 ID（唯一）
	ExecutionID string

	// 全局状态（所有节点可读写）
	State sync.Map

	// 取消函数
	Cancel context.CancelFunc

	// 执行轨迹
	Trace *ExecutionTrace
}

// ExecutionTrace 执行轨迹
type ExecutionTrace struct {
	mu sync.Mutex

	// 节点执行记录
	NodeExecutions []*NodeExecution

	// 开始/结束时间
	StartTime int64
	EndTime   int64
}

// NodeExecution 单个节点的执行记录
type NodeExecution struct {
	NodeID    string
	StartTime int64
	EndTime   int64
	Duration  int64
	Status    ExecutionStatus
	Input     any
	Output    any
	Error     string
}

// ExecutionStatus 执行状态
type ExecutionStatus string

const (
	ExecutionStatusPending   ExecutionStatus = "pending"
	ExecutionStatusRunning   ExecutionStatus = "running"
	ExecutionStatusSuccess   ExecutionStatus = "success"
	ExecutionStatusFailed    ExecutionStatus = "failed"
	ExecutionStatusCancelled ExecutionStatus = "cancelled"
	ExecutionStatusSkipped   ExecutionStatus = "skipped"
)

// ExecutionResult 执行结果
type ExecutionResult struct {
	// 最终输出（来自出口节点）
	Output any

	// 执行状态
	Status ExecutionStatus

	// 执行轨迹
	Trace *ExecutionTrace

	// 错误信息
	Error error
}

// AppendNodeExecution 追加节点执行记录
func (t *ExecutionTrace) AppendNodeExecution(exec *NodeExecution) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.NodeExecutions = append(t.NodeExecutions, exec)
}

// GetNodeExecutions 获取所有节点执行记录
func (t *ExecutionTrace) GetNodeExecutions() []*NodeExecution {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]*NodeExecution, len(t.NodeExecutions))
	copy(result, t.NodeExecutions)
	return result
}

// Validate 验证工作流定义的合法性
func (w *Workflow) Validate() error {
	if w.ID == "" {
		return fmt.Errorf("工作流 ID 不能为空")
	}

	if len(w.Nodes) == 0 {
		return fmt.Errorf("工作流至少需要一个节点")
	}

	// 检查节点 ID 唯一性
	nodeIDs := make(map[string]bool)
	for _, node := range w.Nodes {
		if node.ID == "" {
			return fmt.Errorf("节点 ID 不能为空")
		}
		if nodeIDs[node.ID] {
			return fmt.Errorf("节点 ID 重复: %s", node.ID)
		}
		nodeIDs[node.ID] = true

		if node.Handler == nil {
			return fmt.Errorf("节点 %s 缺少 Handler", node.ID)
		}
	}

	// 检查边引用的节点是否存在
	for _, edge := range w.Edges {
		if !nodeIDs[edge.From] {
			return fmt.Errorf("边引用了不存在的起始节点: %s", edge.From)
		}
		if !nodeIDs[edge.To] {
			return fmt.Errorf("边引用了不存在的目标节点: %s", edge.To)
		}
	}

	return nil
}
