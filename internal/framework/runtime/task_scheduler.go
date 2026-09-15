package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ─────────────────────────────────────────────
//  任务调度接口
// ─────────────────────────────────────────────

// ScheduleType 调度类型
type ScheduleType string

const (
	// ScheduleTypeOnce 单次执行
	ScheduleTypeOnce ScheduleType = "once"
	// ScheduleTypeInterval 固定间隔
	ScheduleTypeInterval ScheduleType = "interval"
	// ScheduleTypeCron Cron 表达式
	ScheduleTypeCron ScheduleType = "cron"
)

// TaskStatus 任务状态
type TaskStatus string

const (
	// TaskStatusPending 等待执行
	TaskStatusPending TaskStatus = "pending"
	// TaskStatusRunning 正在执行
	TaskStatusRunning TaskStatus = "running"
	// TaskStatusCompleted 已完成（单次任务）
	TaskStatusCompleted TaskStatus = "completed"
	// TaskStatusFailed 执行失败
	TaskStatusFailed TaskStatus = "failed"
	// TaskStatusCancelled 已取消
	TaskStatusCancelled TaskStatus = "cancelled"
)

// TaskFunc 任务执行函数
type TaskFunc func(ctx context.Context) error

// ScheduledTask 调度任务配置
type ScheduledTask struct {
	// 任务唯一标识
	ID string `json:"id"`

	// 任务名称
	Name string `json:"name"`

	// 调度类型
	Type ScheduleType `json:"type"`

	// 固定间隔（Type=Interval 时使用）
	Interval time.Duration `json:"interval,omitempty"`

	// Cron 表达式（Type=Cron 时使用）
	CronExpr string `json:"cron_expr,omitempty"`

	// 延迟启动时间（首次执行延迟）
	Delay time.Duration `json:"delay,omitempty"`

	// 最大执行次数（0 表示无限制）
	MaxExecutions int `json:"max_executions,omitempty"`

	// 执行超时时间（0 表示无超时）
	Timeout time.Duration `json:"timeout,omitempty"`

	// 失败重试次数
	MaxRetries int `json:"max_retries,omitempty"`

	// 重试间隔
	RetryInterval time.Duration `json:"retry_interval,omitempty"`

	// 任务执行函数
	Func TaskFunc `json:"-"`

	// 任务元数据
	Metadata map[string]string `json:"metadata,omitempty"`
}

// TaskExecution 任务执行记录
type TaskExecution struct {
	// 执行 ID
	ExecutionID string `json:"execution_id"`

	// 任务 ID
	TaskID string `json:"task_id"`

	// 开始时间
	StartTime time.Time `json:"start_time"`

	// 结束时间
	EndTime time.Time `json:"end_time"`

	// 执行状态
	Status TaskStatus `json:"status"`

	// 错误信息
	Error string `json:"error,omitempty"`

	// 重试次数
	RetryCount int `json:"retry_count"`

	// 执行序号（第几次执行）
	ExecutionNumber int `json:"execution_number"`
}

// TaskScheduler 任务调度器接口
type TaskScheduler interface {
	// Schedule 调度任务（返回任务 ID）
	Schedule(task *ScheduledTask) (string, error)

	// Cancel 取消任务
	Cancel(taskID string) error

	// Pause 暂停任务
	Pause(taskID string) error

	// Resume 恢复任务
	Resume(taskID string) error

	// GetTask 获取任务信息
	GetTask(taskID string) (*ScheduledTask, error)

	// ListTasks 列出所有任务
	ListTasks() []*ScheduledTask

	// GetExecutions 获取任务执行历史
	GetExecutions(taskID string, limit int) []*TaskExecution

	// Start 启动调度器
	Start(ctx context.Context) error

	// Stop 停止调度器
	Stop(ctx context.Context) error
}

// ─────────────────────────────────────────────
//  默认实现
// ─────────────────────────────────────────────

// DefaultTaskScheduler 基于 Timer 的任务调度器实现
type DefaultTaskScheduler struct {
	mu         sync.RWMutex
	tasks      map[string]*scheduledTaskState // taskID -> state
	executions map[string][]*TaskExecution    // taskID -> executions
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	started    bool
}

