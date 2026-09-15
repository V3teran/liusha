package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DefaultWorkflowEngine 是 WorkflowEngine 的默认实现
type DefaultWorkflowEngine struct {
	// 配置
	config *EngineConfig
}

// EngineConfig 引擎配置
type EngineConfig struct {
	// 默认最大并行度
	DefaultMaxParallelism int

	// 默认超时（毫秒）
	DefaultTimeoutMs int64

	// 是否启用详细日志
	VerboseLogging bool
}

// NewWorkflowEngine 创建工作流引擎
func NewWorkflowEngine(config *EngineConfig) WorkflowEngine {
	if config == nil {
		config = &EngineConfig{
			DefaultMaxParallelism: 10,
			DefaultTimeoutMs:      300000, // 5分钟
			VerboseLogging:        false,
		}
	}
	return &DefaultWorkflowEngine{
		config: config,
	}
}

// Compile 编译工作流
func (e *DefaultWorkflowEngine) Compile(ctx context.Context, workflow *Workflow) (*ExecutableGraph, error) {
	// 验证工作流定义
	if err := workflow.Validate(); err != nil {
		return nil, fmt.Errorf("工作流验证失败: %w", err)
	}

	// 构建图结构
	graph := &ExecutableGraph{
		Workflow:     workflow,
		NodeIndex:    make(map[string]*Node),
		Predecessors: make(map[string][]*Edge),
		Successors:   make(map[string][]*Edge),
	}

	// 索引节点
	for _, node := range workflow.Nodes {
		graph.NodeIndex[node.ID] = node
	}

	// 构建邻接表
	for _, edge := range workflow.Edges {
		graph.Predecessors[edge.To] = append(graph.Predecessors[edge.To], edge)
		graph.Successors[edge.From] = append(graph.Successors[edge.From], edge)
	}

	// 识别入口节点（无前驱）
	if len(workflow.EntryNodes) > 0 {
		// 使用显式指定的入口节点
		for _, nodeID := range workflow.EntryNodes {
			if node, ok := graph.NodeIndex[nodeID]; ok {
				graph.EntryNodes = append(graph.EntryNodes, node)
			}
		}
	} else {
		// 自动识别入口节点
		for _, node := range workflow.Nodes {
			if len(graph.Predecessors[node.ID]) == 0 {
				graph.EntryNodes = append(graph.EntryNodes, node)
			}
		}
	}

	// 注意：如果没有入口节点，可能是环图，留给拓扑排序检测

	// 识别出口节点（无后继）
	if len(workflow.ExitNodes) > 0 {
		for _, nodeID := range workflow.ExitNodes {
			if node, ok := graph.NodeIndex[nodeID]; ok {
				graph.ExitNodes = append(graph.ExitNodes, node)
			}
		}
	} else {
		for _, node := range workflow.Nodes {
			if len(graph.Successors[node.ID]) == 0 {
				graph.ExitNodes = append(graph.ExitNodes, node)
			}
		}
	}

	// 拓扑排序（分层）
	layers, err := e.topologicalSort(graph)
	if err != nil {
		return nil, fmt.Errorf("拓扑排序失败: %w", err)
	}
	graph.Layers = layers

	return graph, nil
}

// topologicalSort 拓扑排序，返回分层结构
// 每一层的节点可以并行执行
func (e *DefaultWorkflowEngine) topologicalSort(graph *ExecutableGraph) ([][]*Node, error) {
	// 计算每个节点的入度
	inDegree := make(map[string]int)
	for nodeID := range graph.NodeIndex {
		inDegree[nodeID] = len(graph.Predecessors[nodeID])
	}

	// 第一层：入度为 0 的节点
	var layers [][]*Node
	currentLayer := make([]*Node, 0)

	for nodeID, degree := range inDegree {
		if degree == 0 {
			currentLayer = append(currentLayer, graph.NodeIndex[nodeID])
		}
	}

	if len(currentLayer) == 0 {
		return nil, fmt.Errorf("检测到环路：没有入度为 0 的节点")
	}

	visited := make(map[string]bool)
	layers = append(layers, currentLayer)

	// 逐层剥离
	for len(currentLayer) > 0 {
		nextLayer := make([]*Node, 0)

		// 标记当前层节点已访问
		for _, node := range currentLayer {
			visited[node.ID] = true
		}

		// 查找下一层节点（所有前驱都已访问）
		for nodeID := range graph.NodeIndex {
			if visited[nodeID] {
				continue
			}

			allPredecessorsVisited := true
			for _, edge := range graph.Predecessors[nodeID] {
				if !visited[edge.From] {
					allPredecessorsVisited = false
					break
				}
			}

			if allPredecessorsVisited {
				nextLayer = append(nextLayer, graph.NodeIndex[nodeID])
			}
		}

		if len(nextLayer) > 0 {
			layers = append(layers, nextLayer)
		}

		currentLayer = nextLayer
	}

	// 检查是否所有节点都被访问（环检测）
	if len(visited) != len(graph.NodeIndex) {
		unvisited := make([]string, 0)
		for nodeID := range graph.NodeIndex {
			if !visited[nodeID] {
				unvisited = append(unvisited, nodeID)
			}
		}
		return nil, fmt.Errorf("检测到环路，未访问的节点: %v", unvisited)
	}

	return layers, nil
}

