package validation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
)

// ============================================
// 知识图谱风格实现：PostgreSQL + 轮询 + CAS
// ============================================

// KnowledgeGraphOrchestrator 基于知识图谱的编排器
type KnowledgeGraphOrchestrator struct {
	taskID     string
	graphStore core.GraphStore

	// 统计
	mu    sync.Mutex
	stats ExecutionStats
}

// NewKnowledgeGraphOrchestrator 创建知识图谱编排器
func NewKnowledgeGraphOrchestrator(taskID string, graphStore core.GraphStore) *KnowledgeGraphOrchestrator {
	return &KnowledgeGraphOrchestrator{
		taskID:     taskID,
		graphStore: graphStore,
		stats: ExecutionStats{
			StartTime: time.Now(),
		},
	}
}

// Run 运行编排器（轮询模式）
func (o *KnowledgeGraphOrchestrator) Run(ctx context.Context) error {
	// 1. 初始化：Planner 生成 Actions 到知识图谱
	if err := o.initializeActions(ctx); err != nil {
		return fmt.Errorf("initialize actions: %w", err)
	}

	// 2. 主循环：轮询知识图谱
	ticker := time.NewTicker(100 * time.Millisecond) // 改为 100ms，更快响应
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-ticker.C:
			// 检查是否完成
			if o.isComplete(ctx) {
				o.mu.Lock()
				o.stats.EndTime = time.Now()
				o.mu.Unlock()
				return nil
			}

			// 执行一轮编排
			if err := o.orchestrateOnce(ctx); err != nil {
				return fmt.Errorf("orchestrate once: %w", err)
			}
		}
	}
}

// RunParallel 并行运行（多个 Executor）
func (o *KnowledgeGraphOrchestrator) RunParallel(ctx context.Context, parallelism int) error {
	// 1. 初始化 Actions
	if err := o.initializeActions(ctx); err != nil {
		return fmt.Errorf("initialize actions: %w", err)
	}

	// 2. 启动多个 workers
	var wg sync.WaitGroup
	errChan := make(chan error, parallelism)

	for i := 0; i < parallelism; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			ticker := time.NewTicker(100 * time.Millisecond) // 改为 100ms
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return

				case <-ticker.C:
					// 检查是否完成
					if o.isComplete(ctx) {
						return
					}

					// 执行一轮编排（带 CAS 竞争）
					if err := o.orchestrateOnceWithCAS(ctx, workerID); err != nil {
						errChan <- fmt.Errorf("worker-%d: %w", workerID, err)
						return
					}
				}
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// 检查错误
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	o.mu.Lock()
	o.stats.EndTime = time.Now()
	o.mu.Unlock()

	return nil
}

// initializeActions 初始化 Actions（模拟 Planner）
func (o *KnowledgeGraphOrchestrator) initializeActions(ctx context.Context) error {
	// 创建 Objective 节点
	objective := &core.GraphNode{
		ID:      fmt.Sprintf("obj-%s", o.taskID),
		Kind:    "objective",
		Content: []byte(`{"description":"scan target"}`),
		State:   "active",
		Metadata: map[string]interface{}{
			"task_id": o.taskID,
		},
	}

	if err := o.graphStore.CreateNode(ctx, objective); err != nil {
		return fmt.Errorf("create objective: %w", err)
	}

	// 创建 10 个 Action 节点
	for i := 0; i < 10; i++ {
		action := &core.GraphNode{
			ID:      fmt.Sprintf("action-%d", i+1),
			Kind:    "action",
			Content: []byte(fmt.Sprintf(`{"type":"scan","target":"target-%d"}`, i+1)),
			State:   "open",
			Metadata: map[string]interface{}{
				"task_id": o.taskID,
			},
		}

		if err := o.graphStore.CreateNode(ctx, action); err != nil {
			return fmt.Errorf("create action %d: %w", i+1, err)
		}

		// 创建 objective → action 边
		edge := &core.GraphEdge{
			From:     objective.ID,
			To:       action.ID,
			Relation: "enables",
		}

		if err := o.graphStore.CreateEdge(ctx, edge); err != nil {
			return fmt.Errorf("create edge %d: %w", i+1, err)
		}
	}

	o.mu.Lock()
	o.stats.TotalActions = 10
	o.mu.Unlock()

	return nil
}

