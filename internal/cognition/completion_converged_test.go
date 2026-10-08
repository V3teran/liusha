package cognition

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
)

// TestCompletionDetector_TaskConverged 验证 P0 修复：
// Planner 返回空列表 → 发布 EventTaskConverged → CompletionDetector 停止任务
func TestCompletionDetector_TaskConverged(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New(ctx)
	graph := explorationgraph.NewMemoryStore()

	detector := NewCompletionDetector(Config{
		TaskID:        "test-task",
		Graph:         graph,
		Bus:           eventBus,
		Logger:        zerolog.Nop(),
		CheckInterval: 100 * time.Millisecond,
	})

	// 启动检测器（后台）
	resultCh := make(chan Result, 1)
	go func() {
		result := detector.Start(ctx)
		resultCh <- result
	}()

	// 等待检测器启动
	time.Sleep(50 * time.Millisecond)

	// 模拟 Planner 发布收敛事件
	eventBus.PublishTaskConverged("test-task", "planner: no more actions to generate")

	// 等待检测器响应
	select {
	case result := <-resultCh:
		assert.Equal(t, "task_converged", result.StopWhy, "应该因任务收敛而停止")
		t.Logf("✅ 任务收敛测试通过：%+v", result)
	case <-time.After(2 * time.Second):
		t.Fatal("超时：检测器未响应收敛事件")
	}
}

// TestCompletionDetector_ConvergedBeforeMaxSteps 验证收敛优先级高于 max_steps
func TestCompletionDetector_ConvergedBeforeMaxSteps(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New(ctx)
	graph := explorationgraph.NewMemoryStore()

	detector := NewCompletionDetector(Config{
		TaskID:        "test-task",
		Graph:         graph,
		Bus:           eventBus,
		Logger:        zerolog.Nop(),
		MaxSteps:      100, // 设置最大步数
		CheckInterval: 100 * time.Millisecond,
	})

	resultCh := make(chan Result, 1)
	go func() {
		result := detector.Start(ctx)
		resultCh <- result
	}()

	time.Sleep(50 * time.Millisecond)

	// 只完成 5 步
	for i := 0; i < 5; i++ {
		eventBus.PublishActionCompleted("test-task", "action-"+string(rune(i)))
	}

	// 发布收敛事件（远未达到 100 步）
	eventBus.PublishTaskConverged("test-task", "planner: no more actions")

	select {
	case result := <-resultCh:
		assert.Equal(t, "task_converged", result.StopWhy, "应该因收敛停止，而非 max_steps")
		assert.Equal(t, 5, result.Steps, "应该记录 5 步")
		t.Logf("✅ 收敛优先级测试通过：steps=%d, stop_why=%s", result.Steps, result.StopWhy)
	case <-time.After(2 * time.Second):
		t.Fatal("超时")
	}
}

// TestCompletionDetector_NormalFlowWithoutConvergence 验证未收敛时不会提前停止
func TestCompletionDetector_NormalFlowWithoutConvergence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	eventBus := bus.New(context.Background())
	graph := explorationgraph.NewMemoryStore()

	detector := NewCompletionDetector(Config{
		TaskID:        "test-task",
		Graph:         graph,
		Bus:           eventBus,
		Logger:        zerolog.Nop(),
		CheckInterval: 100 * time.Millisecond,
	})

	resultCh := make(chan Result, 1)
	go func() {
		result := detector.Start(ctx)
		resultCh <- result
	}()

	// 持续产生 action
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	actionCount := 0
	for {
		select {
		case <-ticker.C:
			eventBus.PublishActionCompleted("test-task", "action-ongoing")
			actionCount++
		case result := <-resultCh:
			// 应该是 context 超时，而非收敛
			assert.Equal(t, "context_cancelled", result.StopWhy, "无收敛事件时应持续运行直到 context 取消")
			assert.Greater(t, result.Steps, 3, "应该执行了多步")
			t.Logf("✅ 持续运行测试通过：steps=%d, stop_why=%s", result.Steps, result.StopWhy)
			return
		}
	}
}