// scheduledTaskState 任务运行时状态
type scheduledTaskState struct {
	task           *ScheduledTask
	status         TaskStatus
	executionCount int           // 已执行次数
	timer          *time.Timer   // 下次执行的定时器
	cancel         context.CancelFunc // 取消当前执行
	paused         bool          // 是否暂停
	lastExecution  time.Time     // 上次执行时间
}

// NewTaskScheduler 创建任务调度器
func NewTaskScheduler() TaskScheduler {
	return &DefaultTaskScheduler{
		tasks:      make(map[string]*scheduledTaskState),
		executions: make(map[string][]*TaskExecution),
	}
}

// Schedule 调度任务
func (s *DefaultTaskScheduler) Schedule(task *ScheduledTask) (string, error) {
	if task.Func == nil {
		return "", fmt.Errorf("任务执行函数不能为空")
	}

	// 验证调度配置
	if err := s.validateTask(task); err != nil {
		return "", err
	}

	// 生成任务 ID
	if task.ID == "" {
		task.ID = uuid.New().String()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 检查任务是否已存在
	if _, exists := s.tasks[task.ID]; exists {
		return "", fmt.Errorf("任务 %s 已存在", task.ID)
	}

	// 创建任务状态
	state := &scheduledTaskState{
		task:   task,
		status: TaskStatusPending,
	}

	s.tasks[task.ID] = state

	// 如果调度器已启动，立即启动该任务
	if s.started {
		s.scheduleNext(state)
	}

	return task.ID, nil
}

// validateTask 验证任务配置
func (s *DefaultTaskScheduler) validateTask(task *ScheduledTask) error {
	switch task.Type {
	case ScheduleTypeOnce:
		// 单次任务无需额外验证
	case ScheduleTypeInterval:
		if task.Interval <= 0 {
			return fmt.Errorf("间隔时间必须大于 0")
		}
	case ScheduleTypeCron:
		if task.CronExpr == "" {
			return fmt.Errorf("Cron 表达式不能为空")
		}
		// TODO: 验证 Cron 表达式格式（可选，使用第三方库）
	default:
		return fmt.Errorf("不支持的调度类型: %s", task.Type)
	}

	return nil
}

// scheduleNext 调度下次执行
func (s *DefaultTaskScheduler) scheduleNext(state *scheduledTaskState) {
	if state.paused {
		return
	}

	// 检查是否达到最大执行次数
	if state.task.MaxExecutions > 0 && state.executionCount >= state.task.MaxExecutions {
		state.status = TaskStatusCompleted
		return
	}

	// 计算下次执行时间
	var nextDelay time.Duration
	if state.executionCount == 0 && state.task.Delay > 0 {
		// 首次执行延迟
		nextDelay = state.task.Delay
	} else {
		switch state.task.Type {
		case ScheduleTypeOnce:
			// 单次任务首次执行后不再调度
			if state.executionCount > 0 {
				return
			}
			nextDelay = 0
		case ScheduleTypeInterval:
			nextDelay = state.task.Interval
		case ScheduleTypeCron:
			// TODO: 解析 Cron 表达式计算下次执行时间
			// 暂时使用固定间隔作为 fallback
			nextDelay = 1 * time.Minute
		}
	}

	// 创建定时器
	state.timer = time.AfterFunc(nextDelay, func() {
		s.executeTask(state)
	})
}

// executeTask 执行任务
func (s *DefaultTaskScheduler) executeTask(state *scheduledTaskState) {
	s.mu.Lock()
	if state.paused {
		s.mu.Unlock()
		return
	}

	state.status = TaskStatusRunning
	state.executionCount++
	executionNumber := state.executionCount
	s.mu.Unlock()

	// 创建执行记录
	execution := &TaskExecution{
		ExecutionID:     uuid.New().String(),
		TaskID:          state.task.ID,
		StartTime:       time.Now(),
		Status:          TaskStatusRunning,
		ExecutionNumber: executionNumber,
	}

	// 执行任务（带超时和重试）
	err := s.executeWithRetry(state, execution)

	// 更新执行记录
	execution.EndTime = time.Now()
	if err != nil {
		execution.Status = TaskStatusFailed
		execution.Error = err.Error()
	} else {
		execution.Status = TaskStatusCompleted
	}

	// 保存执行记录
	s.mu.Lock()
	s.executions[state.task.ID] = append(s.executions[state.task.ID], execution)

	// 限制执行历史记录数量（最多保留 100 条）
	if len(s.executions[state.task.ID]) > 100 {
		s.executions[state.task.ID] = s.executions[state.task.ID][len(s.executions[state.task.ID])-100:]
	}

	state.lastExecution = time.Now()
	state.status = TaskStatusPending
	s.mu.Unlock()

	// 调度下次执行
	s.scheduleNext(state)
}

// executeWithRetry 执行任务（带重试）
func (s *DefaultTaskScheduler) executeWithRetry(state *scheduledTaskState, execution *TaskExecution) error {
	var lastErr error

	maxRetries := state.task.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			execution.RetryCount++
			// 重试前等待
			if state.task.RetryInterval > 0 {
				time.Sleep(state.task.RetryInterval)
			}
		}

		// 创建执行上下文
		execCtx := s.ctx
		if state.task.Timeout > 0 {
			var cancel context.CancelFunc
			execCtx, cancel = context.WithTimeout(s.ctx, state.task.Timeout)
			state.cancel = cancel
			defer cancel()
		}

		// 执行任务
		lastErr = state.task.Func(execCtx)
		if lastErr == nil {
			return nil
		}

		// 检查是否上下文取消
		if execCtx.Err() != nil {
			return execCtx.Err()
		}
	}

	return lastErr
}

