package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// SubtaskRunnerImpl 是子任务运行器的实现。
type SubtaskRunnerImpl struct {
	mu      sync.RWMutex
	results map[string]*SubtaskResult
	running map[string]context.CancelFunc
	logger  zerolog.Logger

	maxDepth int // 最大递归深度
}

// NewSubtaskRunnerImpl 创建子任务运行器。
func NewSubtaskRunnerImpl(logger zerolog.Logger) *SubtaskRunnerImpl {
	return &SubtaskRunnerImpl{
		results: make(map[string]*SubtaskResult),
		running: make(map[string]context.CancelFunc),
		logger:  logger.With().Str("component", "subtask_runner").Logger(),
		maxDepth: 10, // 默认最大递归深度 10
	}
}

// SetMaxDepth 设置最大递归深度。
func (r *SubtaskRunnerImpl) SetMaxDepth(depth int) {
	r.maxDepth = depth
}

// Run 运行子任务（阻塞）。
func (r *SubtaskRunnerImpl) Run(ctx context.Context, config SubtaskConfig) (*SubtaskResult, error) {
	// 生成子任务 ID
	taskID := uuid.New().String()

	r.logger.Info().
		Str("subtask_id", taskID).
		Str("parent_id", config.ParentTaskID).
		Msg("starting subtask")

	// 设置超时
	var cancel context.CancelFunc
	if config.TimeoutMs > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(config.TimeoutMs)*time.Millisecond)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	// 注册到运行列表
	r.mu.Lock()
	r.running[taskID] = cancel
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		delete(r.running, taskID)
		r.mu.Unlock()
	}()

	// 执行子任务
	startTime := time.Now()
	output, err := r.executeSubtask(ctx, config)

	result := &SubtaskResult{
		TaskID:     taskID,
		Success:    err == nil,
		Output:     output,
		DurationMs: time.Since(startTime).Milliseconds(),
	}

	if err != nil {
		result.Error = err.Error()
	}

	// 保存结果
	r.mu.Lock()
	r.results[taskID] = result
	r.mu.Unlock()

	r.logger.Info().
		Str("subtask_id", taskID).
		Bool("success", result.Success).
		Int64("duration_ms", result.DurationMs).
		Msg("subtask completed")

	return result, err
}

// RunAsync 异步运行子任务。
func (r *SubtaskRunnerImpl) RunAsync(ctx context.Context, config SubtaskConfig) (string, error) {
	// 生成子任务 ID
	taskID := uuid.New().String()

	go func() {
		result, _ := r.Run(ctx, config)
		r.mu.Lock()
		r.results[taskID] = result
		r.mu.Unlock()
	}()

	return taskID, nil
}

// Wait 等待子任务完成。
func (r *SubtaskRunnerImpl) Wait(ctx context.Context, taskID string) (*SubtaskResult, error) {
	// 轮询等待结果
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.mu.RLock()
			result, exists := r.results[taskID]
			r.mu.RUnlock()

			if exists {
				return result, nil
			}

		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Cancel 取消子任务。
func (r *SubtaskRunnerImpl) Cancel(taskID string) error {
	r.mu.Lock()
	cancel, exists := r.running[taskID]
	r.mu.Unlock()

	if !exists {
		return fmt.Errorf("subtask not found or already completed: %s", taskID)
	}

	cancel()
	r.logger.Info().Str("subtask_id", taskID).Msg("subtask canceled")
	return nil
}

// GetResult 获取子任务结果。
func (r *SubtaskRunnerImpl) GetResult(taskID string) (*SubtaskResult, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result, exists := r.results[taskID]
	if !exists {
		return nil, fmt.Errorf("subtask result not found: %s", taskID)
	}

	return result, nil
}

// ListRunning 列出运行中的子任务。
func (r *SubtaskRunnerImpl) ListRunning() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	taskIDs := make([]string, 0, len(r.running))
	for taskID := range r.running {
		taskIDs = append(taskIDs, taskID)
	}
	return taskIDs
}

// ListCompleted 列出已完成的子任务。
func (r *SubtaskRunnerImpl) ListCompleted() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	taskIDs := make([]string, 0, len(r.results))
	for taskID := range r.results {
		taskIDs = append(taskIDs, taskID)
	}
	return taskIDs
}

// CleanupResults 清理已完成的结果。
func (r *SubtaskRunnerImpl) CleanupResults(olderThan time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	for taskID, result := range r.results {
		resultTime := time.UnixMilli(now.UnixMilli() - result.DurationMs)
		if now.Sub(resultTime) > olderThan {
			delete(r.results, taskID)
		}
	}

	r.logger.Debug().Int("remaining", len(r.results)).Msg("cleanup completed")
}

// executeSubtask 执行子任务（业务逻辑占位符）。
func (r *SubtaskRunnerImpl) executeSubtask(ctx context.Context, config SubtaskConfig) (any, error) {
	// 这里是子任务的实际执行逻辑
	// 实际实现需要：
	// 1. 创建子图
	// 2. 初始化上下文
	// 3. 运行 Agent
	// 4. 返回结果

	// 占位符实现
	r.logger.Debug().
		Str("objective", config.Objective).
		Bool("isolated", config.Isolated).
		Msg("executing subtask")

	// 模拟执行
	select {
	case <-time.After(100 * time.Millisecond):
		return map[string]any{
			"objective": config.Objective,
			"status":    "completed",
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// GetStats 获取统计信息。
func (r *SubtaskRunnerImpl) GetStats() SubtaskStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := SubtaskStats{
		RunningCount:   len(r.running),
		CompletedCount: len(r.results),
		SuccessCount:   0,
		FailureCount:   0,
	}

	for _, result := range r.results {
		if result.Success {
			stats.SuccessCount++
		} else {
			stats.FailureCount++
		}
	}

	return stats
}

// SubtaskStats 是子任务统计信息。
type SubtaskStats struct {
	RunningCount   int `json:"running_count"`
	CompletedCount int `json:"completed_count"`
	SuccessCount   int `json:"success_count"`
	FailureCount   int `json:"failure_count"`
}