// orchestrateOnce 执行一轮编排（单线程）
func (o *KnowledgeGraphOrchestrator) orchestrateOnce(ctx context.Context) error {
	// 1. 查询 open 状态的 Actions
	query := core.GraphNodeQuery{
		Kind: "action",
		State: "open",
		Filters: map[string]interface{}{
			"metadata.task_id": o.taskID,
		},
	}

	nodes, err := o.graphStore.ListNodes(ctx, query)
	if err != nil {
		return fmt.Errorf("list open actions: %w", err)
	}

	if len(nodes) == 0 {
		return nil
	}

	// 2. 执行第一个 Action
	action := nodes[0]

	// 更新状态为 running
	update := core.GraphNodeUpdate{
		State: "running",
	}

	if err := o.graphStore.UpdateNode(ctx, action.ID, update); err != nil {
		return fmt.Errorf("update action state: %w", err)
	}

	// 模拟执行（耗时 100ms）
	time.Sleep(100 * time.Millisecond)

	// 3. 创建 Observation 节点
	obs := &core.GraphNode{
		ID:      fmt.Sprintf("obs-%s", action.ID),
		Kind:    "observation",
		Content: []byte(`{"status":"success"}`),
		State:   "active",
		Metadata: map[string]interface{}{
			"task_id":   o.taskID,
			"action_id": action.ID,
		},
	}

	if err := o.graphStore.CreateNode(ctx, obs); err != nil {
		return fmt.Errorf("create observation: %w", err)
	}

	// 创建 action → observation 边
	edge := &core.GraphEdge{
		From:     action.ID,
		To:       obs.ID,
		Relation: "generates",
	}

	if err := o.graphStore.CreateEdge(ctx, edge); err != nil {
		return fmt.Errorf("create edge: %w", err)
	}

	// 4. 更新 Action 状态为 completed
	update = core.GraphNodeUpdate{
		State: "completed",
	}

	if err := o.graphStore.UpdateNode(ctx, action.ID, update); err != nil {
		return fmt.Errorf("update action to completed: %w", err)
	}

	o.mu.Lock()
	o.stats.CompletedActions++
	o.mu.Unlock()

	// 5. 模拟 Evaluator（10% 概率发现漏洞）
	if o.stats.CompletedActions%10 == 0 {
		finding := &core.GraphNode{
			ID:      fmt.Sprintf("finding-%d", o.stats.TotalFindings+1),
			Kind:    "finding",
			Content: []byte(`{"severity":"high","description":"SQL injection"}`),
			State:   "confirmed",
			Metadata: map[string]interface{}{
				"task_id":        o.taskID,
				"observation_id": obs.ID,
			},
		}

		if err := o.graphStore.CreateNode(ctx, finding); err != nil {
			return fmt.Errorf("create finding: %w", err)
		}

		o.mu.Lock()
		o.stats.TotalFindings++
		o.mu.Unlock()
	}

	return nil
}

// orchestrateOnceWithCAS 执行一轮编排（带 CAS 竞争）
func (o *KnowledgeGraphOrchestrator) orchestrateOnceWithCAS(ctx context.Context, workerID int) error {
	// 1. 查询 open 状态的 Actions
	query := core.GraphNodeQuery{
		Kind:  "action",
		State: "open",
		Filters: map[string]interface{}{
			"metadata.task_id": o.taskID,
		},
		Limit: 1,
	}

	nodes, err := o.graphStore.ListNodes(ctx, query)
	if err != nil {
		return fmt.Errorf("list open actions: %w", err)
	}

	if len(nodes) == 0 {
		return nil
	}

	action := nodes[0]

	// 2. CAS 操作：竞争执行权
	// 注意：这里模拟 CAS，实际需要数据库支持
	// 如果多个 worker 同时更新，只有一个会成功

	// 先读取当前状态
	current, err := o.graphStore.GetNode(ctx, action.ID)
	if err != nil {
		return fmt.Errorf("get action: %w", err)
	}

	// 如果状态已经不是 open，说明被其他 worker 抢走了
	if current.State != "open" {
		return nil
	}

	// 更新为 running（模拟 CAS）
	update := core.GraphNodeUpdate{
		State: "running",
	}

	if err := o.graphStore.UpdateNode(ctx, action.ID, update); err != nil {
		// 更新失败，可能是并发冲突
		return nil
	}

	// 3. 执行 Action
	time.Sleep(100 * time.Millisecond)

	// 4. 创建 Observation
	obs := &core.GraphNode{
		ID:      fmt.Sprintf("obs-%s", action.ID),
		Kind:    "observation",
		Content: []byte(fmt.Sprintf(`{"worker_id":%d}`, workerID)),
		State:   "active",
		Metadata: map[string]interface{}{
			"task_id":   o.taskID,
			"action_id": action.ID,
			"worker_id": workerID,
		},
	}

	if err := o.graphStore.CreateNode(ctx, obs); err != nil {
		return fmt.Errorf("create observation: %w", err)
	}

	// 5. 更新 Action 状态为 completed
	update = core.GraphNodeUpdate{
		State: "completed",
	}

	if err := o.graphStore.UpdateNode(ctx, action.ID, update); err != nil {
		return fmt.Errorf("update action to completed: %w", err)
	}

	o.mu.Lock()
	o.stats.CompletedActions++
	o.mu.Unlock()

	return nil
}

// isComplete 检查是否完成
func (o *KnowledgeGraphOrchestrator) isComplete(ctx context.Context) bool {
	query := core.GraphNodeQuery{
		Kind:  "action",
		State: "open",
		Filters: map[string]interface{}{
			"metadata.task_id": o.taskID,
		},
		Limit: 1,
	}

	nodes, err := o.graphStore.ListNodes(ctx, query)
	if err != nil {
		return false
	}

	return len(nodes) == 0
}

// GetStats 获取统计信息
func (o *KnowledgeGraphOrchestrator) GetStats() ExecutionStats {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stats
}
