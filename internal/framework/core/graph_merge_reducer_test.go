package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGraphState_MergeStrategyOverwrite 测试覆盖策略
func TestGraphState_MergeStrategyOverwrite(t *testing.T) {
	state1 := core.NewGraphState()
	state1.Set("key1", "value1")
	state1.Set("key2", "old")

	state2 := core.NewGraphState()
	state2.Set("key2", "new")
	state2.Set("key3", "value3")

	options := &core.MergeOptions{
		Strategy: core.MergeStrategyOverwrite,
	}

	state1.MergeWithOptions(state2, options)

	// key1 保持不变
	val1, err := state1.GetString("key1")
	require.NoError(t, err)
	assert.Equal(t, "value1", val1)

	// key2 被覆盖
	val2, err := state1.GetString("key2")
	require.NoError(t, err)
	assert.Equal(t, "new", val2)

	// key3 被添加
	val3, err := state1.GetString("key3")
	require.NoError(t, err)
	assert.Equal(t, "value3", val3)
}

// TestGraphState_MergeStrategyKeepFirst 测试保留首个策略
func TestGraphState_MergeStrategyKeepFirst(t *testing.T) {
	state1 := core.NewGraphState()
	state1.Set("key1", "first")
	state1.Set("key2", "original")

	state2 := core.NewGraphState()
	state2.Set("key2", "should_be_ignored")
	state2.Set("key3", "new_key")

	options := &core.MergeOptions{
		Strategy: core.MergeStrategyKeepFirst,
	}

	state1.MergeWithOptions(state2, options)

	// key2 保持原值（不被覆盖）
	val2, err := state1.GetString("key2")
	require.NoError(t, err)
	assert.Equal(t, "original", val2)

	// key3 被添加（因为之前不存在）
	val3, err := state1.GetString("key3")
	require.NoError(t, err)
	assert.Equal(t, "new_key", val3)
}

// TestGraphState_MergeStrategyAppend 测试追加策略
func TestGraphState_MergeStrategyAppend(t *testing.T) {
	state1 := core.NewGraphState()
	state1.Set("items", []interface{}{"item1", "item2"})

	state2 := core.NewGraphState()
	state2.Set("items", "item3")

	state3 := core.NewGraphState()
	state3.Set("items", "item4")

	options := &core.MergeOptions{
		Strategy: core.MergeStrategyAppend,
	}

	state1.MergeWithOptions(state2, options)
	state1.MergeWithOptions(state3, options)

	items, ok := state1.Get("items")
	require.True(t, ok)

	itemsSlice, ok := items.([]interface{})
	require.True(t, ok)
	assert.Len(t, itemsSlice, 4)
	assert.Equal(t, "item1", itemsSlice[0])
	assert.Equal(t, "item2", itemsSlice[1])
	assert.Equal(t, "item3", itemsSlice[2])
	assert.Equal(t, "item4", itemsSlice[3])
}

// TestGraphState_CustomReducer 测试自定义 Reducer
func TestGraphState_CustomReducer(t *testing.T) {
	state1 := core.NewGraphState()
	state1.Set("count", 10)
	state1.Set("name", "alice")

	state2 := core.NewGraphState()
	state2.Set("count", 5)
	state2.Set("name", "bob")

	// 自定义 reducer：count 累加，name 拼接
	options := &core.MergeOptions{
		Strategy: core.MergeStrategyOverwrite, // 默认策略
		KeyReducers: map[string]core.ReducerFunc{
			"count": func(key string, existing, incoming interface{}) interface{} {
				e, _ := existing.(int)
				i, _ := incoming.(int)
				return e + i
			},
			"name": func(key string, existing, incoming interface{}) interface{} {
				e, _ := existing.(string)
				i, _ := incoming.(string)
				return e + "," + i
			},
		},
	}

	state1.MergeWithOptions(state2, options)

	count, err := state1.GetInt("count")
	require.NoError(t, err)
	assert.Equal(t, 15, count) // 10 + 5

	name, err := state1.GetString("name")
	require.NoError(t, err)
	assert.Equal(t, "alice,bob", name)
}

// TestGraph_ConcurrentMergeOverwrite 测试并发执行中的覆盖策略
func TestGraph_ConcurrentMergeOverwrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeConcurrent,
		MaxIterations: 10,
		MergeOptions: &core.MergeOptions{
			Strategy: core.MergeStrategyOverwrite,
		},
	}
	graph := core.NewGraph("start", config)

	// start 节点
	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("initialized", true)
		return state, nil
	})

	// 并行节点：node1 和 node2
	graph.AddNode("node1", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("result", "from_node1")
		state.Set("node1_data", "data1")
		return state, nil
	})

	graph.AddNode("node2", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("result", "from_node2")
		state.Set("node2_data", "data2")
		return state, nil
	})

	graph.AddEdge("start", "node1")
	graph.AddEdge("start", "node2")
	graph.AddEdge("node1", core.END)
	graph.AddEdge("node2", core.END)

	err := graph.Compile()
	require.NoError(t, err)

	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证初始化标志
	initialized, err := finalState.GetBool("initialized")
	require.NoError(t, err)
	assert.True(t, initialized)

	// result 字段被其中一个节点覆盖（不确定是哪个，因为并发）
	result, err := finalState.GetString("result")
	require.NoError(t, err)
	assert.Contains(t, []string{"from_node1", "from_node2"}, result)

	// 两个节点的独立数据都应该存在
	node1Data, err := finalState.GetString("node1_data")
	require.NoError(t, err)
	assert.Equal(t, "data1", node1Data)

	node2Data, err := finalState.GetString("node2_data")
	require.NoError(t, err)
	assert.Equal(t, "data2", node2Data)
}

// TestGraph_ConcurrentMergeAppend 测试并发执行中的追加策略
func TestGraph_ConcurrentMergeAppend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeConcurrent,
		MaxIterations: 10,
		MergeOptions: &core.MergeOptions{
			Strategy: core.MergeStrategyAppend,
		},
	}
	graph := core.NewGraph("start", config)

	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		return state, nil
	})

	// 三个并行节点，每个生成一个结果
	graph.AddNode("worker1", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("results", "result1")
		return state, nil
	})

	graph.AddNode("worker2", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("results", "result2")
		return state, nil
	})

	graph.AddNode("worker3", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("results", "result3")
		return state, nil
	})

	graph.AddEdge("start", "worker1")
	graph.AddEdge("start", "worker2")
	graph.AddEdge("start", "worker3")
	graph.AddEdge("worker1", core.END)
	graph.AddEdge("worker2", core.END)
	graph.AddEdge("worker3", core.END)

	err := graph.Compile()
	require.NoError(t, err)

	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err)

	// results 应该是包含三个结果的切片
	results, ok := finalState.Get("results")
	require.True(t, ok)

	resultsSlice, ok := results.([]interface{})
	require.True(t, ok)
	assert.Len(t, resultsSlice, 3)

	// 验证所有结果都在切片中（顺序不确定）
	resultsSet := make(map[string]bool)
	for _, r := range resultsSlice {
		resultsSet[r.(string)] = true
	}
	assert.True(t, resultsSet["result1"])
	assert.True(t, resultsSet["result2"])
	assert.True(t, resultsSet["result3"])
}
