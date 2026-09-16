package core_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/framework/core"
)

// TestGraph_IntegrationWithAgents 集成测试：Graph 编排 Agent
func TestGraph_IntegrationWithAgents(t *testing.T) {
	t.Run("模拟 Planner-Executor 循环", func(t *testing.T) {
		// 模拟 Planner 和 Executor Agent 的行为
		// 使用条件边来实现循环，避免环检测

		config := &core.GraphConfig{
			ExecutionMode: core.ExecutionModeSequential,
			MaxIterations: 10,
		}
		graph := core.NewGraph("planner", config)

		// 共享状态：模拟黑板
		var plannerCallCount int32
		var executorCallCount int32
		var actionsGenerated []string
		var actionsCompleted []string

		// Planner 节点：生成 Actions
		graph.AddNode("planner", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			count := atomic.AddInt32(&plannerCallCount, 1)

			// 模拟规划逻辑
			iteration, _ := state.GetInt("iteration")
			if iteration == 0 {
				// 初始规划
				actionsGenerated = []string{"scan", "exploit"}
				state.Set("actions", actionsGenerated)
				state.Set("actions_completed", []string{})
				state.Set("should_execute", true)
			} else {
				// 重规划：根据已完成的 Actions 决定
				completed, _ := state.Get("actions_completed")
				completedList := completed.([]string)

				if len(completedList) < 2 {
					// 还有未完成的 Actions
					state.Set("should_execute", true)
				} else {
					// 所有 Actions 完成
					state.Set("should_execute", false)
				}
			}

			state.Set("iteration", iteration+1)
			state.Set("planner_count", int(count))
			return state, nil
		})

		// Executor 节点：执行 Actions
		graph.AddNode("executor", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			count := atomic.AddInt32(&executorCallCount, 1)

			// 模拟执行逻辑
			actions, _ := state.Get("actions")
			actionList := actions.([]string)
			completed, _ := state.Get("actions_completed")
			completedList := completed.([]string)

			// 执行一个 Action
			if len(completedList) < len(actionList) {
				nextAction := actionList[len(completedList)]
				completedList = append(completedList, nextAction)
				actionsCompleted = completedList
				state.Set("actions_completed", completedList)
			}

			state.Set("executor_count", int(count))
			state.Set("need_replan", true) // 执行后需要重新规划
			return state, nil
		})

		// Planner 的条件路由
		graph.AddConditionalEdge("planner", func(ctx context.Context, state core.GraphState) (string, error) {
			shouldExecute, _ := state.GetBool("should_execute")
			if shouldExecute {
				return "execute", nil
			}
			return "end", nil
		}, map[string]string{
			"execute": "executor",
			"end":     core.END,
		})

		// Executor 的条件路由：回到 Planner 或结束
		graph.AddConditionalEdge("executor", func(ctx context.Context, state core.GraphState) (string, error) {
			needReplan, _ := state.GetBool("need_replan")
			if needReplan {
				return "replan", nil
			}
			return "end", nil
		}, map[string]string{
			"replan": "planner",
			"end":    core.END,
		})

		err := graph.Compile()
		require.NoError(t, err)

		// 运行
		input := core.NewGraphState()
		input.Set("iteration", 0)
		input.Set("actions", []string{})
		input.Set("actions_completed", []string{})

		output, err := graph.Run(context.Background(), *input)
		require.NoError(t, err)

		// 验证结果
		plannerCount, _ := output.GetInt("planner_count")
		executorCount, _ := output.GetInt("executor_count")

		assert.Equal(t, 3, plannerCount, "Planner 应该运行 3 次（初始 + 2 次重规划）")
		assert.Equal(t, 2, executorCount, "Executor 应该运行 2 次（执行 2 个 Actions）")
		assert.Equal(t, []string{"scan", "exploit"}, actionsCompleted, "应该完成所有 Actions")

		t.Logf("✅ Planner-Executor 循环验证通过")
		t.Logf("   - Planner 运行: %d 次", plannerCount)
		t.Logf("   - Executor 运行: %d 次", executorCount)
		t.Logf("   - Actions 完成: %v", actionsCompleted)
	})

	t.Run("并发执行多个 Actions", func(t *testing.T) {
		// 验证并发模式下可以同时执行多个独立的 Actions
		config := &core.GraphConfig{
			ExecutionMode: core.ExecutionModeConcurrent,
			MaxIterations: 10,
		}
		graph := core.NewGraph("start", config)

		var execOrder []string

		// Start 节点
		graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			state.Set("actions", []string{"action_a", "action_b", "action_c"})
			return state, nil
		})

		// 三个并行的 Action 执行节点
		for _, actionName := range []string{"action_a", "action_b", "action_c"} {
			name := actionName // 捕获循环变量
			graph.AddNode(name, func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
				time.Sleep(10 * time.Millisecond) // 模拟执行耗时
				execOrder = append(execOrder, name)
				state.Set(name+"_done", true)
				return state, nil
			})
			graph.AddEdge("start", name)
			graph.AddEdge(name, core.END)
		}

		err := graph.Compile()
		require.NoError(t, err)

		input := core.NewGraphState()
		startTime := time.Now()
		output, err := graph.Run(context.Background(), *input)
		duration := time.Since(startTime)

		require.NoError(t, err)

		// 验证所有 Actions 都完成
		aDone, _ := output.GetBool("action_a_done")
		bDone, _ := output.GetBool("action_b_done")
		cDone, _ := output.GetBool("action_c_done")

		assert.True(t, aDone)
		assert.True(t, bDone)
		assert.True(t, cDone)

		// 验证并行执行（3 个 10ms 的任务并行应该在 20ms 内完成）
		assert.Less(t, duration, 20*time.Millisecond, "并行执行应该比串行快")

		t.Logf("✅ 并发执行多个 Actions 验证通过")
		t.Logf("   - 总耗时: %v", duration)
	})

	t.Run("依赖 Actions 的串联执行", func(t *testing.T) {
		// 验证有依赖关系的 Actions 按正确顺序执行
		// 场景：scan -> analyze -> exploit
		config := &core.GraphConfig{
			ExecutionMode: core.ExecutionModeConcurrent,
			MaxIterations: 10,
		}
		graph := core.NewGraph("scan", config)

		var execOrder []string

		// scan 节点
		graph.AddNode("scan", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			execOrder = append(execOrder, "scan")
			state.Set("targets", []string{"target1", "target2"})
			return state, nil
		})

		// analyze 节点（依赖 scan）
		graph.AddNode("analyze", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			targets, _ := state.Get("targets")
			if targets == nil {
				return state, fmt.Errorf("analyze 需要 scan 的结果")
			}
			execOrder = append(execOrder, "analyze")
			state.Set("vulnerabilities", []string{"vuln1"})
			return state, nil
		})

		// exploit 节点（依赖 analyze）
		graph.AddNode("exploit", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			vulns, _ := state.Get("vulnerabilities")
			if vulns == nil {
				return state, fmt.Errorf("exploit 需要 analyze 的结果")
			}
			execOrder = append(execOrder, "exploit")
			state.Set("exploited", true)
			return state, nil
		})

		graph.AddEdge("scan", "analyze")
		graph.AddEdge("analyze", "exploit")
		graph.AddEdge("exploit", core.END)

		err := graph.Compile()
		require.NoError(t, err)

		input := core.NewGraphState()
		output, err := graph.Run(context.Background(), *input)
		require.NoError(t, err)

		// 验证执行顺序
		assert.Equal(t, []string{"scan", "analyze", "exploit"}, execOrder, "应该按依赖顺序执行")

		exploited, _ := output.GetBool("exploited")
		assert.True(t, exploited)

		t.Logf("✅ 依赖 Actions 串联执行验证通过")
		t.Logf("   - 执行顺序: %v", execOrder)
	})

	t.Run("条件分支：根据结果选择不同路径", func(t *testing.T) {
		// 验证条件路由：scan 后根据是否发现漏洞决定下一步
		config := &core.GraphConfig{
			ExecutionMode: core.ExecutionModeSequential,
			MaxIterations: 10,
		}
		graph := core.NewGraph("scan", config)

		// scan 节点
		graph.AddNode("scan", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			// 模拟扫描发现漏洞
			state.Set("vulnerabilities_found", true)
			state.Set("vuln_count", 3)
			return state, nil
		})

		// exploit 节点
		graph.AddNode("exploit", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			state.Set("exploited", true)
			return state, nil
		})

		// report 节点
		graph.AddNode("report", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			state.Set("reported", true)
			return state, nil
		})

		// 条件路由：根据是否发现漏洞
		graph.AddConditionalEdge("scan", func(ctx context.Context, state core.GraphState) (string, error) {
			found, _ := state.GetBool("vulnerabilities_found")
			if found {
				return "exploit", nil
			}
			return "report", nil
		}, map[string]string{
			"exploit": "exploit",
			"report":  "report",
		})

		graph.AddEdge("exploit", core.END)
		graph.AddEdge("report", core.END)

		err := graph.Compile()
		require.NoError(t, err)

		input := core.NewGraphState()
		output, err := graph.Run(context.Background(), *input)
		require.NoError(t, err)

		// 验证走了 exploit 路径
		exploited, _ := output.GetBool("exploited")
		reported, _ := output.GetBool("reported")

		assert.True(t, exploited, "应该执行 exploit")
		assert.False(t, reported, "不应该执行 report")

		t.Logf("✅ 条件分支验证通过")
	})
}

