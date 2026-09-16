package core_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGraphRunner_ReplaceOrchestrator 验证 Graph 能完整替代 Orchestrator
//
// 测试场景：模拟真实的任务编排流程
// 1. 初始化
// 2. 启动 Agents（Planner + Monitor）
// 3. 获取可执行 Actions
// 4. 执行 Actions（并行/串行）
// 5. 检查完成条件
// 6. 循环直到任务完成
func TestGraphRunner_ReplaceOrchestrator(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 模拟状态
	var (
		mu              sync.Mutex
		agentsStarted   bool
		actionsExecuted []string
		plannerRuns     int
		executorRuns    int
		iterationCount  int
	)

	// 构建 Graph
	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 100,
	}
	graph := core.NewGraph("initialize", config)

	// 节点 1: initialize
	graph.AddNode("initialize", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("task_id", "test-task-001")
		state.Set("iteration", 0)
		return state, nil
	})

	// 节点 2: start_agents
	graph.AddNode("start_agents", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		mu.Lock()
		defer mu.Unlock()

		if agentsStarted {
			return state, nil
		}

		// 模拟启动 Planner 和 Monitor
		agentsStarted = true
		state.Set("agents_started", true)

		return state, nil
	})

	// 节点 3: fetch_actions（模拟从 WorldModel 读取）
	graph.AddNode("fetch_actions", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		mu.Lock()
		defer mu.Unlock()

		iteration, _ := state.GetInt("iteration")
		iterationCount = iteration + 1
		state.Set("iteration", iterationCount)

		plannerRuns++

		// 模拟 Planner 生成 Actions
		var actions []string
		if len(actionsExecuted) < 5 {
			// 还有工作要做
			actions = []string{
				"action-1",
				"action-2",
			}
		}

		state.Set("actions", actions)
		state.Set("action_count", len(actions))

		return state, nil
	})

	// 节点 4: execute_actions
	graph.AddNode("execute_actions", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		mu.Lock()
		defer mu.Unlock()

		actionsRaw, _ := state.Get("actions")
		actions, ok := actionsRaw.([]string)
		if !ok || len(actions) == 0 {
			return state, nil
		}

		executorRuns++

		// 模拟执行 Actions
		for _, action := range actions {
			actionsExecuted = append(actionsExecuted, action)
		}

		state.Set("executed", len(actions))

		return state, nil
	})

	// 节点 5: check_completion
	graph.AddNode("check_completion", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		mu.Lock()
		defer mu.Unlock()

		completed := len(actionsExecuted) >= 5
		state.Set("task_completed", completed)

		return state, nil
	})

	// 边：initialize → start_agents
	graph.AddEdge("initialize", "start_agents")

	// 边：start_agents → fetch_actions
	graph.AddEdge("start_agents", "fetch_actions")

	// 条件边：fetch_actions → execute_actions 或 check_completion
	graph.AddConditionalEdge("fetch_actions", func(ctx context.Context, state core.GraphState) (string, error) {
		actionCount, _ := state.GetInt("action_count")
		if actionCount > 0 {
			return "execute", nil
		}
		return "check", nil
	}, map[string]string{
		"execute": "execute_actions",
		"check":   "check_completion",
	})

	// 边：execute_actions → check_completion
	graph.AddEdge("execute_actions", "check_completion")

	// 条件边：check_completion → fetch_actions（继续）或 END（完成）
	graph.AddConditionalEdge("check_completion", func(ctx context.Context, state core.GraphState) (string, error) {
		completed, _ := state.GetBool("task_completed")
		if completed {
			return "end", nil
		}
		return "continue", nil
	}, map[string]string{
		"continue": "fetch_actions",
		"end":      core.END,
	})

	// 编译并运行
	err := graph.Compile()
	require.NoError(t, err, "Graph 编译应该成功")

	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err, "Graph 执行应该成功")

	// 验证结果
	taskCompleted, _ := finalState.GetBool("task_completed")
	assert.True(t, taskCompleted, "任务应该完成")

	assert.True(t, agentsStarted, "Agents 应该已启动")
	assert.GreaterOrEqual(t, len(actionsExecuted), 5, "应该执行至少 5 个 Actions")
	assert.GreaterOrEqual(t, plannerRuns, 3, "Planner 应该运行至少 3 次")
	assert.GreaterOrEqual(t, executorRuns, 2, "Executor 应该运行至少 2 次")
	assert.GreaterOrEqual(t, iterationCount, 3, "应该迭代至少 3 次")

	t.Logf("✅ Graph 成功替代 Orchestrator")
	t.Logf("   - Actions 执行: %d 个", len(actionsExecuted))
	t.Logf("   - Planner 运行: %d 次", plannerRuns)
	t.Logf("   - Executor 运行: %d 次", executorRuns)
	t.Logf("   - 总迭代次数: %d 次", iterationCount)
}

