package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGraph_Subgraph_Basic 测试基础子图嵌套
func TestGraph_Subgraph_Basic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 创建子图：简单的数据处理流程
	subgraphConfig := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	subgraph := core.NewGraph("sub_start", subgraphConfig)

	subgraph.AddNode("sub_start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		val, _ := state.GetInt("value")
		state.Set("value", val*2)
		return state, nil
	})

	subgraph.AddNode("sub_end", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		val, _ := state.GetInt("value")
		state.Set("value", val+10)
		return state, nil
	})

	subgraph.AddEdge("sub_start", "sub_end")
	subgraph.AddEdge("sub_end", core.END)

	err := subgraph.Compile()
	require.NoError(t, err)

	// 创建主图
	mainConfig := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	mainGraph := core.NewGraph("main_start", mainConfig)

	mainGraph.AddNode("main_start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("value", 5)
		return state, nil
	})

	// 添加子图作为节点
	err = mainGraph.AddSubgraph("process", subgraph)
	require.NoError(t, err)

	mainGraph.AddNode("main_end", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		val, _ := state.GetInt("value")
		state.Set("result", val+1)
		return state, nil
	})

	mainGraph.AddEdge("main_start", "process")
	mainGraph.AddEdge("process", "main_end")
	mainGraph.AddEdge("main_end", core.END)

	err = mainGraph.Compile()
	require.NoError(t, err)

	// 执行
	input := core.NewGraphState()
	finalState, err := mainGraph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证结果：(5 * 2) + 10 + 1 = 21
	result, err := finalState.GetInt("result")
	require.NoError(t, err)
	assert.Equal(t, 21, result)
}

// TestGraph_Subgraph_Nested 测试多层嵌套
func TestGraph_Subgraph_Nested(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 最内层子图：level3
	level3Config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	level3 := core.NewGraph("l3_start", level3Config)

	level3.AddNode("l3_start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		val, _ := state.GetInt("counter")
		state.Set("counter", val+1)
		state.Set("level3_visited", true)
		return state, nil
	})

	level3.AddEdge("l3_start", core.END)
	err := level3.Compile()
	require.NoError(t, err)

	// 中层子图：level2 包含 level3
	level2Config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	level2 := core.NewGraph("l2_start", level2Config)

	level2.AddNode("l2_start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		val, _ := state.GetInt("counter")
		state.Set("counter", val+1)
		state.Set("level2_visited", true)
		return state, nil
	})

	err = level2.AddSubgraph("nested_level3", level3)
	require.NoError(t, err)

	level2.AddEdge("l2_start", "nested_level3")
	level2.AddEdge("nested_level3", core.END)
	err = level2.Compile()
	require.NoError(t, err)

	// 主图：包含 level2
	mainConfig := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	mainGraph := core.NewGraph("main_start", mainConfig)

	mainGraph.AddNode("main_start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("counter", 0)
		state.Set("main_visited", true)
		return state, nil
	})

	err = mainGraph.AddSubgraph("nested_level2", level2)
	require.NoError(t, err)

	mainGraph.AddEdge("main_start", "nested_level2")
	mainGraph.AddEdge("nested_level2", core.END)
	err = mainGraph.Compile()
	require.NoError(t, err)

	// 执行
	input := core.NewGraphState()
	finalState, err := mainGraph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证所有层级都执行了
	counter, err := finalState.GetInt("counter")
	require.NoError(t, err)
	assert.Equal(t, 2, counter) // level2 +1, level3 +1

	mainVisited, err := finalState.GetBool("main_visited")
	require.NoError(t, err)
	assert.True(t, mainVisited)

	level2Visited, err := finalState.GetBool("level2_visited")
	require.NoError(t, err)
	assert.True(t, level2Visited)

	level3Visited, err := finalState.GetBool("level3_visited")
	require.NoError(t, err)
	assert.True(t, level3Visited)
}

