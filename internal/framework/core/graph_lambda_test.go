package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGraph_Lambda_Basic 测试基础 Lambda 节点
func TestGraph_Lambda_Basic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	// 普通节点
	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("value", 10)
		return state, nil
	})

	// Lambda 节点：简单转换
	err := graph.AddLambda("double", func(state core.GraphState) core.GraphState {
		val, _ := state.GetInt("value")
		state.Set("value", val*2)
		return state
	})
	require.NoError(t, err)

	// 另一个 Lambda 节点
	err = graph.AddLambda("add_five", func(state core.GraphState) core.GraphState {
		val, _ := state.GetInt("value")
		state.Set("value", val+5)
		return state
	})
	require.NoError(t, err)

	graph.AddEdge("start", "double")
	graph.AddEdge("double", "add_five")
	graph.AddEdge("add_five", core.END)

	err = graph.Compile()
	require.NoError(t, err)

	// 执行
	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证结果：(10 * 2) + 5 = 25
	result, err := finalState.GetInt("value")
	require.NoError(t, err)
	assert.Equal(t, 25, result)
}

// TestGraph_Lambda_DataTransform 测试数据转换场景
func TestGraph_Lambda_DataTransform(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("input", config)

	// 输入节点
	graph.AddNode("input", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("raw_data", "hello world")
		return state, nil
	})

	// Lambda: 转大写
	err := graph.AddLambda("uppercase", func(state core.GraphState) core.GraphState {
		raw, _ := state.GetString("raw_data")
		state.Set("processed", raw)
		return state
	})
	require.NoError(t, err)

	// Lambda: 提取长度
	err = graph.AddLambda("extract_length", func(state core.GraphState) core.GraphState {
		processed, _ := state.GetString("processed")
		state.Set("length", len(processed))
		return state
	})
	require.NoError(t, err)

	// Lambda: 判断是否长字符串
	err = graph.AddLambda("classify", func(state core.GraphState) core.GraphState {
		length, _ := state.GetInt("length")
		if length > 10 {
			state.Set("category", "long")
		} else {
			state.Set("category", "short")
		}
		return state
	})
	require.NoError(t, err)

	graph.AddEdge("input", "uppercase")
	graph.AddEdge("uppercase", "extract_length")
	graph.AddEdge("extract_length", "classify")
	graph.AddEdge("classify", core.END)

	err = graph.Compile()
	require.NoError(t, err)

	// 执行
	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证结果
	category, err := finalState.GetString("category")
	require.NoError(t, err)
	assert.Equal(t, "long", category)

	length, err := finalState.GetInt("length")
	require.NoError(t, err)
	assert.Equal(t, 11, length)
}

// TestGraph_Lambda_MixedWithNodes 测试 Lambda 与普通节点混合
func TestGraph_Lambda_MixedWithNodes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	executionLog := []string{}

	// 普通节点
	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		executionLog = append(executionLog, "node:start")
		state.Set("count", 0)
		return state, nil
	})

	// Lambda 节点
	err := graph.AddLambda("increment", func(state core.GraphState) core.GraphState {
		executionLog = append(executionLog, "lambda:increment")
		count, _ := state.GetInt("count")
		state.Set("count", count+1)
		return state
	})
	require.NoError(t, err)

	// 普通节点
	graph.AddNode("process", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		executionLog = append(executionLog, "node:process")
		count, _ := state.GetInt("count")
		state.Set("count", count*10)
		return state, nil
	})

	// Lambda 节点
	err = graph.AddLambda("finalize", func(state core.GraphState) core.GraphState {
		executionLog = append(executionLog, "lambda:finalize")
		count, _ := state.GetInt("count")
		state.Set("final", count+100)
		return state
	})
	require.NoError(t, err)

	graph.AddEdge("start", "increment")
	graph.AddEdge("increment", "process")
	graph.AddEdge("process", "finalize")
	graph.AddEdge("finalize", core.END)

	err = graph.Compile()
	require.NoError(t, err)

	// 执行
	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证执行顺序
	assert.Equal(t, []string{
		"node:start",
		"lambda:increment",
		"node:process",
		"lambda:finalize",
	}, executionLog)

	// 验证结果：((0 + 1) * 10) + 100 = 110
	result, err := finalState.GetInt("final")
	require.NoError(t, err)
	assert.Equal(t, 110, result)
}