// Execute 执行工作流
func (e *DefaultWorkflowEngine) Execute(ctx context.Context, graph *ExecutableGraph, input any) (*ExecutionResult, error) {
	// 创建执行上下文
	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	executionID := uuid.New().String()
	executionContext := &ExecutionContext{
		WorkflowID:  graph.Workflow.ID,
		ExecutionID: executionID,
		Cancel:      cancel,
		Trace: &ExecutionTrace{
			StartTime:      time.Now().UnixMilli(),
			NodeExecutions: make([]*NodeExecution, 0),
		},
	}

	// 应用全局超时
	if graph.Workflow.Config != nil && graph.Workflow.Config.TimeoutMs > 0 {
		var timeoutCancel context.CancelFunc
		execCtx, timeoutCancel = context.WithTimeout(execCtx, time.Duration(graph.Workflow.Config.TimeoutMs)*time.Millisecond)
		defer timeoutCancel()
	}

	// 节点输出缓存（nodeID -> output）
	nodeOutputs := &sync.Map{}

	// 将入口输入存储到第一个入口节点
	if len(graph.EntryNodes) > 0 {
		for _, entryNode := range graph.EntryNodes {
			nodeOutputs.Store(entryNode.ID, &NodeOutput{Data: input})
		}
	}

	// 按层执行
	for layerIndex, layer := range graph.Layers {
		if err := execCtx.Err(); err != nil {
			// 上下文已取消
			return &ExecutionResult{
				Status: ExecutionStatusCancelled,
				Trace:  executionContext.Trace,
				Error:  err,
			}, err
		}

		// 并行执行当前层的所有节点
		if err := e.executeLayer(execCtx, executionContext, graph, layer, nodeOutputs); err != nil {
			// 检查是否是上下文取消
			if execCtx.Err() != nil {
				cancel()
				executionContext.Trace.EndTime = time.Now().UnixMilli()
				return &ExecutionResult{
					Status: ExecutionStatusCancelled,
					Trace:  executionContext.Trace,
					Error:  execCtx.Err(),
				}, execCtx.Err()
			}

			// 根据失败策略处理
			if graph.Workflow.Config != nil && graph.Workflow.Config.FailurePolicy == FailurePolicyContinue {
				// 继续执行（记录错误但不中断）
				continue
			}

			// 取消所有节点
			cancel()

			executionContext.Trace.EndTime = time.Now().UnixMilli()
			return &ExecutionResult{
				Status: ExecutionStatusFailed,
				Trace:  executionContext.Trace,
				Error:  fmt.Errorf("第 %d 层执行失败: %w", layerIndex, err),
			}, err
		}
	}

	// 收集出口节点的输出
	var finalOutput any
	if len(graph.ExitNodes) == 1 {
		// 单出口：直接返回
		if output, ok := nodeOutputs.Load(graph.ExitNodes[0].ID); ok {
			if nodeOutput, ok := output.(*NodeOutput); ok {
				finalOutput = nodeOutput.Data
			}
		}
	} else if len(graph.ExitNodes) > 1 {
		// 多出口：返回 map
		outputs := make(map[string]any)
		for _, exitNode := range graph.ExitNodes {
			if output, ok := nodeOutputs.Load(exitNode.ID); ok {
				if nodeOutput, ok := output.(*NodeOutput); ok {
					outputs[exitNode.ID] = nodeOutput.Data
				}
			}
		}
		finalOutput = outputs
	}

	executionContext.Trace.EndTime = time.Now().UnixMilli()

	return &ExecutionResult{
		Output: finalOutput,
		Status: ExecutionStatusSuccess,
		Trace:  executionContext.Trace,
	}, nil
}

// executeLayer 并行执行一层节点
func (e *DefaultWorkflowEngine) executeLayer(
	ctx context.Context,
	execCtx *ExecutionContext,
	graph *ExecutableGraph,
	layer []*Node,
	nodeOutputs *sync.Map,
) error {
	// 确定并行度
	maxParallelism := e.config.DefaultMaxParallelism
	if graph.Workflow.Config != nil && graph.Workflow.Config.MaxParallelism > 0 {
		maxParallelism = graph.Workflow.Config.MaxParallelism
	}

	// 并行执行节点
	var wg sync.WaitGroup
	errChan := make(chan error, len(layer))
	semaphore := make(chan struct{}, maxParallelism)

	for _, node := range layer {
		wg.Add(1)
		go func(node *Node) {
			defer wg.Done()

			// 限流
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// 执行节点
			if err := e.executeNode(ctx, execCtx, graph, node, nodeOutputs); err != nil {
				errChan <- err
			}
		}(node)
	}

	wg.Wait()
	close(errChan)

	// 检查错误
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	return nil
}

