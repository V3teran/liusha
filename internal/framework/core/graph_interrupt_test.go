package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGraph_InterruptBefore 测试节点执行前中断
func TestGraph_InterruptBefore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	executionLog := []string{}

	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		executionLog = append(executionLog, "start")
		state.Set("value", 1)
		return state, nil
	})

	graph.AddNode("middle", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		executionLog = append(executionLog, "middle")
		val, _ := state.GetInt("value")
		state.Set("value", val+1)
		return state, nil
	})

	graph.AddNode("end", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		executionLog = append(executionLog, "end")
		return state, nil
	})

	graph.AddEdge("start", "middle")
	graph.AddEdge("middle", "end")
	graph.AddEdge("end", core.END)

	// 在 middle 节点执行前设置中断点
	err := graph.SetInterruptBefore("middle")
	require.NoError(t, err)

	err = graph.Compile()
	require.NoError(t, err)

	input := core.NewGraphState()
	_, err = graph.Run(ctx, *input)

	// 应该在 middle 之前中断
	require.Error(t, err)
	assert.True(t, core.IsInterrupt(err))

	checkpoint := core.GetInterruptCheckpoint(err)
	require.NotNil(t, checkpoint)
	assert.Equal(t, "middle", checkpoint.CurrentNode)
	assert.Equal(t, core.InterruptBefore, checkpoint.InterruptType)

	// 验证只执行了 start
	assert.Equal(t, []string{"start"}, executionLog)

	// 验证状态
	val, err := checkpoint.State.GetInt("value")
	require.NoError(t, err)
	assert.Equal(t, 1, val)
}

// TestGraph_InterruptAfter 测试节点执行后中断
func TestGraph_InterruptAfter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	executionLog := []string{}

	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		executionLog = append(executionLog, "start")
		state.Set("value", 1)
		return state, nil
	})

	graph.AddNode("middle", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		executionLog = append(executionLog, "middle")
		val, _ := state.GetInt("value")
		state.Set("value", val+1)
		return state, nil
	})

	graph.AddNode("end", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		executionLog = append(executionLog, "end")
		return state, nil
	})

	graph.AddEdge("start", "middle")
	graph.AddEdge("middle", "end")
	graph.AddEdge("end", core.END)

	// 在 middle 节点执行后设置中断点
	err := graph.SetInterruptAfter("middle")
	require.NoError(t, err)

	err = graph.Compile()
	require.NoError(t, err)

	input := core.NewGraphState()
	_, err = graph.Run(ctx, *input)

	// 应该在 middle 之后中断
	require.Error(t, err)
	assert.True(t, core.IsInterrupt(err))

	checkpoint := core.GetInterruptCheckpoint(err)
	require.NotNil(t, checkpoint)
	assert.Equal(t, "middle", checkpoint.CurrentNode)
	assert.Equal(t, core.InterruptAfter, checkpoint.InterruptType)

	// 验证执行了 start 和 middle
	assert.Equal(t, []string{"start", "middle"}, executionLog)

	// 验证状态（middle 已执行，value 应该是 2）
	val, err := checkpoint.State.GetInt("value")
	require.NoError(t, err)
	assert.Equal(t, 2, val)
}

// TestGraph_Resume 测试从中断点恢复执行
func TestGraph_Resume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	executionLog := []string{}

	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		executionLog = append(executionLog, "start")
		state.Set("value", 1)
		return state, nil
	})

	graph.AddNode("middle", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		executionLog = append(executionLog, "middle")
		val, _ := state.GetInt("value")
		state.Set("value", val+1)
		return state, nil
	})

	graph.AddNode("end", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		executionLog = append(executionLog, "end")
		val, _ := state.GetInt("value")
		state.Set("value", val+1)
		return state, nil
	})

	graph.AddEdge("start", "middle")
	graph.AddEdge("middle", "end")
	graph.AddEdge("end", core.END)

	// 在 middle 执行前中断
	err := graph.SetInterruptBefore("middle")
	require.NoError(t, err)

	err = graph.Compile()
	require.NoError(t, err)

	// 第一次运行：在 middle 前中断
	input := core.NewGraphState()
	_, err = graph.Run(ctx, *input)
	require.Error(t, err)
	assert.True(t, core.IsInterrupt(err))

	checkpoint := core.GetInterruptCheckpoint(err)
	require.NotNil(t, checkpoint)

	// 恢复执行
	finalState, err := graph.Resume(ctx, checkpoint)
	require.NoError(t, err)

	// 验证所有节点都执行了
	assert.Equal(t, []string{"start", "middle", "end"}, executionLog)

	// 验证最终状态
	val, err := finalState.GetInt("value")
	require.NoError(t, err)
	assert.Equal(t, 3, val) // 1 + 1 + 1
}