// TestGraphRunner_ConcurrentActions 验证并行执行 Actions
func TestGraphRunner_ConcurrentActions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var (
		mu              sync.Mutex
		actionsExecuted []string
		executionTimes  = make(map[string]time.Time)
	)

	// 构建 Graph（并发模式）
	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeConcurrent,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	// 节点：start
	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("actions", []string{"action-1", "action-2", "action-3"})
		return state, nil
	})

	// 节点：并行执行 3 个 Actions
	for i := 1; i <= 3; i++ {
		actionName := "action-" + string(rune('0'+i))
		graph.AddNode(actionName, func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			mu.Lock()
			actionsExecuted = append(actionsExecuted, actionName)
			executionTimes[actionName] = time.Now()
			mu.Unlock()

			// 模拟执行时间
			time.Sleep(10 * time.Millisecond)

			state.Set(actionName+"_done", true)
			return state, nil
		})

		// 边：start → action-N
		graph.AddEdge("start", actionName)
	}

	// 汇聚节点：check_results
	graph.AddNode("check_results", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		mu.Lock()
		defer mu.Unlock()

		state.Set("all_done", len(actionsExecuted) == 3)
		return state, nil
	})

	// 边：所有 actions → check_results
	for i := 1; i <= 3; i++ {
		actionName := "action-" + string(rune('0'+i))
		graph.AddEdge(actionName, "check_results")
	}

	// 编译并运行
	err := graph.Compile()
	require.NoError(t, err)

	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证结果
	allDone, _ := finalState.GetBool("all_done")
	assert.True(t, allDone, "所有 Actions 应该完成")
	assert.Equal(t, 3, len(actionsExecuted), "应该执行 3 个 Actions")

	// 验证并行执行（时间戳应该接近）
	times := make([]time.Time, 0, 3)
	for _, t := range executionTimes {
		times = append(times, t)
	}

	if len(times) == 3 {
		maxDiff := times[0].Sub(times[1])
		if maxDiff < 0 {
			maxDiff = -maxDiff
		}
		for i := 1; i < len(times); i++ {
			diff := times[i].Sub(times[i-1])
			if diff < 0 {
				diff = -diff
			}
			if diff > maxDiff {
				maxDiff = diff
			}
		}

		// 并行执行时，时间差应该小于 5ms
		assert.Less(t, maxDiff, 5*time.Millisecond, "Actions 应该并行执行")
	}

	t.Logf("✅ 并行执行验证通过")
	t.Logf("   - 执行的 Actions: %v", actionsExecuted)
}

// TestGraphRunner_ErrorHandling 验证错误处理
func TestGraphRunner_ErrorHandling(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 5,
	}
	graph := core.NewGraph("start", config)

	// 节点：start
	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("step", 1)
		return state, nil
	})

	// 节点：error（模拟错误）
	graph.AddNode("error", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		return state, assert.AnError
	})

	// 边：start → error
	graph.AddEdge("start", "error")

	// 编译并运行
	err := graph.Compile()
	require.NoError(t, err)

	input := core.NewGraphState()
	_, err = graph.Run(ctx, *input)

	// 验证错误传播
	assert.Error(t, err, "应该返回错误")

	t.Logf("✅ 错误处理验证通过")
}

// TestGraphRunner_ContextCancellation 验证上下文取消
func TestGraphRunner_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 100,
	}
	graph := core.NewGraph("start", config)

	// 节点：start
	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("count", 0)
		return state, nil
	})

	// 节点：loop（无限循环）
	graph.AddNode("loop", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		// 检查上下文取消
		select {
		case <-ctx.Done():
			return state, ctx.Err()
		default:
		}

		count, _ := state.GetInt("count")
		state.Set("count", count+1)

		time.Sleep(50 * time.Millisecond)
		return state, nil
	})

	// 边：start → loop
	graph.AddEdge("start", "loop")

	// 条件边：loop → loop（条件循环，允许）
	graph.AddConditionalEdge("loop", func(ctx context.Context, state core.GraphState) (string, error) {
		// 无限循环，直到上下文取消
		return "continue", nil
	}, map[string]string{
		"continue": "loop",
	})

	// 编译并运行
	err := graph.Compile()
	require.NoError(t, err)

	input := core.NewGraphState()
	_, err = graph.Run(ctx, *input)

	// 验证上下文取消
	assert.Error(t, err, "应该因上下文取消而返回错误")
	assert.ErrorIs(t, err, context.DeadlineExceeded, "应该是 DeadlineExceeded 错误")

	t.Logf("✅ 上下文取消验证通过")
}