// TestGraph_Lambda_ChainTransform 测试链式转换
func TestGraph_Lambda_ChainTransform(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	// 初始化
	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("x", 1)
		return state, nil
	})

	// 链式 Lambda 转换
	transforms := []struct {
		name string
		fn   func(core.GraphState) core.GraphState
	}{
		{"add2", func(s core.GraphState) core.GraphState {
			x, _ := s.GetInt("x")
			s.Set("x", x+2)
			return s
		}},
		{"mul3", func(s core.GraphState) core.GraphState {
			x, _ := s.GetInt("x")
			s.Set("x", x*3)
			return s
		}},
		{"sub1", func(s core.GraphState) core.GraphState {
			x, _ := s.GetInt("x")
			s.Set("x", x-1)
			return s
		}},
		{"div2", func(s core.GraphState) core.GraphState {
			x, _ := s.GetInt("x")
			s.Set("x", x/2)
			return s
		}},
	}

	prev := "start"
	for _, transform := range transforms {
		err := graph.AddLambda(transform.name, transform.fn)
		require.NoError(t, err)
		graph.AddEdge(prev, transform.name)
		prev = transform.name
	}
	graph.AddEdge(prev, core.END)

	err := graph.Compile()
	require.NoError(t, err)

	// 执行
	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证结果：((1 + 2) * 3 - 1) / 2 = 4
	result, err := finalState.GetInt("x")
	require.NoError(t, err)
	assert.Equal(t, 4, result)
}

// TestGraph_Lambda_StateEnrichment 测试状态增强
func TestGraph_Lambda_StateEnrichment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	// 初始数据
	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("user_id", 12345)
		return state, nil
	})

	// Lambda: 添加用户名
	err := graph.AddLambda("add_username", func(state core.GraphState) core.GraphState {
		userID, _ := state.GetInt("user_id")
		state.Set("username", "user_"+string(rune(userID)))
		return state
	})
	require.NoError(t, err)

	// Lambda: 添加时间戳
	err = graph.AddLambda("add_timestamp", func(state core.GraphState) core.GraphState {
		state.Set("timestamp", time.Now().Unix())
		return state
	})
	require.NoError(t, err)

	// Lambda: 添加标签
	err = graph.AddLambda("add_tags", func(state core.GraphState) core.GraphState {
		state.Set("tags", []string{"active", "verified"})
		return state
	})
	require.NoError(t, err)

	graph.AddEdge("start", "add_username")
	graph.AddEdge("add_username", "add_timestamp")
	graph.AddEdge("add_timestamp", "add_tags")
	graph.AddEdge("add_tags", core.END)

	err = graph.Compile()
	require.NoError(t, err)

	// 执行
	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证所有字段都被添加
	userID, err := finalState.GetInt("user_id")
	require.NoError(t, err)
	assert.Equal(t, 12345, userID)

	username, ok := finalState.Get("username")
	require.True(t, ok)
	assert.NotEmpty(t, username)

	timestamp, ok := finalState.Get("timestamp")
	require.True(t, ok)
	assert.NotZero(t, timestamp)

	tags, ok := finalState.Get("tags")
	require.True(t, ok)
	assert.NotEmpty(t, tags)
}

// TestGraph_Lambda_FilterPattern 测试过滤模式
func TestGraph_Lambda_FilterPattern(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	// 准备数据
	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("items", []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
		return state, nil
	})

	// Lambda: 过滤偶数
	err := graph.AddLambda("filter_even", func(state core.GraphState) core.GraphState {
		items, _ := state.Get("items")
		itemsSlice := items.([]int)

		var evens []int
		for _, item := range itemsSlice {
			if item%2 == 0 {
				evens = append(evens, item)
			}
		}

		state.Set("evens", evens)
		return state
	})
	require.NoError(t, err)

	// Lambda: 计算总和
	err = graph.AddLambda("sum", func(state core.GraphState) core.GraphState {
		evens, _ := state.Get("evens")
		evensSlice := evens.([]int)

		sum := 0
		for _, val := range evensSlice {
			sum += val
		}

		state.Set("sum", sum)
		return state
	})
	require.NoError(t, err)

	graph.AddEdge("start", "filter_even")
	graph.AddEdge("filter_even", "sum")
	graph.AddEdge("sum", core.END)

	err = graph.Compile()
	require.NoError(t, err)

	// 执行
	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证结果：2 + 4 + 6 + 8 + 10 = 30
	sum, err := finalState.GetInt("sum")
	require.NoError(t, err)
	assert.Equal(t, 30, sum)
}
