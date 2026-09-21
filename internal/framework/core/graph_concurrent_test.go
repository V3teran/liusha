package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGraph_ConcurrentWithStream 测试并发模式 + Stream
func TestGraph_ConcurrentWithStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeConcurrent,
		MaxIterations: 10,
	}

	graph := core.NewGraph("start", config)

	// 添加并行节点
	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("start", true)
		return state, nil
	})

	graph.AddNode("parallel1", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		time.Sleep(50 * time.Millisecond)
		state.Set("p1", "done")
		return state, nil
	})

	graph.AddNode("parallel2", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		time.Sleep(50 * time.Millisecond)
		state.Set("p2", "done")
		return state, nil
	})

	graph.AddNode("end", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("end", true)
		return state, nil
	})

	// start → parallel1, parallel2 (并行)
	graph.AddEdge("start", "parallel1")
	graph.AddEdge("start", "parallel2")

	// parallel1, parallel2 → end
	graph.AddEdge("parallel1", "end")
	graph.AddEdge("parallel2", "end")
	graph.AddEdge("end", core.END)

	err := graph.Compile()
	require.NoError(t, err)

	// 执行 Stream
	input := core.NewGraphState()
	eventChan, err := graph.Stream(ctx, *input)
	require.NoError(t, err)

	// 收集事件
	var events []*core.StreamEvent
	for event := range eventChan {
		events = append(events, event)
	}

	// 验证事件
	assert.NotEmpty(t, events)

	// 应该有 NodeStart 和 NodeEnd 事件
	foundNodeStart := false
	foundNodeEnd := false
	for _, event := range events {
		if event.Type == core.StreamEventTypeNodeStart {
			foundNodeStart = true
		}
		if event.Type == core.StreamEventTypeNodeEnd {
			foundNodeEnd = true
		}
	}

	assert.True(t, foundNodeStart, "应该有 NodeStart 事件")
	assert.True(t, foundNodeEnd, "应该有 NodeEnd 事件")
}

// TestGraph_ConcurrentWithSend 测试并发模式 + Send
func TestGraph_ConcurrentWithSend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeConcurrent,
		MaxIterations: 10,
	}

	graph := core.NewGraph("start", config)

	// 添加节点
	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("start", true)

		// 动态发送消息到 worker
		workerState := core.NewGraphState()
		workerState.Set("task", "dynamic_task")
		graph.Send("worker", *workerState)

		return state, nil
	})

	graph.AddNode("worker", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("worker", "done")
		return state, nil
	})

	graph.AddNode("end", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("end", true)
		return state, nil
	})

	graph.AddEdge("start", "end")
	graph.AddEdge("end", core.END)

	err := graph.Compile()
	require.NoError(t, err)

	// 执行
	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证状态
	start, _ := finalState.GetBool("start")
	assert.True(t, start)

	end, _ := finalState.GetBool("end")
	assert.True(t, end)
}

// TestGraph_ConcurrentParallelExecution 测试并发执行的并行性
func TestGraph_ConcurrentParallelExecution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeConcurrent,
		MaxIterations: 10,
	}

	graph := core.NewGraph("start", config)

	startTime := time.Now()

	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("start_time", time.Now().UnixNano())
		return state, nil
	})

	// 三个并行节点，每个耗时 200ms
	graph.AddNode("task1", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		time.Sleep(200 * time.Millisecond)
		state.Set("task1", time.Now().UnixNano())
		return state, nil
	})

	graph.AddNode("task2", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		time.Sleep(200 * time.Millisecond)
		state.Set("task2", time.Now().UnixNano())
		return state, nil
	})

	graph.AddNode("task3", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		time.Sleep(200 * time.Millisecond)
		state.Set("task3", time.Now().UnixNano())
		return state, nil
	})

	graph.AddNode("end", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("end_time", time.Now().UnixNano())
		return state, nil
	})

	graph.AddEdge("start", "task1")
	graph.AddEdge("start", "task2")
	graph.AddEdge("start", "task3")
	graph.AddEdge("task1", "end")
	graph.AddEdge("task2", "end")
	graph.AddEdge("task3", "end")
	graph.AddEdge("end", core.END)

	err := graph.Compile()
	require.NoError(t, err)

	input := core.NewGraphState()
	_, err = graph.Run(ctx, *input)
	require.NoError(t, err)

	elapsed := time.Since(startTime)

	// 如果是并行执行，总时间应该接近 200ms（而非 600ms）
	// 留一些余量（300ms）给调度和执行开销
	assert.Less(t, elapsed, 500*time.Millisecond, "并行执行应该比串行快")
}
