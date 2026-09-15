package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorkflowEngine_LinearChain 测试线性链
func TestWorkflowEngine_LinearChain(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	// 构建工作流：A -> B -> C
	workflow := &Workflow{
		ID: "linear-workflow",
		Nodes: []*Node{
			{
				ID:   "A",
				Type: NodeTypeAction,
				Handler: func(ctx context.Context, input *NodeInput) (*NodeOutput, error) {
					return &NodeOutput{Data: "A-output"}, nil
				},
			},
			{
				ID:   "B",
				Type: NodeTypeAction,
				Handler: func(ctx context.Context, input *NodeInput) (*NodeOutput, error) {
					inputStr := input.Data.(string)
					return &NodeOutput{Data: inputStr + " -> B-output"}, nil
				},
			},
			{
				ID:   "C",
				Type: NodeTypeAction,
				Handler: func(ctx context.Context, input *NodeInput) (*NodeOutput, error) {
					inputStr := input.Data.(string)
					return &NodeOutput{Data: inputStr + " -> C-output"}, nil
				},
			},
		},
		Edges: []*Edge{
			{From: "A", To: "B"},
			{From: "B", To: "C"},
		},
	}

	// 编译
	graph, err := engine.Compile(context.Background(), workflow)
	require.NoError(t, err)
	require.NotNil(t, graph)

	// 验证层次结构
	assert.Equal(t, 3, len(graph.Layers))
	assert.Equal(t, 1, len(graph.Layers[0])) // A
	assert.Equal(t, 1, len(graph.Layers[1])) // B
	assert.Equal(t, 1, len(graph.Layers[2])) // C

	// 执行
	result, err := engine.Execute(context.Background(), graph, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, ExecutionStatusSuccess, result.Status)
	assert.Equal(t, "A-output -> B-output -> C-output", result.Output)
	assert.Equal(t, 3, len(result.Trace.GetNodeExecutions()))
}

// TestWorkflowEngine_ParallelExecution 测试并行执行
func TestWorkflowEngine_ParallelExecution(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	// 构建工作流：
	//     A
	//    / \
	//   B   C
	//    \ /
	//     D
	workflow := &Workflow{
		ID: "parallel-workflow",
		Nodes: []*Node{
			{
				ID:   "A",
				Type: NodeTypeAction,
				Handler: func(ctx context.Context, input *NodeInput) (*NodeOutput, error) {
					return &NodeOutput{Data: 1}, nil
				},
			},
			{
				ID:   "B",
				Type: NodeTypeAction,
				Handler: func(ctx context.Context, input *NodeInput) (*NodeOutput, error) {
					time.Sleep(50 * time.Millisecond)
					return &NodeOutput{Data: 2}, nil
				},
			},
			{
				ID:   "C",
				Type: NodeTypeAction,
				Handler: func(ctx context.Context, input *NodeInput) (*NodeOutput, error) {
					time.Sleep(50 * time.Millisecond)
					return &NodeOutput{Data: 3}, nil
				},
			},
			{
				ID:   "D",
				Type: NodeTypeMerge,
				Handler: func(ctx context.Context, input *NodeInput) (*NodeOutput, error) {
					inputs := input.Data.([]any)
					sum := 0
					for _, v := range inputs {
						sum += v.(int)
					}
					return &NodeOutput{Data: sum}, nil
				},
			},
		},
		Edges: []*Edge{
			{From: "A", To: "B"},
			{From: "A", To: "C"},
			{From: "B", To: "D"},
			{From: "C", To: "D"},
		},
	}

	// 编译
	graph, err := engine.Compile(context.Background(), workflow)
	require.NoError(t, err)

	// 验证层次结构
	assert.Equal(t, 3, len(graph.Layers))
	assert.Equal(t, 1, len(graph.Layers[0])) // A
	assert.Equal(t, 2, len(graph.Layers[1])) // B, C（并行）
	assert.Equal(t, 1, len(graph.Layers[2])) // D

	// 执行
	startTime := time.Now()
	result, err := engine.Execute(context.Background(), graph, nil)
	duration := time.Since(startTime)

	require.NoError(t, err)
	assert.Equal(t, ExecutionStatusSuccess, result.Status)
	assert.Equal(t, 5, result.Output) // 2 + 3

	// 验证并行执行（总时间应接近 50ms，而非 100ms）
	assert.Less(t, duration, 80*time.Millisecond)
}

