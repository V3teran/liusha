package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// GraphExecutorImpl 是图执行引擎的实现。
type GraphExecutorImpl struct {
	graph        *GraphImpl
	resolver     *DependencyResolver
	nodeExecutor NodeExecutor
	eventBus     EventBus
	checkpointer Checkpointer
	logger       zerolog.Logger

	maxConcurrency int
	failFast       bool // true: 任意节点失败立即停止，false: 继续执行
}

// NewGraphExecutor 创建图执行引擎。
func NewGraphExecutor(
	graph *GraphImpl,
	nodeExecutor NodeExecutor,
	eventBus EventBus,
	checkpointer Checkpointer,
	logger zerolog.Logger,
) *GraphExecutorImpl {
	return &GraphExecutorImpl{
		graph:          graph,
		resolver:       NewDependencyResolver(graph),
		nodeExecutor:   nodeExecutor,
		eventBus:       eventBus,
		checkpointer:   checkpointer,
		logger:         logger.With().Str("component", "graph_executor").Logger(),
		maxConcurrency: 10, // 默认最大并发 10
		failFast:       true,
	}
}

// SetMaxConcurrency 设置最大并发数。
func (e *GraphExecutorImpl) SetMaxConcurrency(n int) {
	e.maxConcurrency = n
}

// SetFailFast 设置失败策略。
func (e *GraphExecutorImpl) SetFailFast(failFast bool) {
	e.failFast = failFast
}

// Execute 执行整个图。
func (e *GraphExecutorImpl) Execute(ctx context.Context, graph Graph) error {
	// 检测循环依赖
	if err := e.resolver.DetectCycle(); err != nil {
		return fmt.Errorf("cycle detected: %w", err)
	}

	// 获取执行层级
	levels, err := e.resolver.GetExecutionLevels()
	if err != nil {
		return fmt.Errorf("get execution levels: %w", err)
	}

	e.logger.Info().Int("levels", len(levels)).Msg("executing graph")

	// 按层级执行
	for levelIdx, level := range levels {
		e.logger.Debug().
			Int("level", levelIdx).
			Int("nodes", len(level)).
			Msg("executing level")

		// 获取当前层的节点
		nodes := make([]Node, 0, len(level))
		for _, nodeID := range level {
			node, err := e.graph.GetNode(nodeID)
			if err != nil {
				return err
			}
			nodes = append(nodes, node)
		}

		// 并发执行当前层
		if err := e.ExecuteParallel(ctx, nodes); err != nil {
			if e.failFast {
				return fmt.Errorf("level %d execution failed: %w", levelIdx, err)
			}
			e.logger.Warn().Err(err).Int("level", levelIdx).Msg("level execution failed, continuing")
		}
	}

	e.logger.Info().Msg("graph execution completed")
	return nil
}

// ExecuteNode 执行单个节点。
func (e *GraphExecutorImpl) ExecuteNode(ctx context.Context, node Node) error {
	e.logger.Debug().Str("node_id", node.ID).Str("type", node.Type).Msg("executing node")

	// 更新状态为 running
	if err := e.graph.UpdateNodeState(ctx, node.ID, NodeStateRunning); err != nil {
		return err
	}

	// 发布事件
	if e.eventBus != nil {
		e.publishEvent(ctx, EventNodeStarted, map[string]any{
			"node_id": node.ID,
			"type":    node.Type,
		})
	}

	// 执行节点
	startTime := time.Now()
	result, err := e.nodeExecutor.Execute(ctx, node)

	// 更新状态
	var newState NodeState
	if err != nil {
		newState = NodeStateFailed
		e.logger.Error().Err(err).Str("node_id", node.ID).Msg("node execution failed")
	} else {
		newState = NodeStateCompleted
		e.logger.Debug().Str("node_id", node.ID).Dur("duration", time.Since(startTime)).Msg("node execution completed")
	}

	if err := e.graph.UpdateNodeState(ctx, node.ID, newState); err != nil {
		return err
	}

	// 发布事件
	if e.eventBus != nil {
		eventType := EventNodeCompleted
		if err != nil {
			eventType = EventNodeFailed
		}

		e.publishEvent(ctx, eventType, map[string]any{
			"node_id":  node.ID,
			"type":     node.Type,
			"result":   result,
			"error":    err,
			"duration": time.Since(startTime).Milliseconds(),
		})
	}

	return err
}

// ExecuteParallel 并发执行多个节点。
func (e *GraphExecutorImpl) ExecuteParallel(ctx context.Context, nodes []Node) error {
	if len(nodes) == 0 {
		return nil
	}

	// 使用 semaphore 限制并发数
	semaphore := make(chan struct{}, e.maxConcurrency)

	var wg sync.WaitGroup
	errCh := make(chan error, len(nodes))

	for _, node := range nodes {
		wg.Add(1)

		go func(n Node) {
			defer wg.Done()

			// 获取信号量
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			}

			// 执行节点
			if err := e.ExecuteNode(ctx, n); err != nil {
				errCh <- fmt.Errorf("node %s: %w", n.ID, err)
			}
		}(node)
	}

	// 等待所有节点完成
	wg.Wait()
	close(errCh)

	// 收集错误
	var errs []error
	for err := range errCh {
		errs = append(errs, err)
		if e.failFast {
			break
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("parallel execution failed: %v", errs)
	}

	return nil
}

// Resume 从 checkpoint 恢复执行。
func (e *GraphExecutorImpl) Resume(ctx context.Context, graph Graph, checkpointID CheckpointID) error {
	// 加载 checkpoint
	_, err := e.checkpointer.Load(ctx, checkpointID)
	if err != nil {
		return fmt.Errorf("load checkpoint: %w", err)
	}

	e.logger.Info().Str("checkpoint_id", string(checkpointID)).Msg("resuming from checkpoint")

	// 恢复图状态（这里简化处理，实际需要恢复节点状态）
	// TODO: 从 checkpoint 恢复节点状态

	// 获取未完成的节点
	pendingNodes, err := e.graph.ListNodes(NodeFilter{State: NodeStatePending})
	if err != nil {
		return err
	}

	readyNodes, err := e.graph.ListNodes(NodeFilter{State: NodeStateReady})
	if err != nil {
		return err
	}

	allNodes := append(pendingNodes, readyNodes...)

	e.logger.Info().Int("pending_nodes", len(allNodes)).Msg("resuming execution")

	// 继续执行
	return e.Execute(ctx, graph)
}

// publishEvent 发布事件。
func (e *GraphExecutorImpl) publishEvent(ctx context.Context, eventType EventType, data map[string]any) {
	event := Event{
		Type:      eventType,
		Data:      mustMarshal(data),
		Timestamp: time.Now().UnixMilli(),
	}

	if err := e.eventBus.Publish(ctx, event); err != nil {
		e.logger.Warn().Err(err).Str("event_type", string(eventType)).Msg("publish event failed")
	}
}

// mustMarshal 序列化（简化处理）。
func mustMarshal(v any) []byte {
	// 这里简化处理，实际应该使用 json.Marshal
	return []byte(fmt.Sprintf("%v", v))
}
