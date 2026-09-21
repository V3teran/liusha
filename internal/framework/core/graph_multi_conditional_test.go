package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGraph_MultiConditionalRoutes 测试多条件分支路由
func TestGraph_MultiConditionalRoutes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	// 构建节点
	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("value", 50)
		return state, nil
	})

	graph.AddNode("high", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("result", "high")
		return state, nil
	})

	graph.AddNode("medium", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("result", "medium")
		return state, nil
	})

	graph.AddNode("low", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("result", "low")
		return state, nil
	})

	graph.AddNode("default_node", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("result", "default")
		return state, nil
	})

	// 添加多条件路由：按顺序评估
	routes := []core.ConditionalRoute{
		{
			Condition: func(ctx context.Context, state core.GraphState) (bool, error) {
				val, _ := state.GetInt("value")
				return val > 80, nil
			},
			Target: "high",
		},
		{
			Condition: func(ctx context.Context, state core.GraphState) (bool, error) {
				val, _ := state.GetInt("value")
				return val > 50, nil
			},
			Target: "medium",
		},
		{
			Condition: func(ctx context.Context, state core.GraphState) (bool, error) {
				val, _ := state.GetInt("value")
				return val > 20, nil
			},
			Target: "low",
		},
	}

	err := graph.AddConditionalRoutes("start", routes, "default_node")
	require.NoError(t, err)

	// 编译并运行
	err = graph.Compile()
	require.NoError(t, err)

	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证结果：value=50，满足第三个条件 (> 20)
	result, err := finalState.GetString("result")
	require.NoError(t, err)
	assert.Equal(t, "low", result)
}

// TestGraph_MultiConditionalRoutes_DefaultRoute 测试默认路由
func TestGraph_MultiConditionalRoutes_DefaultRoute(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("value", 5)
		return state, nil
	})

	graph.AddNode("high", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("result", "high")
		return state, nil
	})

	graph.AddNode("default_node", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("result", "default")
		return state, nil
	})

	// 所有条件都不满足，应该走 default
	routes := []core.ConditionalRoute{
		{
			Condition: func(ctx context.Context, state core.GraphState) (bool, error) {
				val, _ := state.GetInt("value")
				return val > 100, nil
			},
			Target: "high",
		},
	}

	err := graph.AddConditionalRoutes("start", routes, "default_node")
	require.NoError(t, err)

	err = graph.Compile()
	require.NoError(t, err)

	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err)

	result, err := finalState.GetString("result")
	require.NoError(t, err)
	assert.Equal(t, "default", result)
}

// TestGraph_MultiConditionalRoutes_ShortCircuit 测试短路求值
func TestGraph_MultiConditionalRoutes_ShortCircuit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("value", 90)
		return state, nil
	})

	graph.AddNode("first", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("result", "first")
		return state, nil
	})

	graph.AddNode("second", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("result", "second")
		return state, nil
	})

	evaluationCount := 0

	// 第一个条件满足后，应该短路，不再评估后续条件
	routes := []core.ConditionalRoute{
		{
			Condition: func(ctx context.Context, state core.GraphState) (bool, error) {
				evaluationCount++
				val, _ := state.GetInt("value")
				return val > 50, nil
			},
			Target: "first",
		},
		{
			Condition: func(ctx context.Context, state core.GraphState) (bool, error) {
				evaluationCount++
				return true, nil
			},
			Target: "second",
		},
	}

	err := graph.AddConditionalRoutes("start", routes, core.END)
	require.NoError(t, err)

	err = graph.Compile()
	require.NoError(t, err)

	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err)

	result, err := finalState.GetString("result")
	require.NoError(t, err)
	assert.Equal(t, "first", result)

	// 验证短路：只评估了第一个条件
	assert.Equal(t, 1, evaluationCount, "应该短路，只评估第一个条件")
}

// TestGraph_MultiConditionalRoutes_ValidationErrors 测试验证错误
func TestGraph_MultiConditionalRoutes_ValidationErrors(t *testing.T) {
	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}

	t.Run("empty routes", func(t *testing.T) {
		graph := core.NewGraph("start", config)
		graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			return state, nil
		})

		err := graph.AddConditionalRoutes("start", []core.ConditionalRoute{}, "default")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "routes cannot be empty")
	})

	t.Run("empty default target", func(t *testing.T) {
		graph := core.NewGraph("start", config)
		graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			return state, nil
		})

		routes := []core.ConditionalRoute{
			{
				Condition: func(ctx context.Context, state core.GraphState) (bool, error) {
					return true, nil
				},
				Target: "next",
			},
		}

		err := graph.AddConditionalRoutes("start", routes, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "defaultTarget is required")
	})

	t.Run("target node not found", func(t *testing.T) {
		graph := core.NewGraph("start", config)
		graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
			return state, nil
		})

		routes := []core.ConditionalRoute{
			{
				Condition: func(ctx context.Context, state core.GraphState) (bool, error) {
					return true, nil
				},
				Target: "nonexistent",
			},
		}

		err := graph.AddConditionalRoutes("start", routes, "default")
		require.NoError(t, err)

		err = graph.Compile()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "target node not found")
	})
}