// TestWorkflowEngine_ConditionalBranch 测试条件分支
func TestWorkflowEngine_ConditionalBranch(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	// 构建工作流：
	//     A
	//    / \
	//   B   C  (条件分支)
	workflow := &Workflow{
		ID: "conditional-workflow",
		Nodes: []*Node{
			{
				ID:   "A",
				Type: NodeTypeCondition,
				Handler: func(ctx context.Context, input *NodeInput) (*NodeOutput, error) {
					value := input.Data.(int)
					return &NodeOutput{
						Data:     value,
						NextHint: "branch-positive",
					}, nil
				},
			},
			{
				ID:   "B",
				Type: NodeTypeAction,
				Handler: func(ctx context.Context, input *NodeInput) (*NodeOutput, error) {
					return &NodeOutput{Data: "positive-path"}, nil
				},
			},
			{
				ID:   "C",
				Type: NodeTypeAction,
				Handler: func(ctx context.Context, input *NodeInput) (*NodeOutput, error) {
					return &NodeOutput{Data: "negative-path"}, nil
				},
			},
		},
		Edges: []*Edge{
			{
				From: "A",
				To:   "B",
				Condition: func(output *NodeOutput) bool {
					return output.NextHint == "branch-positive"
				},
			},
			{
				From: "A",
				To:   "C",
				Condition: func(output *NodeOutput) bool {
					return output.NextHint == "branch-negative"
				},
			},
		},
	}

	// 编译
	graph, err := engine.Compile(context.Background(), workflow)
	require.NoError(t, err)

	// 执行（正数分支）
	result, err := engine.Execute(context.Background(), graph, 10)
	require.NoError(t, err)
	assert.Equal(t, ExecutionStatusSuccess, result.Status)

	// 验证只执行了 B，没有执行 C
	executions := result.Trace.GetNodeExecutions()
	successfulExecutions := 0
	var lastSuccessfulExecution *NodeExecution
	for _, exec := range executions {
		if exec.Status == ExecutionStatusSuccess {
			successfulExecutions++
			lastSuccessfulExecution = exec
		}
	}
	assert.Equal(t, 2, successfulExecutions) // A + B
	assert.Equal(t, "B", lastSuccessfulExecution.NodeID)
	assert.Equal(t, "positive-path", lastSuccessfulExecution.Output)
}

// TestWorkflowEngine_CycleDetection 测试环检测
func TestWorkflowEngine_CycleDetection(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	// 构建有环的工作流：A -> B -> C -> A
	workflow := &Workflow{
		ID: "cycle-workflow",
		Nodes: []*Node{
			{
				ID:      "A",
				Type:    NodeTypeAction,
				Handler: func(ctx context.Context, input *NodeInput) (*NodeOutput, error) { return &NodeOutput{}, nil },
			},
			{
				ID:      "B",
				Type:    NodeTypeAction,
				Handler: func(ctx context.Context, input *NodeInput) (*NodeOutput, error) { return &NodeOutput{}, nil },
			},
			{
				ID:      "C",
				Type:    NodeTypeAction,
				Handler: func(ctx context.Context, input *NodeInput) (*NodeOutput, error) { return &NodeOutput{}, nil },
			},
		},
		Edges: []*Edge{
			{From: "A", To: "B"},
			{From: "B", To: "C"},
			{From: "C", To: "A"}, // 环
		},
	}

	// 编译应该失败
	graph, err := engine.Compile(context.Background(), workflow)
	assert.Error(t, err)
	assert.Nil(t, graph)
	assert.Contains(t, err.Error(), "环路")
}

