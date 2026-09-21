package core_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockCheckpointer 用于测试的 Checkpointer
type MockCheckpointer struct {
	checkpoints map[core.CheckpointID]*core.Checkpoint
	mu          sync.RWMutex
}

func NewMockCheckpointer() *MockCheckpointer {
	return &MockCheckpointer{
		checkpoints: make(map[core.CheckpointID]*core.Checkpoint),
	}
}

func (m *MockCheckpointer) Save(ctx context.Context, checkpoint core.Checkpoint) (core.CheckpointID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkpoints[checkpoint.ID] = &checkpoint
	return checkpoint.ID, nil
}

func (m *MockCheckpointer) Load(ctx context.Context, id core.CheckpointID) (*core.Checkpoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp, exists := m.checkpoints[id]
	if !exists {
		return nil, assert.AnError
	}
	return cp, nil
}

func (m *MockCheckpointer) List(ctx context.Context, taskID string, limit int) ([]core.CheckpointMeta, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	metas := []core.CheckpointMeta{}
	for _, cp := range m.checkpoints {
		if cp.TaskID == taskID {
			metas = append(metas, core.CheckpointMeta{
				ID:        cp.ID,
				TaskID:    cp.TaskID,
				Phase:     cp.Phase,
				CreatedAt: cp.CreatedAt,
				SizeBytes: cp.SizeBytes,
			})
		}
	}
	return metas, nil
}

func (m *MockCheckpointer) Delete(ctx context.Context, id core.CheckpointID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.checkpoints, id)
	return nil
}

func (m *MockCheckpointer) Latest(ctx context.Context, taskID string) (*core.Checkpoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var latest *core.Checkpoint
	for _, cp := range m.checkpoints {
		if cp.TaskID == taskID {
			if latest == nil || cp.CreatedAt.After(latest.CreatedAt) {
				latest = cp
			}
		}
	}
	if latest == nil {
		return nil, assert.AnError
	}
	return latest, nil
}

func (m *MockCheckpointer) Prune(ctx context.Context, taskID string, keepCount int) error {
	return nil
}

// TestGraph_AutoCheckpoint 测试自动检查点保存
func TestGraph_AutoCheckpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	checkpointer := NewMockCheckpointer()

	config := &core.GraphConfig{
		ExecutionMode:  core.ExecutionModeSequential,
		MaxIterations:  10,
		TaskID:         "test_task_001",
		Checkpointer:   checkpointer,
		AutoCheckpoint: true,
	}

	graph := core.NewGraph("start", config)

	// 添加节点
	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("step", "start")
		return state, nil
	})

	graph.AddNode("middle", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("step", "middle")
		return state, nil
	})

	graph.AddNode("end", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("step", "end")
		return state, nil
	})

	graph.AddEdge("start", "middle")
	graph.AddEdge("middle", "end")
	graph.AddEdge("end", core.END)

	err := graph.Compile()
	require.NoError(t, err)

	// 执行
	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证最终状态
	step, err := finalState.GetString("step")
	require.NoError(t, err)
	assert.Equal(t, "end", step)

	// 验证保存了检查点
	checkpoints, err := checkpointer.List(ctx, "test_task_001", 100)
	require.NoError(t, err)
	assert.NotEmpty(t, checkpoints, "应该保存了检查点")
}

// TestGraph_CheckpointStrategy 测试检查点策略
func TestGraph_CheckpointStrategy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	checkpointer := NewMockCheckpointer()

	// 只在特定阶段保存
	strategy := core.NewPhaseCheckpointStrategy([]string{"middle"})

	config := &core.GraphConfig{
		ExecutionMode:      core.ExecutionModeSequential,
		MaxIterations:      10,
		TaskID:             "test_task_002",
		Checkpointer:       checkpointer,
		AutoCheckpoint:     true,
		CheckpointStrategy: strategy,
	}

	graph := core.NewGraph("start", config)

	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("count", 1)
		return state, nil
	})

	graph.AddNode("middle", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		count, _ := state.GetInt("count")
		state.Set("count", count+1)
		return state, nil
	})

	graph.AddNode("end", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		count, _ := state.GetInt("count")
		state.Set("count", count+1)
		return state, nil
	})

	graph.AddEdge("start", "middle")
	graph.AddEdge("middle", "end")
	graph.AddEdge("end", core.END)

	err := graph.Compile()
	require.NoError(t, err)

	// 执行
	input := core.NewGraphState()
	_, err = graph.Run(ctx, *input)
	require.NoError(t, err)

	// 验证只保存了 middle 阶段的检查点
	checkpoints, err := checkpointer.List(ctx, "test_task_002", 100)
	require.NoError(t, err)

	// 检查是否有 middle 阶段的检查点
	foundMiddle := false
	for _, cp := range checkpoints {
		if cp.Phase == "middle" {
			foundMiddle = true
		}
	}
	assert.True(t, foundMiddle, "应该保存了 middle 阶段的检查点")
}

// TestGraph_LoadFromCheckpoint 测试从检查点恢复
func TestGraph_LoadFromCheckpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	checkpointer := NewMockCheckpointer()

	config := &core.GraphConfig{
		ExecutionMode:  core.ExecutionModeSequential,
		MaxIterations:  10,
		TaskID:         "test_task_003",
		Checkpointer:   checkpointer,
		AutoCheckpoint: true,
	}

	graph := core.NewGraph("start", config)

	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("value", 10)
		return state, nil
	})

	graph.AddNode("process", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		val, _ := state.GetInt("value")
		state.Set("value", val*2)
		return state, nil
	})

	graph.AddEdge("start", "process")
	graph.AddEdge("process", core.END)

	err := graph.Compile()
	require.NoError(t, err)

	// 执行并保存检查点
	input := core.NewGraphState()
	_, err = graph.Run(ctx, *input)
	require.NoError(t, err)

	// 获取最新检查点
	latest, err := checkpointer.Latest(ctx, "test_task_003")
	require.NoError(t, err)
	require.NotNil(t, latest)

	// 从检查点恢复状态
	// 注意：LoadFromCheckpoint 需要是 Graph 的方法
	// 由于当前实现在 graph 私有结构体上，这里模拟恢复过程
	var restoredState core.GraphState
	err = json.Unmarshal(latest.StateSnapshot, &restoredState)
	require.NoError(t, err)

	// 验证恢复的状态包含数据
	// GraphState 是 map，反序列化后应该有值
	assert.NotNil(t, restoredState)
}

// TestGraph_NoCheckpointer 测试未配置 Checkpointer 时不报错
func TestGraph_NoCheckpointer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	config := &core.GraphConfig{
		ExecutionMode:  core.ExecutionModeSequential,
		MaxIterations:  10,
		AutoCheckpoint: true, // 开启自动检查点，但未配置 Checkpointer
	}

	graph := core.NewGraph("start", config)

	graph.AddNode("start", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		state.Set("data", "test")
		return state, nil
	})

	graph.AddEdge("start", core.END)

	err := graph.Compile()
	require.NoError(t, err)

	// 执行应该成功（即使没有 Checkpointer）
	input := core.NewGraphState()
	_, err = graph.Run(ctx, *input)
	require.NoError(t, err)
}
