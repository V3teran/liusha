package core_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/framework/core"
)

// TestGraph_ConcurrentMode 并发模式核心测试
func TestGraph_ConcurrentMode(t *testing.T) {
	t.Run("简单并行执行", func(t *testing.T) {
		config := &core.GraphConfig{
			ExecutionMode: core.ExecutionModeConcurrent,
			MaxIterations: 1000,
		}
		graph := core.NewGraph("start", config)

		// 记录执行顺序
		var mu sync.Mutex
		execOrder := []string{}

		// start 节点
		graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			mu.Lock()
			execOrder = append(execOrder, "start")
			mu.Unlock()
			state.Set("start", true)
			return state, nil
		})

		// 两个并行节点 a 和 b
		graph.AddNode("a", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			time.Sleep(10 * time.Millisecond)
			mu.Lock()
			execOrder = append(execOrder, "a")
			mu.Unlock()
			state.Set("a", true)
			return state, nil
		})

		graph.AddNode("b", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			time.Sleep(10 * time.Millisecond)
			mu.Lock()
			execOrder = append(execOrder, "b")
			mu.Unlock()
			state.Set("b", true)
			return state, nil
		})

		// 图结构：start -> a, start -> b, a -> END, b -> END
		graph.AddEdge("start", "a")
		graph.AddEdge("start", "b")
		graph.AddEdge("a", core.END)
		graph.AddEdge("b", core.END)

		err := graph.Compile()
		require.NoError(t, err)

		// 执行
		input := core.NewGraphState()
		startTime := time.Now()
		output, err := graph.Run(context.Background(), *input)
		duration := time.Since(startTime)

		require.NoError(t, err)

		// 验证结果
		startVal, _ := output.GetBool("start")
		aVal, _ := output.GetBool("a")
		bVal, _ := output.GetBool("b")

		assert.True(t, startVal, "start should execute")
		assert.True(t, aVal, "a should execute")
		assert.True(t, bVal, "b should execute")

		// 验证并行执行（如果串行，至少需要 20ms；并行应该在 15ms 内完成）
		assert.Less(t, duration, 15*time.Millisecond, "并行执行应该比串行快")

		// 验证执行顺序：start 必须先执行，a 和 b 可以乱序
		assert.Equal(t, "start", execOrder[0], "start 必须第一个执行")
		assert.Contains(t, execOrder, "a", "a 应该执行")
		assert.Contains(t, execOrder, "b", "b 应该执行")

		t.Logf("✅ 并行执行验证通过：耗时 %v", duration)
	})

	t.Run("多层依赖并行", func(t *testing.T) {
		config := &core.GraphConfig{
			ExecutionMode: core.ExecutionModeConcurrent,
			MaxIterations: 1000,
		}
		graph := core.NewGraph("start", config)

		// 图结构：
		//   start
		//   /   \
		//  a     b     (第 1 层，可并行)
		//   \   /
		//     c         (第 2 层，依赖 a 和 b)
		//     |
		//    END

		graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			state.Set("start", true)
			return state, nil
		})

		graph.AddNode("a", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			time.Sleep(10 * time.Millisecond)
			state.Set("a", true)
			return state, nil
		})

		graph.AddNode("b", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			time.Sleep(10 * time.Millisecond)
			state.Set("b", true)
			return state, nil
		})

		graph.AddNode("c", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			// c 依赖 a 和 b，应该在它们之后执行
			aVal, _ := state.GetBool("a")
			bVal, _ := state.GetBool("b")
			if !aVal || !bVal {
				return state, fmt.Errorf("c 执行时 a 和 b 应该已完成")
			}
			state.Set("c", true)
			return state, nil
		})

		graph.AddEdge("start", "a")
		graph.AddEdge("start", "b")
		graph.AddEdge("a", "c")
		graph.AddEdge("b", "c")
		graph.AddEdge("c", core.END)

		err := graph.Compile()
		require.NoError(t, err)

		input := core.NewGraphState()
		startTime := time.Now()
		output, err := graph.Run(context.Background(), *input)
		duration := time.Since(startTime)

		require.NoError(t, err)

		cVal, _ := output.GetBool("c")
		assert.True(t, cVal, "c 应该成功执行")

		// 验证并行效果：a 和 b 并行（10ms）+ c（0ms）≈ 10ms
		assert.Less(t, duration, 15*time.Millisecond, "应该并行执行 a 和 b")

		t.Logf("✅ 多层依赖并行验证通过：耗时 %v", duration)
	})

	t.Run("并发模式错误处理", func(t *testing.T) {
		config := &core.GraphConfig{
			ExecutionMode: core.ExecutionModeConcurrent,
			MaxIterations: 1000,
		}
		graph := core.NewGraph("start", config)

		graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			return state, nil
		})

		graph.AddNode("a", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			return state, nil
		})

		graph.AddNode("b", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			return state, fmt.Errorf("node b failed")
		})

		graph.AddEdge("start", "a")
		graph.AddEdge("start", "b")
		graph.AddEdge("a", core.END)
		graph.AddEdge("b", core.END)

		err := graph.Compile()
		require.NoError(t, err)

		input := core.NewGraphState()
		_, err = graph.Run(context.Background(), *input)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "node b failed")

		t.Log("✅ 并发模式错误处理验证通过")
	})

	t.Run("并发模式上下文取消", func(t *testing.T) {
		config := &core.GraphConfig{
			ExecutionMode: core.ExecutionModeConcurrent,
			MaxIterations: 1000,
		}
		graph := core.NewGraph("start", config)

		graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			return state, nil
		})

		graph.AddNode("a", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			select {
			case <-ctx.Done():
				return state, ctx.Err()
			case <-time.After(100 * time.Millisecond):
				return state, nil
			}
		})

		graph.AddNode("b", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			select {
			case <-ctx.Done():
				return state, ctx.Err()
			case <-time.After(100 * time.Millisecond):
				return state, nil
			}
		})

		graph.AddEdge("start", "a")
		graph.AddEdge("start", "b")
		graph.AddEdge("a", core.END)
		graph.AddEdge("b", core.END)

		err := graph.Compile()
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		input := core.NewGraphState()
		_, err = graph.Run(ctx, *input)

		assert.Error(t, err)
		// 上下文取消错误可能从节点函数传播，或从 runConcurrent 的上下文检查传播
		assert.True(t, err == context.DeadlineExceeded || err.Error() == "context deadline exceeded" ||
			err.Error() == "node a failed: context deadline exceeded" ||
			err.Error() == "node b failed: context deadline exceeded",
			"应该返回上下文取消错误，实际: %v", err)

		t.Log("✅ 并发模式上下文取消验证通过")
	})
}

