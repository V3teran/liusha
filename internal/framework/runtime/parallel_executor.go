package runtime

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

// ParallelExecutor 是并发执行器。
type ParallelExecutor struct {
	maxConcurrency int
	semaphore      chan struct{}
	logger         zerolog.Logger

	// 任务管理
	mu      sync.RWMutex
	tasks   map[string]*taskHandle
	running atomic.Int32
}

// taskHandle 是任务句柄。
type taskHandle struct {
	id     string
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

// NewParallelExecutor 创建并发执行器。
func NewParallelExecutor(maxConcurrency int, logger zerolog.Logger) *ParallelExecutor {
	return &ParallelExecutor{
		maxConcurrency: maxConcurrency,
		semaphore:      make(chan struct{}, maxConcurrency),
		logger:         logger.With().Str("component", "parallel_executor").Logger(),
		tasks:          make(map[string]*taskHandle),
	}
}

// Execute 并发执行任务。
func (e *ParallelExecutor) Execute(ctx context.Context, taskID string, fn func(context.Context) error) error {
	// 获取信号量
	select {
	case e.semaphore <- struct{}{}:
		defer func() { <-e.semaphore }()
	case <-ctx.Done():
		return ctx.Err()
	}

	// 创建任务上下文
	taskCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	handle := &taskHandle{
		id:     taskID,
		cancel: cancel,
		done:   make(chan struct{}),
	}

	e.mu.Lock()
	e.tasks[taskID] = handle
	e.mu.Unlock()

	e.running.Add(1)
	defer e.running.Add(-1)

	e.logger.Debug().Str("task_id", taskID).Msg("task started")

	// 执行任务
	startTime := time.Now()
	handle.err = fn(taskCtx)
	close(handle.done)

	e.logger.Debug().
		Str("task_id", taskID).
		Dur("duration", time.Since(startTime)).
		Err(handle.err).
		Msg("task completed")

	return handle.err
}

// ExecuteParallel 并发执行多个任务。
func (e *ParallelExecutor) ExecuteParallel(ctx context.Context, tasks map[string]func(context.Context) error) map[string]error {
	results := make(map[string]error)
	var mu sync.Mutex

	var wg sync.WaitGroup
	wg.Add(len(tasks))

	for taskID, fn := range tasks {
		go func(id string, f func(context.Context) error) {
			defer wg.Done()

			err := e.Execute(ctx, id, f)

			mu.Lock()
			results[id] = err
			mu.Unlock()
		}(taskID, fn)
	}

	wg.Wait()
	return results
}

// ExecuteBatch 批量执行任务（按批次限制并发）。
func (e *ParallelExecutor) ExecuteBatch(ctx context.Context, taskIDs []string, fn func(context.Context, string) error) []error {
	results := make([]error, len(taskIDs))
	var mu sync.Mutex

	var wg sync.WaitGroup
	wg.Add(len(taskIDs))

	for i, taskID := range taskIDs {
		go func(idx int, id string) {
			defer wg.Done()

			err := e.Execute(ctx, id, func(ctx context.Context) error {
				return fn(ctx, id)
			})

			mu.Lock()
			results[idx] = err
			mu.Unlock()
		}(i, taskID)
	}

	wg.Wait()
	return results
}

// SetMaxConcurrency 设置最大并发数。
func (e *ParallelExecutor) SetMaxConcurrency(n int) {
	e.maxConcurrency = n
	e.semaphore = make(chan struct{}, n)
}

// WaitAll 等待所有任务完成。
func (e *ParallelExecutor) WaitAll(ctx context.Context) error {
	e.mu.RLock()
	tasks := make([]*taskHandle, 0, len(e.tasks))
	for _, handle := range e.tasks {
		tasks = append(tasks, handle)
	}
	e.mu.RUnlock()

	for _, handle := range tasks {
		select {
		case <-handle.done:
			// 任务完成
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

// CancelAll 取消所有任务。
func (e *ParallelExecutor) CancelAll() {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, handle := range e.tasks {
		handle.cancel()
	}

	e.logger.Info().Int("tasks", len(e.tasks)).Msg("all tasks canceled")
}

// Cancel 取消指定任务。
func (e *ParallelExecutor) Cancel(taskID string) error {
	e.mu.RLock()
	handle, exists := e.tasks[taskID]
	e.mu.RUnlock()

	if !exists {
		return fmt.Errorf("task not found: %s", taskID)
	}

	handle.cancel()
	e.logger.Debug().Str("task_id", taskID).Msg("task canceled")
	return nil
}

// GetStatus 获取任务状态。
func (e *ParallelExecutor) GetStatus(taskID string) (TaskStatus, error) {
	e.mu.RLock()
	handle, exists := e.tasks[taskID]
	e.mu.RUnlock()

	if !exists {
		return TaskStatus{}, fmt.Errorf("task not found: %s", taskID)
	}

	select {
	case <-handle.done:
		return TaskStatus{
			TaskID:    taskID,
			State:     "completed",
			Error:     handle.err,
		}, nil
	default:
		return TaskStatus{
			TaskID:    taskID,
			State:     "running",
		}, nil
	}
}

// ListRunning 列出正在运行的任务。
func (e *ParallelExecutor) ListRunning() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	running := make([]string, 0)
	for taskID, handle := range e.tasks {
		select {
		case <-handle.done:
			// 已完成
		default:
			running = append(running, taskID)
		}
	}

	return running
}

// Stats 获取执行器统计信息。
func (e *ParallelExecutor) Stats() ExecutorStats {
	e.mu.RLock()
	defer e.mu.RUnlock()

	stats := ExecutorStats{
		MaxConcurrency:  e.maxConcurrency,
		RunningTasks:    int(e.running.Load()),
		TotalTasks:      len(e.tasks),
		CompletedTasks:  0,
	}

	for _, handle := range e.tasks {
		select {
		case <-handle.done:
			stats.CompletedTasks++
		default:
		}
	}

	return stats
}

// Cleanup 清理已完成的任务。
func (e *ParallelExecutor) Cleanup() {
	e.mu.Lock()
	defer e.mu.Unlock()

	for taskID, handle := range e.tasks {
		select {
		case <-handle.done:
			delete(e.tasks, taskID)
		default:
		}
	}

	e.logger.Debug().Int("remaining", len(e.tasks)).Msg("cleanup completed")
}

// TaskStatus 是任务状态。
type TaskStatus struct {
	TaskID string `json:"task_id"`
	State  string `json:"state"` // running, completed, failed, canceled
	Error  error  `json:"error,omitempty"`
}

// ExecutorStats 是执行器统计信息。
type ExecutorStats struct {
	MaxConcurrency int `json:"max_concurrency"`
	RunningTasks   int `json:"running_tasks"`
	TotalTasks     int `json:"total_tasks"`
	CompletedTasks int `json:"completed_tasks"`
}

// ============================================
// 高级并发模式
// ============================================

// ExecuteWithRetry 执行任务并重试。
func (e *ParallelExecutor) ExecuteWithRetry(ctx context.Context, taskID string, maxRetries int, fn func(context.Context) error) error {
	var lastErr error

	for i := 0; i <= maxRetries; i++ {
		if i > 0 {
			e.logger.Debug().Str("task_id", taskID).Int("attempt", i).Msg("retrying task")
		}

		err := e.Execute(ctx, taskID, fn)
		if err == nil {
			return nil
		}

		lastErr = err

		// 指数退避
		if i < maxRetries {
			backoff := time.Duration(1<<uint(i)) * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	return fmt.Errorf("task failed after %d retries: %w", maxRetries, lastErr)
}

// ExecuteWithTimeout 执行任务并设置超时。
func (e *ParallelExecutor) ExecuteWithTimeout(ctx context.Context, taskID string, timeout time.Duration, fn func(context.Context) error) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	errCh := make(chan error, 1)

	go func() {
		errCh <- e.Execute(timeoutCtx, taskID, fn)
	}()

	select {
	case err := <-errCh:
		return err
	case <-timeoutCtx.Done():
		e.Cancel(taskID)
		return fmt.Errorf("task timeout after %v", timeout)
	}
}

// ExecutePipeline 管道执行（前一个任务的输出是后一个的输入）。
func (e *ParallelExecutor) ExecutePipeline(ctx context.Context, stages []PipelineStage) (any, error) {
	var result any

	for i, stage := range stages {
		taskID := fmt.Sprintf("pipeline-stage-%d", i)

		err := e.Execute(ctx, taskID, func(ctx context.Context) error {
			output, err := stage(ctx, result)
			if err != nil {
				return err
			}
			result = output
			return nil
		})

		if err != nil {
			return nil, fmt.Errorf("stage %d failed: %w", i, err)
		}
	}

	return result, nil
}

// PipelineStage 是管道阶段函数。
type PipelineStage func(context.Context, any) (any, error)
