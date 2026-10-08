package metrics

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

func TestMetricsCollector_Update(t *testing.T) {
	ctx := context.Background()
	graph := explorationgraph.NewMemoryStore()
	taskID := "test-task"
	logger := zerolog.New(zerolog.NewTestWriter(t))

	collector := NewMetricsCollector(graph, taskID, logger)

	// 创建一些 actions
	for i := 0; i < 5; i++ {
		action := explorationgraph.Node{
			ID:      "action-" + string(rune(i)),
			TaskID:  taskID,
			Kind:    core.KindAction,
			Content: json.RawMessage(`{}`),
		}
		_, err := graph.CreateNode(ctx, action)
		require.NoError(t, err)
	}

	// 创建一些 results
	for i := 0; i < 3; i++ {
		result := explorationgraph.Node{
			ID:      "result-" + string(rune(i)),
			TaskID:  taskID,
			Kind:    core.KindResult,
			Content: json.RawMessage(`{}`),
		}
		_, err := graph.CreateNode(ctx, result)
		require.NoError(t, err)
	}

	// 更新指标
	err := collector.Update(ctx)
	require.NoError(t, err)

	// 获取指标
	metrics := collector.GetMetrics()

	// 验证指标
	assert.Equal(t, 5.0, metrics.ActionRatePerHour, "应该有 5 actions/hour")
	assert.Equal(t, 3.0, metrics.ResultRatePerHour, "应该有 3 results/hour")
	assert.Greater(t, metrics.AvgResultActionRatio, 0.0, "比率应该大于 0")

	t.Logf("指标: %+v", metrics)
}

func TestMetricsCollector_IncrementalUpdate(t *testing.T) {
	ctx := context.Background()
	graph := explorationgraph.NewMemoryStore()
	taskID := "test-task"
	logger := zerolog.New(zerolog.NewTestWriter(t))

	collector := NewMetricsCollector(graph, taskID, logger)

	// 第一次更新：3 个 actions
	for i := 0; i < 3; i++ {
		action := explorationgraph.Node{
			ID:      "action-1-" + string(rune(i)),
			TaskID:  taskID,
			Kind:    core.KindAction,
			Content: json.RawMessage(`{}`),
		}
		_, err := graph.CreateNode(ctx, action)
		require.NoError(t, err)
	}

	err := collector.Update(ctx)
	require.NoError(t, err)

	metrics1 := collector.GetMetrics()
	assert.Equal(t, 3.0, metrics1.ActionRatePerHour)

	// 第二次更新：再添加 2 个 actions
	time.Sleep(100 * time.Millisecond) // 确保时间戳不同
	for i := 0; i < 2; i++ {
		action := explorationgraph.Node{
			ID:      "action-2-" + string(rune(i)),
			TaskID:  taskID,
			Kind:    core.KindAction,
			Content: json.RawMessage(`{}`),
		}
		_, err := graph.CreateNode(ctx, action)
		require.NoError(t, err)
	}

	err = collector.Update(ctx)
	require.NoError(t, err)

	metrics2 := collector.GetMetrics()
	assert.Equal(t, 5.0, metrics2.ActionRatePerHour, "应该累计到 5 actions/hour")

	t.Logf("第一次: %+v", metrics1)
	t.Logf("第二次: %+v", metrics2)
}

func TestMetricsCollector_EmptyGraph(t *testing.T) {
	ctx := context.Background()
	graph := explorationgraph.NewMemoryStore()
	taskID := "test-task"
	logger := zerolog.New(zerolog.NewTestWriter(t))

	collector := NewMetricsCollector(graph, taskID, logger)

	// 空图
	err := collector.Update(ctx)
	require.NoError(t, err)

	metrics := collector.GetMetrics()

	// 验证空指标
	assert.Equal(t, 0.0, metrics.ActionRatePerHour)
	assert.Equal(t, 0.0, metrics.ResultRatePerHour)
	assert.Equal(t, 0.0, metrics.AvgResultActionRatio)
}

func TestMetricsCollector_Ratio(t *testing.T) {
	ctx := context.Background()
	graph := explorationgraph.NewMemoryStore()
	taskID := "test-task"
	logger := zerolog.New(zerolog.NewTestWriter(t))

	collector := NewMetricsCollector(graph, taskID, logger)

	// 创建 10 个 actions
	for i := 0; i < 10; i++ {
		action := explorationgraph.Node{
			ID:      "action-" + string(rune(i)),
			TaskID:  taskID,
			Kind:    core.KindAction,
			Content: json.RawMessage(`{}`),
		}
		_, err := graph.CreateNode(ctx, action)
		require.NoError(t, err)
	}

	// 创建 1 个 result
	result := explorationgraph.Node{
		ID:      "result-1",
		TaskID:  taskID,
		Kind:    core.KindResult,
		Content: json.RawMessage(`{}`),
	}
	_, err := graph.CreateNode(ctx, result)
	require.NoError(t, err)

	// 更新指标
	err = collector.Update(ctx)
	require.NoError(t, err)

	metrics := collector.GetMetrics()

	// 验证比率 = 1/10 = 0.1
	assert.InDelta(t, 0.1, metrics.AvgResultActionRatio, 0.01, "比率应该约为 0.1")

	t.Logf("指标: %+v", metrics)
}
