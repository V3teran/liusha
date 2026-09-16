package core_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/framework/core"
)

// TestGraph_MinimalValidation 阶段 0：最小验证
func TestGraph_MinimalValidation(t *testing.T) {
	t.Run("基础执行流程", func(t *testing.T) {
		graph := core.NewGraph("start", nil)

		// 添加两个简单节点
		err := graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			state.Set("step", "planner executed")
			return state, nil
		})
		require.NoError(t, err)

		err = graph.AddNode("next", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			state.Set("step", "executor executed")
			return state, nil
		})
		require.NoError(t, err)

		// 添加边
		err = graph.AddEdge("start", "next")
		require.NoError(t, err)

		err = graph.AddEdge("next", core.END)
		require.NoError(t, err)

		// 编译
		err = graph.Compile()
		require.NoError(t, err)

		// 运行
		input := core.NewGraphState()
		input.Set("step", "initial")

		output, err := graph.Run(context.Background(), *input)
		require.NoError(t, err)

		// 验证结果
		step, err := output.GetString("step")
		require.NoError(t, err)
		assert.Equal(t, "executor executed", step)

		t.Log("✅ 最小验证通过：Graph 可以正确执行 节点 → 边 → 节点 → END")
	})

	t.Run("条件路由", func(t *testing.T) {
		graph := core.NewGraph("start", nil)

		executed := make([]string, 0)

		err := graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			executed = append(executed, "start")
			state.Set("should_continue", true)
			return state, nil
		})
		require.NoError(t, err)

		err = graph.AddNode("continue_node", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			executed = append(executed, "continue_node")
			return state, nil
		})
		require.NoError(t, err)

		// 条件路由
		err = graph.AddConditionalEdge("start", func(ctx context.Context, state core.GraphState) (string, error) {
			shouldContinue, err := state.GetBool("should_continue")
			if err != nil {
				return "", err
			}
			if shouldContinue {
				return "continue", nil
			}
			return "end", nil
		}, map[string]string{
			"continue": "continue_node",
			"end":      core.END,
		})
		require.NoError(t, err)

		err = graph.AddEdge("continue_node", core.END)
		require.NoError(t, err)

		err = graph.Compile()
		require.NoError(t, err)

		input := core.NewGraphState()
		_, err = graph.Run(context.Background(), *input)
		require.NoError(t, err)

		assert.Equal(t, []string{"start", "continue_node"}, executed)
		t.Log("✅ 条件路由验证通过")
	})

	t.Run("循环检测", func(t *testing.T) {
		graph := core.NewGraph("a", nil)

		graph.AddNode("a", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			return state, nil
		})
		graph.AddNode("b", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			return state, nil
		})

		// 创建循环：a -> b -> a
		graph.AddEdge("a", "b")
		graph.AddEdge("b", "a")

		err := graph.Compile()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cycle detected")
		t.Log("✅ 循环检测工作正常")
	})
}

// TestGraph_StateOperations State 操作测试
func TestGraph_StateOperations(t *testing.T) {
	t.Run("类型安全的 Get/Set", func(t *testing.T) {
		state := core.NewGraphState()

		// 设置不同类型的值
		state.Set("name", "test")
		state.Set("count", 42)
		state.Set("enabled", true)

		// 获取字符串
		name, err := state.GetString("name")
		require.NoError(t, err)
		assert.Equal(t, "test", name)

		// 获取整数
		count, err := state.GetInt("count")
		require.NoError(t, err)
		assert.Equal(t, 42, count)

		// 获取布尔
		enabled, err := state.GetBool("enabled")
		require.NoError(t, err)
		assert.True(t, enabled)

		// 类型错误
		_, err = state.GetString("count")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not string")

		// 键不存在
		_, err = state.GetString("nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("Clone 和 Merge", func(t *testing.T) {
		original := core.NewGraphState()
		original.Set("a", 1)
		original.Set("b", 2)

		// 克隆
		cloned := original.Clone()
		cloned.Set("c", 3)

		// 原始 State 不受影响
		_, ok := original.Get("c")
		assert.False(t, ok)

		// 合并
		original.Merge(cloned)
		c, err := original.GetInt("c")
		require.NoError(t, err)
		assert.Equal(t, 3, c)
	})
}

// TestGraph_ErrorHandling 错误处理测试
func TestGraph_ErrorHandling(t *testing.T) {
	t.Run("节点执行失败", func(t *testing.T) {
		graph := core.NewGraph("start", nil)

		graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			return state, fmt.Errorf("node failed")
		})

		graph.Compile()

		input := core.NewGraphState()
		_, err := graph.Run(context.Background(), *input)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "node start failed")
	})

	t.Run("条件函数失败", func(t *testing.T) {
		graph := core.NewGraph("start", nil)

		graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			return state, nil
		})

		graph.AddConditionalEdge("start", func(ctx context.Context, state core.GraphState) (string, error) {
			return "", fmt.Errorf("condition failed")
		}, map[string]string{
			"x": core.END,
		})

		graph.Compile()

		input := core.NewGraphState()
		_, err := graph.Run(context.Background(), *input)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "condition at node start failed")
	})

	t.Run("未编译就运行", func(t *testing.T) {
		graph := core.NewGraph("start", nil)
		graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			return state, nil
		})

		input := core.NewGraphState()
		_, err := graph.Run(context.Background(), *input)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not compiled")
	})

	t.Run("编译后不能修改", func(t *testing.T) {
		graph := core.NewGraph("start", nil)
		graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			return state, nil
		})
		graph.AddEdge("start", core.END)
		graph.Compile()

		// 尝试添加节点
		err := graph.AddNode("new", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			return state, nil
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "after compilation")
	})
}

// TestGraph_ContextCancellation 上下文取消测试
func TestGraph_ContextCancellation(t *testing.T) {
	graph := core.NewGraph("start", nil)

	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		// 模拟长时间运行
		<-ctx.Done()
		return state, ctx.Err()
	})

	graph.AddEdge("start", core.END)
	graph.Compile()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	input := core.NewGraphState()
	_, err := graph.Run(ctx, *input)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}
