package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGraph_Stream_Basic 测试基础流式输出
func TestGraph_Stream_Basic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	// 添加节点
	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("count", 0)
		return state, nil
	})

	graph.AddNode("increment", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		count, _ := state.GetInt("count")
		state.Set("count", count+1)
		return state, nil
	})

	graph.AddNode("double", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		count, _ := state.GetInt("count")
		state.Set("count", count*2)
		return state, nil
	})

	// 添加边
	graph.AddEdge("start", "increment")
	graph.AddEdge("increment", "double")
	graph.AddEdge("double", core.END)

	err := graph.Compile()
	require.NoError(t, err)

	// 执行流式输出
	input := core.NewGraphState()
	eventChan, err := graph.Stream(ctx, *input)
	require.NoError(t, err)

	// 收集事件
	events := []*core.StreamEvent{}
	for event := range eventChan {
		events = append(events, event)
	}

	// 验证事件序列
	require.NotEmpty(t, events)

	// 应该有：start 开始 -> start 结束 -> increment 开始 -> increment 结束 -> double 开始 -> double 结束 -> 最终状态
	nodeStartCount := 0
	nodeEndCount := 0
	finalStateCount := 0

	for _, event := range events {
		switch event.Type {
		case core.StreamEventTypeNodeStart:
			nodeStartCount++
		case core.StreamEventTypeNodeEnd:
			nodeEndCount++
		case core.StreamEventTypeState:
			data := event.Data.(map[string]any)
			if data["final"] == true {
				finalStateCount++
				// 验证最终结果：(0 + 1) * 2 = 2
				state := data["state"].(core.GraphState)
				count, err := state.GetInt("count")
				require.NoError(t, err)
				assert.Equal(t, 2, count)
			}
		}
	}

	assert.Equal(t, 3, nodeStartCount, "应该有 3 个节点开始事件")
	assert.Equal(t, 3, nodeEndCount, "应该有 3 个节点结束事件")
	assert.Equal(t, 1, finalStateCount, "应该有 1 个最终状态事件")
}

// TestGraph_Stream_WithError 测试错误处理
func TestGraph_Stream_WithError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	// 添加节点
	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("value", "ok")
		return state, nil
	})

	graph.AddNode("failing_node", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		return state, assert.AnError
	})

	// 添加边
	graph.AddEdge("start", "failing_node")
	graph.AddEdge("failing_node", core.END)

	err := graph.Compile()
	require.NoError(t, err)

	// 执行流式输出
	input := core.NewGraphState()
	eventChan, err := graph.Stream(ctx, *input)
	require.NoError(t, err)

	// 收集事件
	events := []*core.StreamEvent{}
	for event := range eventChan {
		events = append(events, event)
	}

	// 验证有错误事件
	hasError := false
	for _, event := range events {
		if event.Type == core.StreamEventTypeError {
			hasError = true
			data := event.Data.(map[string]any)
			errorMsg := data["error"].(string)
			// 验证错误信息包含节点名或错误本身
			assert.True(t, len(errorMsg) > 0, "错误消息不应为空")
		}
	}

	assert.True(t, hasError, "应该有错误事件")
}

// TestGraph_Stream_EventOrder 测试事件顺序
func TestGraph_Stream_EventOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("a", config)

	// 添加节点
	graph.AddNode("a", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("order", []string{"a"})
		return state, nil
	})

	graph.AddNode("b", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		order, _ := state.Get("order")
		orderSlice := order.([]string)
		orderSlice = append(orderSlice, "b")
		state.Set("order", orderSlice)
		return state, nil
	})

	graph.AddNode("c", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		order, _ := state.Get("order")
		orderSlice := order.([]string)
		orderSlice = append(orderSlice, "c")
		state.Set("order", orderSlice)
		return state, nil
	})

	// 添加边
	graph.AddEdge("a", "b")
	graph.AddEdge("b", "c")
	graph.AddEdge("c", core.END)

	err := graph.Compile()
	require.NoError(t, err)

	// 执行流式输出
	input := core.NewGraphState()
	eventChan, err := graph.Stream(ctx, *input)
	require.NoError(t, err)

	// 收集节点事件
	nodeEvents := []string{}
	for event := range eventChan {
		if event.Type == core.StreamEventTypeNodeStart {
			data := event.Data.(map[string]any)
			nodeEvents = append(nodeEvents, data["node"].(string)+"_start")
		} else if event.Type == core.StreamEventTypeNodeEnd {
			data := event.Data.(map[string]any)
			nodeEvents = append(nodeEvents, data["node"].(string)+"_end")
		}
	}

	// 验证事件顺序
	expectedOrder := []string{
		"a_start", "a_end",
		"b_start", "b_end",
		"c_start", "c_end",
	}

	assert.Equal(t, expectedOrder, nodeEvents, "事件顺序应该正确")
}

// TestGraph_Stream_ConditionalRouting 测试条件路由的流式输出
func TestGraph_Stream_ConditionalRouting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	// 添加节点
	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("value", 10)
		return state, nil
	})

	graph.AddNode("positive", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("result", "positive")
		return state, nil
	})

	graph.AddNode("negative", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("result", "negative")
		return state, nil
	})

	// 添加条件路由
	graph.AddConditionalEdge("start", func(ctx context.Context, state core.GraphState) (string, error) {
		value, _ := state.GetInt("value")
		if value > 0 {
			return "positive", nil
		}
		return "negative", nil
	}, map[string]string{
		"positive": "positive",
		"negative": "negative",
	})

	graph.AddEdge("positive", core.END)
	graph.AddEdge("negative", core.END)

	err := graph.Compile()
	require.NoError(t, err)

	// 执行流式输出
	input := core.NewGraphState()
	eventChan, err := graph.Stream(ctx, *input)
	require.NoError(t, err)

	// 收集节点名称
	visitedNodes := []string{}
	for event := range eventChan {
		if event.Type == core.StreamEventTypeNodeStart {
			data := event.Data.(map[string]any)
			visitedNodes = append(visitedNodes, data["node"].(string))
		}
	}

	// 验证路由到了 positive 节点
	assert.Contains(t, visitedNodes, "start")
	assert.Contains(t, visitedNodes, "positive")
	assert.NotContains(t, visitedNodes, "negative")
}
