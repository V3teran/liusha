package cognition

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
)

// TestCompletionDetector_TaskConvergedE2E 端到端测试：
// 模拟 Planner 发布 EventTaskConverged，验证 CompletionDetector 立即停止
func TestCompletionDetector_TaskConvergedE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventBus := bus.New(ctx)
	graph := explorationgraph.NewMemoryStore()
	taskID := "test-task"

	// 创建 CompletionDetector（max_steps=100，防止提前结束）
	detector := NewCompletionDetector(Config{
		TaskID:        taskID,
		Graph:         graph,
		Bus:           eventBus,
		Logger:        zerolog.New(zerolog.NewTestWriter(t)),
		MaxSteps:      100,
		CheckInterval: 100 * time.Millisecond,
	})

	// 在后台启动 detector
	resultChan := make(chan Result)
	go func() {
		result := detector.Start(ctx)
		resultChan <- result
	}()

	// 等待 detector 启动
	time.Sleep(200 * time.Millisecond)

	// 模拟一些 action 完成
	eventBus.PublishActionCompleted(taskID, "action-1")
	eventBus.PublishActionCompleted(taskID, "action-2")
	eventBus.PublishActionCompleted(taskID, "action-3")

	// 等待事件处理
	time.Sleep(150 * time.Millisecond)

	// 模拟 Planner 发布 EventTaskConverged
	eventBus.PublishTaskConverged(taskID, "planner: no more actions to generate")

	// 验证 detector 立即停止（不等待 max_steps）
	select {
	case result := <-resultChan:
		assert.Equal(t, "task_converged", result.StopWhy, "应该以 task_converged 停止")
		assert.Equal(t, 3, result.Steps, "应该记录 3 步")
		t.Log("✅ CompletionDetector 收到 EventTaskConverged 后立即停止")
	case <-time.After(2 * time.Second):
		t.Fatal("超时：CompletionDetector 未在 2 秒内停止")
	}
}

// TestCompletionDetector_MaxStepsStillWorksIntegration 验证 max_steps 仍然生效
func TestCompletionDetector_MaxStepsStillWorksIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventBus := bus.New(ctx)
	graph := explorationgraph.NewMemoryStore()
	taskID := "test-task"

	// 创建 CompletionDetector（max_steps=3）
	detector := NewCompletionDetector(Config{
		TaskID:        taskID,
		Graph:         graph,
		Bus:           eventBus,
		Logger:        zerolog.New(zerolog.NewTestWriter(t)),
		MaxSteps:      3,
		CheckInterval: 50 * time.Millisecond,
	})

	// 在后台启动
	resultChan := make(chan Result)
	go func() {
		result := detector.Start(ctx)
		resultChan <- result
	}()

	// 等待启动
	time.Sleep(100 * time.Millisecond)

	// 发布 3 个 action 完成事件
	eventBus.PublishActionCompleted(taskID, "action-1")
	eventBus.PublishActionCompleted(taskID, "action-2")
	eventBus.PublishActionCompleted(taskID, "action-3")

	// 验证停止
	select {
	case result := <-resultChan:
		assert.Equal(t, "max_steps_reached", result.StopWhy)
		assert.Equal(t, 3, result.Steps)
		t.Log("✅ max_steps 机制不受 P0 修复影响")
	case <-time.After(2 * time.Second):
		t.Fatal("超时：未达到 max_steps")
	}
}

// TestCompletionDetector_ConvergedBeforeMaxStepsE2E 验证收敛优先级高于 max_steps
func TestCompletionDetector_ConvergedBeforeMaxStepsE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventBus := bus.New(ctx)
	graph := explorationgraph.NewMemoryStore()
	taskID := "test-task"

	// 创建 CompletionDetector（max_steps=100）
	detector := NewCompletionDetector(Config{
		TaskID:        taskID,
		Graph:         graph,
		Bus:           eventBus,
		Logger:        zerolog.New(zerolog.NewTestWriter(t)),
		MaxSteps:      100,
		CheckInterval: 100 * time.Millisecond,
	})

	// 在后台启动
	resultChan := make(chan Result)
	go func() {
		result := detector.Start(ctx)
		resultChan <- result
	}()

	// 等待启动
	time.Sleep(100 * time.Millisecond)

	// 完成 10 个 action
	for i := 0; i < 10; i++ {
		eventBus.PublishActionCompleted(taskID, "action")
	}

	// 等待处理
	time.Sleep(150 * time.Millisecond)

	// 发布收敛事件（远未达到 max_steps=100）
	eventBus.PublishTaskConverged(taskID, "planner: no more actions")

	// 验证以 task_converged 停止（不是 max_steps）
	select {
	case result := <-resultChan:
		assert.Equal(t, "task_converged", result.StopWhy, "收敛应优先于 max_steps")
		assert.Equal(t, 10, result.Steps, "应该记录 10 步")
		t.Log("✅ 任务收敛优先级高于 max_steps")
	case <-time.After(2 * time.Second):
		t.Fatal("超时")
	}
}