// TestGraph_ConcurrentVsSequential 对比并发和串行模式
func TestGraph_ConcurrentVsSequential(t *testing.T) {
	// 创建一个线性链（串行）和扇形图（并行）

	// 串行模式：start -> node_0 -> node_1 -> node_2 -> node_3 -> END
	t.Run("串行链", func(t *testing.T) {
		config := &core.GraphConfig{
			ExecutionMode: core.ExecutionModeSequential,
			MaxIterations: 1000,
		}
		graph := core.NewGraph("start", config)

		graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			state.Set("start", true)
			return state, nil
		})

		prev := "start"
		for i := 0; i < 4; i++ {
			name := fmt.Sprintf("node_%d", i)
			graph.AddNode(name, func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
				time.Sleep(10 * time.Millisecond)
				state.Set(name, true)
				return state, nil
			})
			graph.AddEdge(prev, name)
			prev = name
		}
		graph.AddEdge(prev, core.END)

		graph.Compile()

		input := core.NewGraphState()
		startTime := time.Now()
		_, err := graph.Run(context.Background(), *input)
		duration := time.Since(startTime)
		require.NoError(t, err)

		t.Logf("📊 串行链耗时: %v", duration)
		// 预期：4 个节点 × 10ms ≈ 40ms
		assert.GreaterOrEqual(t, duration, 40*time.Millisecond)
	})

	// 并发模式：start -> (node_0, node_1, node_2, node_3) -> END
	t.Run("并行扇形", func(t *testing.T) {
		config := &core.GraphConfig{
			ExecutionMode: core.ExecutionModeConcurrent,
			MaxIterations: 1000,
		}
		graph := core.NewGraph("start", config)

		graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			state.Set("start", true)
			return state, nil
		})

		for i := 0; i < 4; i++ {
			name := fmt.Sprintf("node_%d", i)
			graph.AddNode(name, func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
				time.Sleep(10 * time.Millisecond)
				state.Set(name, true)
				return state, nil
			})
			graph.AddEdge("start", name)
			graph.AddEdge(name, core.END)
		}

		graph.Compile()

		input := core.NewGraphState()
		startTime := time.Now()
		_, err := graph.Run(context.Background(), *input)
		duration := time.Since(startTime)
		require.NoError(t, err)

		t.Logf("📊 并行扇形耗时: %v", duration)
		// 预期：4 个节点并行 ≈ 10ms
		assert.Less(t, duration, 20*time.Millisecond)
	})
}

// TestGraph_TopologicalSort 测试拓扑排序
func TestGraph_TopologicalSort(t *testing.T) {
	t.Run("DAG 拓扑排序", func(t *testing.T) {
		config := &core.GraphConfig{
			ExecutionMode: core.ExecutionModeConcurrent,
			MaxIterations: 1000,
		}
		graph := core.NewGraph("a", config)

		// 图: a -> b -> d
		//     a -> c -> d
		graph.AddNode("a", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			state.Set("a", 1)
			return state, nil
		})
		graph.AddNode("b", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			state.Set("b", 2)
			return state, nil
		})
		graph.AddNode("c", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			state.Set("c", 3)
			return state, nil
		})
		graph.AddNode("d", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			state.Set("d", 4)
			return state, nil
		})

		graph.AddEdge("a", "b")
		graph.AddEdge("a", "c")
		graph.AddEdge("b", "d")
		graph.AddEdge("c", "d")
		graph.AddEdge("d", core.END)

		err := graph.Compile()
		require.NoError(t, err)

		input := core.NewGraphState()
		output, err := graph.Run(context.Background(), *input)
		require.NoError(t, err)

		// 验证所有节点都执行了
		aVal, _ := output.GetInt("a")
		bVal, _ := output.GetInt("b")
		cVal, _ := output.GetInt("c")
		dVal, _ := output.GetInt("d")

		assert.Equal(t, 1, aVal)
		assert.Equal(t, 2, bVal)
		assert.Equal(t, 3, cVal)
		assert.Equal(t, 4, dVal)

		t.Log("✅ DAG 拓扑排序验证通过")
	})

	t.Run("环检测", func(t *testing.T) {
		config := &core.GraphConfig{
			ExecutionMode: core.ExecutionModeConcurrent,
			MaxIterations: 1000,
		}
		graph := core.NewGraph("a", config)

		graph.AddNode("a", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			return state, nil
		})
		graph.AddNode("b", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			return state, nil
		})

		// 创建环：a -> b -> a
		graph.AddEdge("a", "b")
		graph.AddEdge("b", "a")

		err := graph.Compile()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cycle")

		t.Log("✅ 环检测验证通过")
	})
}
