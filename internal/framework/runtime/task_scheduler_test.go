package runtime

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────
//  测试用例
// ─────────────────────────────────────────────

// TestTaskScheduler_OnceTask 测试单次任务
func TestTaskScheduler_OnceTask(t *testing.T) {
	scheduler := NewTaskScheduler()
	ctx := context.Background()

	// 启动调度器
	err := scheduler.Start(ctx)
	require.NoError(t, err)
	defer scheduler.Stop(ctx)

	// 记录执行次数
	var executeCount int32

	task := &ScheduledTask{
		Name: "once-task",
		Type: ScheduleTypeOnce,
		Func: func(ctx context.Context) error {
			atomic.AddInt32(&executeCount, 1)
			return nil
		},
	}

	// 调度任务
	taskID, err := scheduler.Schedule(task)
	require.NoError(t, err)
	assert.NotEmpty(t, taskID)

	// 等待任务执行
	time.Sleep(200 * time.Millisecond)

	// 验证只执行一次
	count := atomic.LoadInt32(&executeCount)
	assert.Equal(t, int32(1), count)

	// 获取任务信息
	taskInfo, err := scheduler.GetTask(taskID)
	require.NoError(t, err)
	assert.Equal(t, "once-task", taskInfo.Name)
}

// TestTaskScheduler_IntervalTask 测试间隔任务
func TestTaskScheduler_IntervalTask(t *testing.T) {
	scheduler := NewTaskScheduler()
	ctx := context.Background()

	err := scheduler.Start(ctx)
	require.NoError(t, err)
	defer scheduler.Stop(ctx)

	var executeCount int32

	task := &ScheduledTask{
		Name:     "interval-task",
		Type:     ScheduleTypeInterval,
		Interval: 100 * time.Millisecond,
		Func: func(ctx context.Context) error {
			atomic.AddInt32(&executeCount, 1)
			return nil
		},
	}

	taskID, err := scheduler.Schedule(task)
	require.NoError(t, err)

	// 等待执行多次
	time.Sleep(350 * time.Millisecond)

	count := atomic.LoadInt32(&executeCount)
	assert.GreaterOrEqual(t, count, int32(3)) // 至少执行 3 次

	// 取消任务
	err = scheduler.Cancel(taskID)
	require.NoError(t, err)

	// 等待确认不再执行
	beforeCancel := count
	time.Sleep(200 * time.Millisecond)
	afterCancel := atomic.LoadInt32(&executeCount)
	assert.Equal(t, beforeCancel, afterCancel)
}

// TestTaskScheduler_MaxExecutions 测试最大执行次数
func TestTaskScheduler_MaxExecutions(t *testing.T) {
	scheduler := NewTaskScheduler()
	ctx := context.Background()

	err := scheduler.Start(ctx)
	require.NoError(t, err)
	defer scheduler.Stop(ctx)

	var executeCount int32

	task := &ScheduledTask{
		Name:          "limited-task",
		Type:          ScheduleTypeInterval,
		Interval:      50 * time.Millisecond,
		MaxExecutions: 3, // 只执行 3 次
		Func: func(ctx context.Context) error {
			atomic.AddInt32(&executeCount, 1)
			return nil
		},
	}

	taskID, err := scheduler.Schedule(task)
	require.NoError(t, err)

	// 等待所有执行完成
	time.Sleep(300 * time.Millisecond)

	// 验证执行次数
	count := atomic.LoadInt32(&executeCount)
	assert.Equal(t, int32(3), count)

	// 获取执行历史
	executions := scheduler.GetExecutions(taskID, 10)
	assert.Len(t, executions, 3)
	assert.Equal(t, 1, executions[0].ExecutionNumber)
	assert.Equal(t, 2, executions[1].ExecutionNumber)
	assert.Equal(t, 3, executions[2].ExecutionNumber)
}

