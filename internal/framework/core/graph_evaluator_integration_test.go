package core_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGraphRunner_WithEvaluator 验证 Graph 包含 Evaluator 的完整流程
//
// 测试场景：Planner → Executor → Evaluator 完整链路
func TestGraphRunner_WithEvaluator(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 模拟状态
	var (
		mu                sync.Mutex
		plannerRuns       int
		executorRuns      int
		evaluatorRuns     int
		hypothesesCreated []string
		findingsCreated   []string
	)

	// 构建 Graph
	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 100,
	}
	graph := core.NewGraph("initialize", config)

	// 节点 1: initialize
	graph.AddNode("initialize", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("task_id", "test-task-with-evaluator")
		return state, nil
	})

	// 节点 2: planner
	graph.AddNode("planner", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		mu.Lock()
		defer mu.Unlock()

		plannerRuns++

		// 模拟 Planner 生成 Actions
		state.Set("actions", []string{"scan", "analyze"})
		return state, nil
	})

	// 节点 3: executor
	graph.AddNode("executor", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		mu.Lock()
		defer mu.Unlock()

		actionsRaw, _ := state.Get("actions")
		actions, ok := actionsRaw.([]string)
		if !ok || len(actions) == 0 {
			return state, nil
		}

		executorRuns++

		// 模拟 Executor 产出 Hypotheses
		for i, action := range actions {
			hypID := action + "-hypothesis-" + string(rune('0'+i))
			hypothesesCreated = append(hypothesesCreated, hypID)
		}

		state.Set("hypotheses", hypothesesCreated)
		return state, nil
	})

	// 节点 4: evaluator（验证 Hypotheses）
	graph.AddNode("evaluator", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		mu.Lock()
		hypothesesRaw, _ := state.Get("hypotheses")
		hypotheses, ok := hypothesesRaw.([]string)
		mu.Unlock()

		if !ok || len(hypotheses) == 0 {
			return state, nil
		}

		// 并行验证 Hypotheses
		var wg sync.WaitGroup
		for _, hypID := range hypotheses {
			wg.Add(1)
			go func(observationID string) {
				defer wg.Done()

				mu.Lock()
				evaluatorRuns++
				mu.Unlock()

				// 模拟 LLM 验证（假设 50% 验证通过）
				if len(observationID)%2 == 0 {
					// 验证通过，创建 Finding
					findingID := observationID + "-finding"
					mu.Lock()
					findingsCreated = append(findingsCreated, findingID)
					mu.Unlock()
				}
			}(hypID)
		}

		wg.Wait()

		state.Set("findings", findingsCreated)
		return state, nil
	})

	// 节点 5: check_completion
	graph.AddNode("check_completion", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		mu.Lock()
		defer mu.Unlock()

		// 简化：执行一轮后完成
		completed := executorRuns >= 1
		state.Set("task_completed", completed)
		return state, nil
	})

	// 构建 Graph 拓扑
	graph.AddEdge("initialize", "planner")
	graph.AddEdge("planner", "executor")
	graph.AddEdge("executor", "evaluator")
	graph.AddEdge("evaluator", "check_completion")

	// 条件边：check_completion → planner（继续）或 END（完成）
	graph.AddConditionalEdge("check_completion", func(ctx context.Context, state core.GraphState) (string, error) {
		completed, _ := state.GetBool("task_completed")
		if completed {
			return "end", nil
		}
		return "continue", nil
	}, map[string]string{
		"continue": "planner",
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

	assert.Equal(t, 1, plannerRuns, "Planner 应该运行 1 次")
	assert.Equal(t, 1, executorRuns, "Executor 应该运行 1 次")
	assert.Equal(t, 2, evaluatorRuns, "Evaluator 应该运行 2 次（验证 2 个 Hypotheses）")
	assert.Equal(t, 2, len(hypothesesCreated), "应该创建 2 个 Hypotheses")
	assert.Greater(t, len(findingsCreated), 0, "应该创建至少 1 个 Finding")

	t.Logf("✅ Graph 包含 Evaluator 验证通过")
	t.Logf("   - Planner 运行: %d 次", plannerRuns)
	t.Logf("   - Executor 运行: %d 次", executorRuns)
	t.Logf("   - Evaluator 运行: %d 次", evaluatorRuns)
	t.Logf("   - Hypotheses 创建: %d 个", len(hypothesesCreated))
	t.Logf("   - Findings 创建: %d 个", len(findingsCreated))
}

// TestGraphRunner_EvaluatorParallelVerification 验证 Evaluator 并行验证多个 Hypotheses
func TestGraphRunner_EvaluatorParallelVerification(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var (
		mu             sync.Mutex
		verifyTimes    = make(map[string]time.Time)
		verifiedCount  int
	)

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	// 模拟多个 Hypotheses
	hypotheses := []string{"hyp-1", "hyp-2", "hyp-3", "hyp-4", "hyp-5"}

	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("hypotheses", hypotheses)
		return state, nil
	})

	graph.AddNode("verify", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		hypothesesRaw, _ := state.Get("hypotheses")
		hyps, ok := hypothesesRaw.([]string)
		if !ok {
			return state, nil
		}

		// 并行验证
		var wg sync.WaitGroup
		for _, hypID := range hyps {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()

				mu.Lock()
				verifyTimes[id] = time.Now()
				verifiedCount++
				mu.Unlock()

				// 模拟验证耗时
				time.Sleep(10 * time.Millisecond)
			}(hypID)
		}

		wg.Wait()

		state.Set("verified_count", verifiedCount)
		return state, nil
	})

	graph.AddEdge("start", "verify")

	// 编译并运行
	err := graph.Compile()
	require.NoError(t, err)

	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证结果
	verifiedCountFinal, _ := finalState.GetInt("verified_count")
	assert.Equal(t, 5, verifiedCountFinal, "应该验证 5 个 Hypotheses")

	// 验证并行执行（时间戳应该接近）
	times := make([]time.Time, 0, len(verifyTimes))
	for _, t := range verifyTimes {
		times = append(times, t)
	}

	if len(times) == 5 {
		var maxDiff time.Duration
		for i := 1; i < len(times); i++ {
			diff := times[i].Sub(times[0])
			if diff < 0 {
				diff = -diff
			}
			if diff > maxDiff {
				maxDiff = diff
			}
		}

		// 并行执行时，时间差应该小于 5ms
		assert.Less(t, maxDiff, 5*time.Millisecond, "Hypotheses 应该并行验证")
		t.Logf("✅ 并行验证时间差: %v", maxDiff)
	}

	t.Logf("✅ Evaluator 并行验证通过")
	t.Logf("   - 验证数量: %d", verifiedCount)
}