// TestGraph_Subgraph_StateIsolation 测试状态传递
func TestGraph_Subgraph_StateIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 子图：处理特定键
	subgraphConfig := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	subgraph := core.NewGraph("sub_start", subgraphConfig)

	subgraph.AddNode("sub_start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		// 读取输入
		input, _ := state.GetString("input")

		// 处理并设置输出
		state.Set("output", input+"_processed")
		state.Set("sub_internal", "should_be_visible")

		return state, nil
	})

	subgraph.AddEdge("sub_start", core.END)
	err := subgraph.Compile()
	require.NoError(t, err)

	// 主图
	mainConfig := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	mainGraph := core.NewGraph("main_start", mainConfig)

	mainGraph.AddNode("main_start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("input", "data")
		state.Set("main_context", "context_value")
		return state, nil
	})

	err = mainGraph.AddSubgraph("process", subgraph)
	require.NoError(t, err)

	mainGraph.AddNode("main_end", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		// 验证子图的输出可见
		output, _ := state.GetString("output")
		state.Set("final", output+"_final")
		return state, nil
	})

	mainGraph.AddEdge("main_start", "process")
	mainGraph.AddEdge("process", "main_end")
	mainGraph.AddEdge("main_end", core.END)

	err = mainGraph.Compile()
	require.NoError(t, err)

	// 执行
	input := core.NewGraphState()
	finalState, err := mainGraph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证状态传递
	mainContext, err := finalState.GetString("main_context")
	require.NoError(t, err)
	assert.Equal(t, "context_value", mainContext)

	subInternal, err := finalState.GetString("sub_internal")
	require.NoError(t, err)
	assert.Equal(t, "should_be_visible", subInternal)

	final, err := finalState.GetString("final")
	require.NoError(t, err)
	assert.Equal(t, "data_processed_final", final)
}

// TestGraph_Subgraph_MustCompile 测试子图必须先编译
func TestGraph_Subgraph_MustCompile(t *testing.T) {
	// 未编译的子图
	subgraphConfig := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	subgraph := core.NewGraph("sub_start", subgraphConfig)

	subgraph.AddNode("sub_start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		return state, nil
	})
	subgraph.AddEdge("sub_start", core.END)

	// 不编译子图

	// 主图尝试添加未编译的子图
	mainConfig := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	mainGraph := core.NewGraph("main_start", mainConfig)

	mainGraph.AddNode("main_start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		return state, nil
	})

	err := mainGraph.AddSubgraph("sub", subgraph)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be compiled")
}

// TestGraph_Subgraph_ReuseSubgraph 测试子图复用
func TestGraph_Subgraph_ReuseSubgraph(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 可复用的子图：数值翻倍
	doubleConfig := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	doubleSubgraph := core.NewGraph("double_start", doubleConfig)

	doubleSubgraph.AddNode("double_start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		val, _ := state.GetInt("value")
		state.Set("value", val*2)
		return state, nil
	})

	doubleSubgraph.AddEdge("double_start", core.END)
	err := doubleSubgraph.Compile()
	require.NoError(t, err)

	// 主图：多次使用同一个子图
	mainConfig := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	mainGraph := core.NewGraph("main_start", mainConfig)

	mainGraph.AddNode("main_start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("value", 1)
		return state, nil
	})

	// 复用子图三次
	err = mainGraph.AddSubgraph("double1", doubleSubgraph)
	require.NoError(t, err)

	err = mainGraph.AddSubgraph("double2", doubleSubgraph)
	require.NoError(t, err)

	err = mainGraph.AddSubgraph("double3", doubleSubgraph)
	require.NoError(t, err)

	mainGraph.AddEdge("main_start", "double1")
	mainGraph.AddEdge("double1", "double2")
	mainGraph.AddEdge("double2", "double3")
	mainGraph.AddEdge("double3", core.END)

	err = mainGraph.Compile()
	require.NoError(t, err)

	// 执行
	input := core.NewGraphState()
	finalState, err := mainGraph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证结果：1 * 2 * 2 * 2 = 8
	value, err := finalState.GetInt("value")
	require.NoError(t, err)
	assert.Equal(t, 8, value)
}