// TestGraph_ResumeInterruptAfter 测试从执行后中断点恢复
func TestGraph_ResumeInterruptAfter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	executionLog := []string{}

	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		executionLog = append(executionLog, "start")
		state.Set("value", 10)
		return state, nil
	})

	graph.AddNode("process", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		executionLog = append(executionLog, "process")
		val, _ := state.GetInt("value")
		state.Set("value", val*2)
		return state, nil
	})

	graph.AddNode("finalize", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		executionLog = append(executionLog, "finalize")
		val, _ := state.GetInt("value")
		state.Set("result", val+5)
		return state, nil
	})

	graph.AddEdge("start", "process")
	graph.AddEdge("process", "finalize")
	graph.AddEdge("finalize", core.END)

	// 在 process 执行后中断
	err := graph.SetInterruptAfter("process")
	require.NoError(t, err)

	err = graph.Compile()
	require.NoError(t, err)

	// 第一次运行：在 process 后中断
	input := core.NewGraphState()
	_, err = graph.Run(ctx, *input)
	require.Error(t, err)
	assert.True(t, core.IsInterrupt(err))

	checkpoint := core.GetInterruptCheckpoint(err)
	require.NotNil(t, checkpoint)
	assert.Equal(t, core.InterruptAfter, checkpoint.InterruptType)

	// 验证 process 已执行
	val, err := checkpoint.State.GetInt("value")
	require.NoError(t, err)
	assert.Equal(t, 20, val) // 10 * 2

	// 恢复执行
	finalState, err := graph.Resume(ctx, checkpoint)
	require.NoError(t, err)

	// 验证所有节点都执行了
	assert.Equal(t, []string{"start", "process", "finalize"}, executionLog)

	// 验证最终结果
	result, err := finalState.GetInt("result")
	require.NoError(t, err)
	assert.Equal(t, 25, result) // 20 + 5
}

// TestGraph_MultipleInterrupts 测试多个中断点
func TestGraph_MultipleInterrupts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential,
		MaxIterations: 10,
	}
	graph := core.NewGraph("start", config)

	executionLog := []string{}

	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		executionLog = append(executionLog, "start")
		return state, nil
	})

	graph.AddNode("step1", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		executionLog = append(executionLog, "step1")
		return state, nil
	})

	graph.AddNode("step2", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		executionLog = append(executionLog, "step2")
		return state, nil
	})

	graph.AddNode("step3", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		executionLog = append(executionLog, "step3")
		return state, nil
	})

	graph.AddEdge("start", "step1")
	graph.AddEdge("step1", "step2")
	graph.AddEdge("step2", "step3")
	graph.AddEdge("step3", core.END)

	// 在 step1 和 step3 前设置中断点
	err := graph.SetInterruptBefore("step1", "step3")
	require.NoError(t, err)

	err = graph.Compile()
	require.NoError(t, err)

	// 第一次运行：在 step1 前中断
	input := core.NewGraphState()
	_, err = graph.Run(ctx, *input)
	require.Error(t, err)
	checkpoint1 := core.GetInterruptCheckpoint(err)
	require.NotNil(t, checkpoint1)
	assert.Equal(t, "step1", checkpoint1.CurrentNode)
	assert.Equal(t, []string{"start"}, executionLog)

	// 第一次恢复：在 step3 前中断
	_, err = graph.Resume(ctx, checkpoint1)
	require.Error(t, err)
	checkpoint2 := core.GetInterruptCheckpoint(err)
	require.NotNil(t, checkpoint2)
	assert.Equal(t, "step3", checkpoint2.CurrentNode)
	assert.Equal(t, []string{"start", "step1", "step2"}, executionLog)

	// 第二次恢复：完成执行
	finalState, err := graph.Resume(ctx, checkpoint2)
	require.NoError(t, err)
	assert.Equal(t, []string{"start", "step1", "step2", "step3"}, executionLog)
	assert.NotNil(t, finalState)
}