// TestGraphRunner_FullPipeline 测试完整的 Planner → Executor → Evaluator 管道
func TestGraphRunner_FullPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var (
		mu           sync.Mutex
		pipelineLog  []string
	)

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 50,
	}
	graph := core.NewGraph("initialize", config)

	graph.AddNode("initialize", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		mu.Lock()
		pipelineLog = append(pipelineLog, "initialize")
		mu.Unlock()

		state.Set("iteration", 0)
		state.Set("actions_executed", 0)
		return state, nil
	})

	graph.AddNode("planner", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		mu.Lock()
		pipelineLog = append(pipelineLog, "planner")
		mu.Unlock()

		actionsExecuted, _ := state.GetInt("actions_executed")

		// 生成最多 3 个 Actions
		var actions []string
		if actionsExecuted < 3 {
			actions = []string{"action-1"}
		}

		state.Set("actions", actions)
		return state, nil
	})

	graph.AddNode("executor", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		actionsRaw, _ := state.Get("actions")
		actions, ok := actionsRaw.([]string)
		if !ok || len(actions) == 0 {
			return state, nil
		}

		mu.Lock()
		pipelineLog = append(pipelineLog, "executor")
		mu.Unlock()

		// 产出 Hypotheses
		state.Set("hypotheses", []string{"hyp-1", "hyp-2"})

		actionsExecuted, _ := state.GetInt("actions_executed")
		state.Set("actions_executed", actionsExecuted+1)

		return state, nil
	})

	graph.AddNode("evaluator", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		hypothesesRaw, _ := state.Get("hypotheses")
		hypotheses, ok := hypothesesRaw.([]string)
		if !ok || len(hypotheses) == 0 {
			return state, nil
		}

		mu.Lock()
		pipelineLog = append(pipelineLog, "evaluator")
		mu.Unlock()

		// 模拟验证
		state.Set("findings", []string{"finding-1"})
		return state, nil
	})

	graph.AddNode("check_completion", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		actionsExecuted, _ := state.GetInt("actions_executed")
		completed := actionsExecuted >= 3
		state.Set("task_completed", completed)

		iteration, _ := state.GetInt("iteration")
		state.Set("iteration", iteration+1)

		return state, nil
	})

	// 构建管道
	graph.AddEdge("initialize", "planner")
	graph.AddConditionalEdge("planner", func(ctx context.Context, state core.GraphState) (string, error) {
		actionsRaw, _ := state.Get("actions")
		actions, ok := actionsRaw.([]string)
		if ok && len(actions) > 0 {
			return "execute", nil
		}
		return "check", nil
	}, map[string]string{
		"execute": "executor",
		"check":   "check_completion",
	})
	graph.AddEdge("executor", "evaluator")
	graph.AddEdge("evaluator", "check_completion")
	graph.AddConditionalEdge("check_completion", func(ctx context.Context, state core.GraphState) (string, error) {
		completed, _ := state.GetBool("task_completed")
		if completed {
			return "end", nil
		}
		return "continue", nil
	}, map[string]string{
		"continue": "planner",
		"end":      core.END,
	})

	// 编译并运行
	err := graph.Compile()
	require.NoError(t, err)

	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证管道执行
	taskCompleted, _ := finalState.GetBool("task_completed")
	assert.True(t, taskCompleted)

	// 验证执行顺序包含所有阶段
	mu.Lock()
	logStr := fmt.Sprint(pipelineLog)
	mu.Unlock()

	assert.Contains(t, logStr, "initialize", "应该包含 initialize")
	assert.Contains(t, logStr, "planner", "应该包含 planner")
	assert.Contains(t, logStr, "executor", "应该包含 executor")
	assert.Contains(t, logStr, "evaluator", "应该包含 evaluator")

	t.Logf("✅ 完整管道验证通过")
	t.Logf("   - 执行阶段: %v", pipelineLog)
}