// TestTaskScheduler_DelayedStart 测试延迟启动
func TestTaskScheduler_DelayedStart(t *testing.T) {
	scheduler := NewTaskScheduler()
	ctx := context.Background()

	err := scheduler.Start(ctx)
	require.NoError(t, err)
	defer scheduler.Stop(ctx)

	var executeCount int32
	startTime := time.Now()

	task := &ScheduledTask{
		Name:  "delayed-task",
		Type:  ScheduleTypeOnce,
		Delay: 200 * time.Millisecond, // 延迟 200ms
		Func: func(ctx context.Context) error {
			atomic.AddInt32(&executeCount, 1)
			return nil
		},
	}

	_, err = scheduler.Schedule(task)
	require.NoError(t, err)

	// 立即检查不应执行
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(0), atomic.LoadInt32(&executeCount))

	// 等待延迟后执行
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, int32(1), atomic.LoadInt32(&executeCount))

	// 验证延迟时间
	elapsed := time.Since(startTime)
	assert.GreaterOrEqual(t, elapsed, 200*time.Millisecond)
}

// TestTaskScheduler_Timeout 测试任务超时
func TestTaskScheduler_Timeout(t *testing.T) {
	scheduler := NewTaskScheduler()
	ctx := context.Background()

	err := scheduler.Start(ctx)
	require.NoError(t, err)
	defer scheduler.Stop(ctx)

	task := &ScheduledTask{
		Name:    "timeout-task",
		Type:    ScheduleTypeOnce,
		Timeout: 100 * time.Millisecond, // 100ms 超时
		Func: func(ctx context.Context) error {
			// 模拟耗时操作
			select {
			case <-time.After(500 * time.Millisecond):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}

	taskID, err := scheduler.Schedule(task)
	require.NoError(t, err)

	// 等待任务执行
	time.Sleep(300 * time.Millisecond)

	// 获取执行历史
	executions := scheduler.GetExecutions(taskID, 1)
	require.Len(t, executions, 1)

	// 验证任务超时失败
	assert.Equal(t, TaskStatusFailed, executions[0].Status)
	assert.Contains(t, executions[0].Error, "context deadline exceeded")
}

// TestTaskScheduler_Retry 测试任务重试
func TestTaskScheduler_Retry(t *testing.T) {
	scheduler := NewTaskScheduler()
	ctx := context.Background()

	err := scheduler.Start(ctx)
	require.NoError(t, err)
	defer scheduler.Stop(ctx)

	var attemptCount int32

	task := &ScheduledTask{
		Name:          "retry-task",
		Type:          ScheduleTypeOnce,
		MaxRetries:    3,                     // 最多重试 3 次
		RetryInterval: 50 * time.Millisecond, // 重试间隔 50ms
		Func: func(ctx context.Context) error {
			count := atomic.AddInt32(&attemptCount, 1)
			if count < 3 {
				return fmt.Errorf("模拟失败（尝试 %d）", count)
			}
			return nil // 第 3 次成功
		},
	}

	taskID, err := scheduler.Schedule(task)
	require.NoError(t, err)

	// 等待所有重试完成
	time.Sleep(300 * time.Millisecond)

	// 验证尝试次数
	assert.Equal(t, int32(3), atomic.LoadInt32(&attemptCount))

	// 获取执行历史
	executions := scheduler.GetExecutions(taskID, 1)
	require.Len(t, executions, 1)

	// 最终成功
	assert.Equal(t, TaskStatusCompleted, executions[0].Status)
	assert.Equal(t, 2, executions[0].RetryCount) // 初始 1 次 + 重试 2 次
}

// TestTaskScheduler_RetryFailure 测试重试全部失败
func TestTaskScheduler_RetryFailure(t *testing.T) {
	scheduler := NewTaskScheduler()
	ctx := context.Background()

	err := scheduler.Start(ctx)
	require.NoError(t, err)
	defer scheduler.Stop(ctx)

	var attemptCount int32

	task := &ScheduledTask{
		Name:          "always-fail-task",
		Type:          ScheduleTypeOnce,
		MaxRetries:    2,
		RetryInterval: 50 * time.Millisecond,
		Func: func(ctx context.Context) error {
			atomic.AddInt32(&attemptCount, 1)
			return fmt.Errorf("永远失败")
		},
	}

	taskID, err := scheduler.Schedule(task)
	require.NoError(t, err)

	// 等待所有重试完成
	time.Sleep(300 * time.Millisecond)

	// 验证尝试次数：初始 1 次 + 重试 2 次 = 3 次
	assert.Equal(t, int32(3), atomic.LoadInt32(&attemptCount))

	// 获取执行历史
	executions := scheduler.GetExecutions(taskID, 1)
	require.Len(t, executions, 1)

	// 最终失败
	assert.Equal(t, TaskStatusFailed, executions[0].Status)
	assert.Contains(t, executions[0].Error, "永远失败")
	assert.Equal(t, 2, executions[0].RetryCount)
}

// TestTaskScheduler_PauseResume 测试暂停和恢复
func TestTaskScheduler_PauseResume(t *testing.T) {
	scheduler := NewTaskScheduler()
	ctx := context.Background()

	err := scheduler.Start(ctx)
	require.NoError(t, err)
	defer scheduler.Stop(ctx)

	var executeCount int32

	task := &ScheduledTask{
		Name:     "pause-resume-task",
		Type:     ScheduleTypeInterval,
		Interval: 100 * time.Millisecond,
		Func: func(ctx context.Context) error {
			atomic.AddInt32(&executeCount, 1)
			return nil
		},
	}

	taskID, err := scheduler.Schedule(task)
	require.NoError(t, err)

	// 等待执行几次
	time.Sleep(250 * time.Millisecond)
	countBeforePause := atomic.LoadInt32(&executeCount)
	assert.GreaterOrEqual(t, countBeforePause, int32(2))

	// 暂停任务
	err = scheduler.Pause(taskID)
	require.NoError(t, err)

	// 等待确认不再执行
	time.Sleep(250 * time.Millisecond)
	countAfterPause := atomic.LoadInt32(&executeCount)
	assert.Equal(t, countBeforePause, countAfterPause)

	// 恢复任务
	err = scheduler.Resume(taskID)
	require.NoError(t, err)

	// 等待确认继续执行
	time.Sleep(250 * time.Millisecond)
	countAfterResume := atomic.LoadInt32(&executeCount)
	assert.Greater(t, countAfterResume, countAfterPause)
}

// TestTaskScheduler_ListTasks 测试列出任务
func TestTaskScheduler_ListTasks(t *testing.T) {
	scheduler := NewTaskScheduler()
	ctx := context.Background()

	err := scheduler.Start(ctx)
	require.NoError(t, err)
	defer scheduler.Stop(ctx)

	// 调度多个任务
	task1 := &ScheduledTask{
		Name: "task1",
		Type: ScheduleTypeOnce,
		Func: func(ctx context.Context) error { return nil },
	}

	task2 := &ScheduledTask{
		Name:     "task2",
		Type:     ScheduleTypeInterval,
		Interval: 1 * time.Second,
		Func:     func(ctx context.Context) error { return nil },
	}

	_, err = scheduler.Schedule(task1)
	require.NoError(t, err)

	_, err = scheduler.Schedule(task2)
	require.NoError(t, err)

	// 列出所有任务
	tasks := scheduler.ListTasks()
	assert.Len(t, tasks, 2)

	names := make([]string, len(tasks))
	for i, t := range tasks {
		names[i] = t.Name
	}
	assert.Contains(t, names, "task1")
	assert.Contains(t, names, "task2")
}

// TestTaskScheduler_ConcurrentExecution 测试并发执行
func TestTaskScheduler_ConcurrentExecution(t *testing.T) {
	scheduler := NewTaskScheduler()
	ctx := context.Background()

	err := scheduler.Start(ctx)
	require.NoError(t, err)
	defer scheduler.Stop(ctx)

	var wg sync.WaitGroup
	executionOrder := make(chan int, 5)

	// 调度 5 个任务，同时执行
	for i := 1; i <= 5; i++ {
		wg.Add(1)
		taskNum := i

		task := &ScheduledTask{
			Name: fmt.Sprintf("concurrent-task-%d", taskNum),
			Type: ScheduleTypeOnce,
			Func: func(ctx context.Context) error {
				defer wg.Done()
				executionOrder <- taskNum
				time.Sleep(50 * time.Millisecond)
				return nil
			},
		}

		_, err := scheduler.Schedule(task)
		require.NoError(t, err)
	}

	// 等待所有任务完成
	wg.Wait()
	close(executionOrder)

	// 验证所有任务都执行了
	executedTasks := make([]int, 0, 5)
	for taskNum := range executionOrder {
		executedTasks = append(executedTasks, taskNum)
	}
	assert.Len(t, executedTasks, 5)
}

// TestTaskScheduler_StartBeforeSchedule 测试先启动后调度
func TestTaskScheduler_StartBeforeSchedule(t *testing.T) {
	scheduler := NewTaskScheduler()
	ctx := context.Background()

	// 先启动调度器
	err := scheduler.Start(ctx)
	require.NoError(t, err)
	defer scheduler.Stop(ctx)

	var executed bool

	task := &ScheduledTask{
		Name: "after-start-task",
		Type: ScheduleTypeOnce,
		Func: func(ctx context.Context) error {
			executed = true
			return nil
		},
	}

	// 后调度任务
	_, err = scheduler.Schedule(task)
	require.NoError(t, err)

	// 等待执行
	time.Sleep(100 * time.Millisecond)
	assert.True(t, executed)
}

// TestTaskScheduler_ScheduleBeforeStart 测试先调度后启动
func TestTaskScheduler_ScheduleBeforeStart(t *testing.T) {
	scheduler := NewTaskScheduler()
	ctx := context.Background()

	var executed bool

	task := &ScheduledTask{
		Name: "before-start-task",
		Type: ScheduleTypeOnce,
		Func: func(ctx context.Context) error {
			executed = true
			return nil
		},
	}

	// 先调度任务
	_, err := scheduler.Schedule(task)
	require.NoError(t, err)

	// 任务不应执行
	time.Sleep(100 * time.Millisecond)
	assert.False(t, executed)

	// 启动调度器
	err = scheduler.Start(ctx)
	require.NoError(t, err)
	defer scheduler.Stop(ctx)

	// 等待执行
	time.Sleep(100 * time.Millisecond)
	assert.True(t, executed)
}

// TestTaskScheduler_StopCancelsRunning 测试停止调度器取消正在执行的任务
func TestTaskScheduler_StopCancelsRunning(t *testing.T) {
	scheduler := NewTaskScheduler()
	ctx := context.Background()

	err := scheduler.Start(ctx)
	require.NoError(t, err)

	taskStarted := make(chan struct{})
	taskCancelled := make(chan struct{})

	task := &ScheduledTask{
		Name: "long-running-task",
		Type: ScheduleTypeOnce,
		Func: func(ctx context.Context) error {
			close(taskStarted)
			select {
			case <-time.After(5 * time.Second):
				return nil
			case <-ctx.Done():
				close(taskCancelled)
				return ctx.Err()
			}
		},
	}

	_, err = scheduler.Schedule(task)
	require.NoError(t, err)

	// 等待任务启动
	<-taskStarted

	// 停止调度器
	err = scheduler.Stop(ctx)
	require.NoError(t, err)

	// 验证任务被取消
	select {
	case <-taskCancelled:
		// 任务成功取消
	case <-time.After(1 * time.Second):
		t.Fatal("任务未被取消")
	}
}