// TestGraph_StateManagement 测试状态管理
func TestGraph_StateManagement(t *testing.T) {
	t.Run("状态在节点间正确传递", func(t *testing.T) {
		config := &core.GraphConfig{
			ExecutionMode: core.ExecutionModeSequential,
			MaxIterations: 10,
		}
		graph := core.NewGraph("node1", config)

		graph.AddNode("node1", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			state.Set("key1", "value1")
			state.Set("counter", 1)
			return state, nil
		})

		graph.AddNode("node2", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			// 读取前一个节点的状态
			key1, _ := state.GetString("key1")
			counter, _ := state.GetInt("counter")

			if key1 != "value1" || counter != 1 {
				return state, fmt.Errorf("状态传递失败")
			}

			// 修改状态
			state.Set("key2", "value2")
			state.Set("counter", counter+1)
			return state, nil
		})

		graph.AddEdge("node1", "node2")
		graph.AddEdge("node2", core.END)

		err := graph.Compile()
		require.NoError(t, err)

		input := core.NewGraphState()
		output, err := graph.Run(context.Background(), *input)
		require.NoError(t, err)

		// 验证最终状态
		key1, _ := output.GetString("key1")
		key2, _ := output.GetString("key2")
		counter, _ := output.GetInt("counter")

		assert.Equal(t, "value1", key1)
		assert.Equal(t, "value2", key2)
		assert.Equal(t, 2, counter)

		t.Log("✅ 状态传递验证通过")
	})

	t.Run("并发模式下状态合并", func(t *testing.T) {
		config := &core.GraphConfig{
			ExecutionMode: core.ExecutionModeConcurrent,
			MaxIterations: 10,
		}
		graph := core.NewGraph("start", config)

		graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			state.Set("started", true)
			return state, nil
		})

		// 两个并行节点，分别设置不同的键
		graph.AddNode("node_a", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			time.Sleep(10 * time.Millisecond)
			state.Set("result_a", "A")
			return state, nil
		})

		graph.AddNode("node_b", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			time.Sleep(10 * time.Millisecond)
			state.Set("result_b", "B")
			return state, nil
		})

		graph.AddEdge("start", "node_a")
		graph.AddEdge("start", "node_b")
		graph.AddEdge("node_a", core.END)
		graph.AddEdge("node_b", core.END)

		err := graph.Compile()
		require.NoError(t, err)

		input := core.NewGraphState()
		output, err := graph.Run(context.Background(), *input)
		require.NoError(t, err)

		// 验证两个并行节点的结果都被合并了
		started, _ := output.GetBool("started")
		resultA, _ := output.GetString("result_a")
		resultB, _ := output.GetString("result_b")

		assert.True(t, started)
		assert.Equal(t, "A", resultA)
		assert.Equal(t, "B", resultB)

		t.Log("✅ 并发状态合并验证通过")
	})
}