// TestCompletionDetector_MultipleConvergedEventsE2E 验证多次收敛事件（幂等性）
func TestCompletionDetector_MultipleConvergedEventsE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	eventBus := bus.New(ctx)
	graph := explorationgraph.NewMemoryStore()
	taskID := "test-task"

	detector := NewCompletionDetector(Config{
		TaskID:        taskID,
		Graph:         graph,
		Bus:           eventBus,
		Logger:        zerolog.New(zerolog.NewTestWriter(t)),
		MaxSteps:      100,
		CheckInterval: 100 * time.Millisecond,
	})

	// 在后台启动
	resultChan := make(chan Result)
	go func() {
		result := detector.Start(ctx)
		resultChan <- result
	}()

	// 等待启动
	time.Sleep(100 * time.Millisecond)

	// 发布多次收敛事件（模拟 Planner 多次返回空列表）
	eventBus.PublishTaskConverged(taskID, "reason 1")
	time.Sleep(50 * time.Millisecond)
	eventBus.PublishTaskConverged(taskID, "reason 2")
	time.Sleep(50 * time.Millisecond)
	eventBus.PublishTaskConverged(taskID, "reason 3")

	// 验证只停止一次
	select {
	case result := <-resultChan:
		assert.Equal(t, "task_converged", result.StopWhy)
		t.Log("✅ 多次收敛事件不会重复处理（幂等）")
	case <-time.After(2 * time.Second):
		t.Fatal("超时")
	}
}

// TestCompletionDetector_OtherTaskEventIgnoredE2E 验证其他任务的事件被忽略
func TestCompletionDetector_OtherTaskEventIgnoredE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	eventBus := bus.New(ctx)
	graph := explorationgraph.NewMemoryStore()
	taskID := "test-task"

	detector := NewCompletionDetector(Config{
		TaskID:        taskID,
		Graph:         graph,
		Bus:           eventBus,
		Logger:        zerolog.New(zerolog.NewTestWriter(t)),
		MaxSteps:      0, // 无限制
		CheckInterval: 100 * time.Millisecond,
	})

	// 在后台启动
	resultChan := make(chan Result)
	go func() {
		result := detector.Start(ctx)
		resultChan <- result
	}()

	// 等待启动
	time.Sleep(100 * time.Millisecond)

	// 发布其他任务的收敛事件
	eventBus.PublishTaskConverged("other-task", "other task converged")

	// 等待一段时间，验证未停止
	select {
	case result := <-resultChan:
		t.Fatalf("不应该停止，但收到结果: %+v", result)
	case <-time.After(500 * time.Millisecond):
		t.Log("✅ 其他任务的收敛事件被正确忽略")
		cancel() // 手动停止 detector
	}
}

// TestCompletionDetector_AbortStillWorksE2E 验证 Abort 仍然生效
func TestCompletionDetector_AbortStillWorksE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	eventBus := bus.New(ctx)
	graph := explorationgraph.NewMemoryStore()
	taskID := "test-task"

	detector := NewCompletionDetector(Config{
		TaskID:        taskID,
		Graph:         graph,
		Bus:           eventBus,
		Logger:        zerolog.New(zerolog.NewTestWriter(t)),
		MaxSteps:      100,
		CheckInterval: 100 * time.Millisecond,
	})

	// 在后台启动
	resultChan := make(chan Result)
	go func() {
		result := detector.Start(ctx)
		resultChan <- result
	}()

	// 等待启动
	time.Sleep(100 * time.Millisecond)

	// 调用 Abort
	detector.Abort("user requested termination")

	// 验证立即停止
	select {
	case result := <-resultChan:
		assert.Equal(t, "user requested termination", result.StopWhy)
		t.Log("✅ Abort 机制不受 P0 修复影响")
	case <-time.After(1 * time.Second):
		t.Fatal("超时：Abort 未生效")
	}
}