// TestWorkflowEngine_Timeout 测试超时处理
func TestWorkflowEngine_Timeout(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	workflow := &Workflow{
		ID: "timeout-workflow",
		Nodes: []*Node{
			{
				ID:        "slow-node",
				Type:      NodeTypeAction,
				TimeoutMs: 100, // 100ms 超时
				Handler: func(ctx context.Context, input *NodeInput) (*NodeOutput, error) {
					select {
					case <-time.After(200 * time.Millisecond):
						return &NodeOutput{Data: "done"}, nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				},
			},
		},
	}

	graph, err := engine.Compile(context.Background(), workflow)
	require.NoError(t, err)

	// 执行应该超时
	result, err := engine.Execute(context.Background(), graph, nil)
	assert.Error(t, err)
	assert.Equal(t, ExecutionStatusFailed, result.Status)
}

// TestWorkflowEngine_RetryPolicy 测试重试策略
func TestWorkflowEngine_RetryPolicy(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	attempts := 0
	workflow := &Workflow{
		ID: "retry-workflow",
		Nodes: []*Node{
			{
				ID:   "retry-node",
				Type: NodeTypeAction,
				Retry: &RetryPolicy{
					MaxRetries: 2,
					IntervalMs: 10,
				},
				Handler: func(ctx context.Context, input *NodeInput) (*NodeOutput, error) {
					attempts++
					if attempts < 3 {
						return nil, assert.AnError
					}
					return &NodeOutput{Data: "success"}, nil
				},
			},
		},
	}

	graph, err := engine.Compile(context.Background(), workflow)
	require.NoError(t, err)

	result, err := engine.Execute(context.Background(), graph, nil)
	require.NoError(t, err)
	assert.Equal(t, ExecutionStatusSuccess, result.Status)
	assert.Equal(t, 3, attempts) // 初次 + 2 次重试
}

// TestWorkflowEngine_MultipleExitNodes 测试多个出口节点
func TestWorkflowEngine_MultipleExitNodes(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	// 构建工作流：
	//     A
	//    / \
	//   B   C  (两个出口)
	workflow := &Workflow{
		ID: "multi-exit-workflow",
		Nodes: []*Node{
			{
				ID:      "A",
				Type:    NodeTypeAction,
				Handler: func(ctx context.Context, input *NodeInput) (*NodeOutput, error) { return &NodeOutput{Data: 1}, nil },
			},
			{
				ID:      "B",
				Type:    NodeTypeAction,
				Handler: func(ctx context.Context, input *NodeInput) (*NodeOutput, error) { return &NodeOutput{Data: 2}, nil },
			},
			{
				ID:      "C",
				Type:    NodeTypeAction,
				Handler: func(ctx context.Context, input *NodeInput) (*NodeOutput, error) { return &NodeOutput{Data: 3}, nil },
			},
		},
		Edges: []*Edge{
			{From: "A", To: "B"},
			{From: "A", To: "C"},
		},
	}

	graph, err := engine.Compile(context.Background(), workflow)
	require.NoError(t, err)

	result, err := engine.Execute(context.Background(), graph, nil)
	require.NoError(t, err)

	// 多出口返回 map
	outputs := result.Output.(map[string]any)
	assert.Equal(t, 2, outputs["B"])
	assert.Equal(t, 3, outputs["C"])
}

// TestWorkflowEngine_ContextCancellation 测试上下文取消
func TestWorkflowEngine_ContextCancellation(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	workflow := &Workflow{
		ID: "cancellation-workflow",
		Nodes: []*Node{
			{
				ID:   "long-running",
				Type: NodeTypeAction,
				Handler: func(ctx context.Context, input *NodeInput) (*NodeOutput, error) {
					select {
					case <-time.After(1 * time.Second):
						return &NodeOutput{Data: "done"}, nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				},
			},
		},
	}

	graph, err := engine.Compile(context.Background(), workflow)
	require.NoError(t, err)

	// 创建可取消的上下文
	ctx, cancel := context.WithCancel(context.Background())

	// 100ms 后取消
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	result, err := engine.Execute(ctx, graph, nil)
	assert.Error(t, err)
	assert.Equal(t, ExecutionStatusCancelled, result.Status)
}
