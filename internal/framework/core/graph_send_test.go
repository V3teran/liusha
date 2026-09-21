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

// TestGraph_Send_Basic 测试基础 Send 功能
func TestGraph_Send_Basic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	var graphRef core.Graph = graph

	// 添加节点
	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("main", "executed")

		// 动态发送消息到 worker 节点
		workerState := core.NewGraphState()
		workerState.Set("task", "dynamic_task")
		err := graphRef.Send("worker", *workerState)
		if err != nil {
			return state, err
		}

		return state, nil
	})

	graph.AddNode("worker", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		task, _ := state.GetString("task")
		state.Set("worker_result", fmt.Sprintf("processed_%s", task))
		return state, nil
	})

	graph.AddEdge("start", core.END)

	err := graph.Compile()
	require.NoError(t, err)

	// 执行
	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证主流程执行
	main, err := finalState.GetString("main")
	require.NoError(t, err)
	assert.Equal(t, "executed", main)

	// 验证 Send 触发的 worker 执行
	workerResult, err := finalState.GetString("worker_result")
	require.NoError(t, err)
	assert.Equal(t, "processed_dynamic_task", workerResult)
}

// TestGraph_Send_FanOut 测试 Fan-out 模式
func TestGraph_Send_FanOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	var graphRef core.Graph = graph

	// 添加节点
	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		// Fan-out: 发送多个任务到 worker
		for i := 1; i <= 3; i++ {
			workerState := core.NewGraphState()
			workerState.Set("task_id", i)
			err := graphRef.Send("worker", *workerState)
			if err != nil {
				return state, err
			}
		}

		state.Set("fanout_count", 3)
		return state, nil
	})

	resultsMu := sync.Mutex{}
	results := []int{}

	graph.AddNode("worker", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		taskID, _ := state.GetInt("task_id")

		// 收集结果
		resultsMu.Lock()
		results = append(results, taskID)
		resultsMu.Unlock()

		state.Set(fmt.Sprintf("task_%d_result", taskID), taskID*10)
		return state, nil
	})

	graph.AddEdge("start", core.END)

	err := graph.Compile()
	require.NoError(t, err)

	// 执行
	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证 Fan-out 执行
	fanoutCount, err := finalState.GetInt("fanout_count")
	require.NoError(t, err)
	assert.Equal(t, 3, fanoutCount)

	// 验证所有 worker 都执行了
	assert.Len(t, results, 3)
	assert.Contains(t, results, 1)
	assert.Contains(t, results, 2)
	assert.Contains(t, results, 3)

	// 验证每个任务的结果
	for i := 1; i <= 3; i++ {
		result, err := finalState.GetInt(fmt.Sprintf("task_%d_result", i))
		require.NoError(t, err)
		assert.Equal(t, i*10, result)
	}
}

// TestGraph_Send_MapReduce 测试 Map-Reduce 模式
func TestGraph_Send_MapReduce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	var graphRef core.Graph = graph

	// Map 阶段：分发数据
	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		data := []int{1, 2, 3, 4, 5}
		state.Set("original_data", data)

		// Map: 发送每个元素到 mapper
		for _, val := range data {
			mapState := core.NewGraphState()
			mapState.Set("value", val)
			err := graphRef.Send("mapper", *mapState)
			if err != nil {
				return state, err
			}
		}

		return state, nil
	})

	// Mapper: 处理单个元素
	graph.AddNode("mapper", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		val, _ := state.GetInt("value")
		// 平方
		state.Set("mapped_value", val*val)
		return state, nil
	})

	graph.AddEdge("start", core.END)

	err := graph.Compile()
	require.NoError(t, err)

	// 执行
	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证原始数据
	originalData, ok := finalState.Get("original_data")
	require.True(t, ok)
	assert.Equal(t, []int{1, 2, 3, 4, 5}, originalData)

	// 验证 mapped 值存在
	mappedValue, ok := finalState.Get("mapped_value")
	require.True(t, ok)
	assert.NotNil(t, mappedValue)
}

// TestGraph_Send_BeforeCompile 测试编译前 Send 应该失败
func TestGraph_Send_BeforeCompile(t *testing.T) {
	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		return state, nil
	})

	graph.AddNode("worker", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		return state, nil
	})

	// 未编译时 Send 应该失败
	state := core.NewGraphState()
	err := graph.Send("worker", *state)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not compiled")
}

// TestGraph_Send_NonExistentNode 测试发送到不存在的节点
func TestGraph_Send_NonExistentNode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	var graphRef core.Graph = graph

	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		workerState := core.NewGraphState()
		// 发送到不存在的节点
		err := graphRef.Send("non_existent", *workerState)
		return state, err
	})

	graph.AddEdge("start", core.END)

	err := graph.Compile()
	require.NoError(t, err)

	// 执行应该失败
	input := core.NewGraphState()
	_, err = graph.Run(ctx, *input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestGraph_Send_OutsideExecution 测试在执行外 Send 应该失败
func TestGraph_Send_OutsideExecution(t *testing.T) {
	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		return state, nil
	})

	graph.AddNode("worker", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		return state, nil
	})

	graph.AddEdge("start", core.END)

	err := graph.Compile()
	require.NoError(t, err)

	// 在执行外 Send 应该失败
	state := core.NewGraphState()
	err = graph.Send("worker", *state)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "during graph execution")
}