// Cancel 取消任务
func (s *DefaultTaskScheduler) Cancel(taskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, exists := s.tasks[taskID]
	if !exists {
		return fmt.Errorf("任务 %s 不存在", taskID)
	}

	// 停止定时器
	if state.timer != nil {
		state.timer.Stop()
	}

	// 取消正在执行的任务
	if state.cancel != nil {
		state.cancel()
	}

	state.status = TaskStatusCancelled
	delete(s.tasks, taskID)

	return nil
}

// Pause 暂停任务
func (s *DefaultTaskScheduler) Pause(taskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, exists := s.tasks[taskID]
	if !exists {
		return fmt.Errorf("任务 %s 不存在", taskID)
	}

	state.paused = true

	// 停止定时器
	if state.timer != nil {
		state.timer.Stop()
	}

	return nil
}

// Resume 恢复任务
func (s *DefaultTaskScheduler) Resume(taskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, exists := s.tasks[taskID]
	if !exists {
		return fmt.Errorf("任务 %s 不存在", taskID)
	}

	if !state.paused {
		return nil
	}

	state.paused = false

	// 重新调度
	s.scheduleNext(state)

	return nil
}

// GetTask 获取任务信息
func (s *DefaultTaskScheduler) GetTask(taskID string) (*ScheduledTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	state, exists := s.tasks[taskID]
	if !exists {
		return nil, fmt.Errorf("任务 %s 不存在", taskID)
	}

	return state.task, nil
}

// ListTasks 列出所有任务
func (s *DefaultTaskScheduler) ListTasks() []*ScheduledTask {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tasks := make([]*ScheduledTask, 0, len(s.tasks))
	for _, state := range s.tasks {
		tasks = append(tasks, state.task)
	}

	return tasks
}

// GetExecutions 获取任务执行历史
func (s *DefaultTaskScheduler) GetExecutions(taskID string, limit int) []*TaskExecution {
	s.mu.RLock()
	defer s.mu.RUnlock()

	executions, exists := s.executions[taskID]
	if !exists {
		return nil
	}

	if limit <= 0 || limit > len(executions) {
		limit = len(executions)
	}

	// 返回最近的 N 条记录
	result := make([]*TaskExecution, limit)
	copy(result, executions[len(executions)-limit:])

	return result
}

// Start 启动调度器
func (s *DefaultTaskScheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return fmt.Errorf("调度器已启动")
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.started = true

	// 启动所有已注册的任务
	for _, state := range s.tasks {
		s.scheduleNext(state)
	}

	return nil
}

// Stop 停止调度器
func (s *DefaultTaskScheduler) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return nil
	}

	// 取消所有任务
	for _, state := range s.tasks {
		if state.timer != nil {
			state.timer.Stop()
		}
		if state.cancel != nil {
			state.cancel()
		}
	}

	s.cancel()
	s.started = false

	return nil
}