// executeNode 执行单个节点
func (e *DefaultWorkflowEngine) executeNode(
	ctx context.Context,
	execCtx *ExecutionContext,
	graph *ExecutableGraph,
	node *Node,
	nodeOutputs *sync.Map,
) error {
	// 检查是否有任何前驱边满足条件
	shouldExecute := false
	predecessors := graph.Predecessors[node.ID]

	if len(predecessors) == 0 {
		// 入口节点，总是执行
		shouldExecute = true
	} else {
		// 检查是否至少有一条边满足条件
		for _, edge := range predecessors {
			if output, ok := nodeOutputs.Load(edge.From); ok {
				if nodeOutput, ok := output.(*NodeOutput); ok {
					if edge.Condition == nil || edge.Condition(nodeOutput) {
						shouldExecute = true
						break
					}
				}
			}
		}
	}

	if !shouldExecute {
		// 跳过执行
		execution := &NodeExecution{
			NodeID:    node.ID,
			StartTime: time.Now().UnixMilli(),
			EndTime:   time.Now().UnixMilli(),
			Duration:  0,
			Status:    ExecutionStatusSkipped,
		}
		execCtx.Trace.AppendNodeExecution(execution)
		return nil
	}

	startTime := time.Now().UnixMilli()

	// 准备节点输入
	nodeInput, err := e.prepareNodeInput(node, graph, nodeOutputs, execCtx)
	if err != nil {
		return fmt.Errorf("准备节点 %s 输入失败: %w", node.ID, err)
	}

	// 应用节点超时
	nodeCtx := ctx
	if node.TimeoutMs > 0 {
		var cancel context.CancelFunc
		nodeCtx, cancel = context.WithTimeout(ctx, time.Duration(node.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	// 执行节点（带重试）
	var output *NodeOutput
	var execErr error

	retryPolicy := node.Retry
	if retryPolicy == nil {
		retryPolicy = &RetryPolicy{MaxRetries: 0}
	}

	for attempt := 0; attempt <= retryPolicy.MaxRetries; attempt++ {
		output, execErr = node.Handler(nodeCtx, nodeInput)
		if execErr == nil {
			break
		}

		// 检查是否是上下文超时/取消
		if nodeCtx.Err() != nil {
			execErr = nodeCtx.Err()
			break
		}

		// 重试间隔
		if attempt < retryPolicy.MaxRetries && retryPolicy.IntervalMs > 0 {
			interval := time.Duration(retryPolicy.IntervalMs) * time.Millisecond
			if retryPolicy.BackoffFactor > 0 {
				// 指数退避
				interval = time.Duration(float64(interval) * (1 + retryPolicy.BackoffFactor*float64(attempt)))
			}
			time.Sleep(interval)
		}
	}

	endTime := time.Now().UnixMilli()

	// 记录执行
	execution := &NodeExecution{
		NodeID:    node.ID,
		StartTime: startTime,
		EndTime:   endTime,
		Duration:  endTime - startTime,
		Input:     nodeInput.Data,
	}

	if execErr != nil {
		execution.Status = ExecutionStatusFailed
		execution.Error = execErr.Error()
		execCtx.Trace.AppendNodeExecution(execution)
		return fmt.Errorf("节点 %s 执行失败: %w", node.ID, execErr)
	}

	execution.Status = ExecutionStatusSuccess
	execution.Output = output.Data
	execCtx.Trace.AppendNodeExecution(execution)

	// 存储输出
	nodeOutputs.Store(node.ID, output)

	return nil
}

// prepareNodeInput 准备节点输入
func (e *DefaultWorkflowEngine) prepareNodeInput(
	node *Node,
	graph *ExecutableGraph,
	nodeOutputs *sync.Map,
	execCtx *ExecutionContext,
) (*NodeInput, error) {
	predecessors := graph.Predecessors[node.ID]

	var inputData any

	if len(predecessors) == 0 {
		// 入口节点：使用初始输入
		if output, ok := nodeOutputs.Load(node.ID); ok {
			if nodeOutput, ok := output.(*NodeOutput); ok {
				inputData = nodeOutput.Data
			}
		}
	} else if len(predecessors) == 1 {
		// 单前驱：直接传递
		predID := predecessors[0].From
		if output, ok := nodeOutputs.Load(predID); ok {
			if nodeOutput, ok := output.(*NodeOutput); ok {
				// 检查边条件
				if predecessors[0].Condition == nil || predecessors[0].Condition(nodeOutput) {
					inputData = nodeOutput.Data
				}
			}
		}
	} else {
		// 多前驱：合并为数组
		inputs := make([]any, 0, len(predecessors))
		for _, pred := range predecessors {
			if output, ok := nodeOutputs.Load(pred.From); ok {
				if nodeOutput, ok := output.(*NodeOutput); ok {
					// 检查边条件
					if pred.Condition == nil || pred.Condition(nodeOutput) {
						inputs = append(inputs, nodeOutput.Data)
					}
				}
			}
		}
		inputData = inputs
	}

	return &NodeInput{
		Data:    inputData,
		Context: execCtx,
		Node:    node,
	}, nil
}
