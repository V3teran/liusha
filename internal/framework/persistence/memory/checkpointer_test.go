package memory

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckpointer_SaveAndLoad(t *testing.T) {
	ctx := context.Background()
	cp := NewCheckpointer()

	// 创建检查点
	checkpoint := core.Checkpoint{
		TaskID:        "task-1",
		Phase:         "exploration",
		StateSnapshot: json.RawMessage(`{"iteration":5,"key":"value"}`),
		ComponentStates: map[string]json.RawMessage{
			"planner": json.RawMessage(`{"total_actions":10}`),
		},
	}

	// 保存
	id, err := cp.Save(ctx, checkpoint)
	require.NoError(t, err)
	assert.NotEmpty(t, id)

	// 加载
	loaded, err := cp.Load(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, checkpoint.TaskID, loaded.TaskID)
	assert.Equal(t, checkpoint.Phase, loaded.Phase)
	assert.JSONEq(t, string(checkpoint.StateSnapshot), string(loaded.StateSnapshot))
	assert.NotZero(t, loaded.CreatedAt)
	assert.Greater(t, loaded.SizeBytes, int64(0))
}

func TestCheckpointer_LoadNotFound(t *testing.T) {
	ctx := context.Background()
	cp := NewCheckpointer()

	// 加载不存在的检查点
	_, err := cp.Load(ctx, core.CheckpointID("nonexistent"))
	require.Error(t, err)
	assert.IsType(t, core.ErrCheckpointNotFound{}, err)
}

func TestCheckpointer_List(t *testing.T) {
	ctx := context.Background()
	cp := NewCheckpointer()

	// 创建多个检查点
	taskID := "task-list"
	for i := 1; i <= 5; i++ {
		checkpoint := core.Checkpoint{
			TaskID:        taskID,
			Phase:         "exploration",
			StateSnapshot: json.RawMessage(`{}`),
		}
		_, err := cp.Save(ctx, checkpoint)
		require.NoError(t, err)
	}

	t.Run("列出所有检查点", func(t *testing.T) {
		list, err := cp.List(ctx, taskID, 10)
		require.NoError(t, err)
		assert.Len(t, list, 5)

		// 验证按时间倒序（最新的在前）
		for i := 0; i < len(list)-1; i++ {
			assert.True(t, list[i].CreatedAt.After(list[i+1].CreatedAt) ||
				list[i].CreatedAt.Equal(list[i+1].CreatedAt))
		}
	})

	t.Run("限制数量", func(t *testing.T) {
		list, err := cp.List(ctx, taskID, 3)
		require.NoError(t, err)
		assert.Len(t, list, 3)
	})

	t.Run("空任务", func(t *testing.T) {
		list, err := cp.List(ctx, "nonexistent", 10)
		require.NoError(t, err)
		assert.Empty(t, list)
	})
}

func TestCheckpointer_Delete(t *testing.T) {
	ctx := context.Background()
	cp := NewCheckpointer()

	// 创建检查点
	checkpoint := core.Checkpoint{
		TaskID:        "task-delete",
		Phase:         "exploration",
		StateSnapshot: json.RawMessage(`{}`),
	}
	id, err := cp.Save(ctx, checkpoint)
	require.NoError(t, err)

	// 验证存在
	_, err = cp.Load(ctx, id)
	require.NoError(t, err)

	// 删除
	err = cp.Delete(ctx, id)
	require.NoError(t, err)

	// 验证已删除
	_, err = cp.Load(ctx, id)
	require.Error(t, err)
	assert.IsType(t, core.ErrCheckpointNotFound{}, err)
}

func TestCheckpointer_Prune(t *testing.T) {
	ctx := context.Background()
	cp := NewCheckpointer()

	taskID := "task-prune"

	// 创建10个检查点
	for i := 1; i <= 10; i++ {
		checkpoint := core.Checkpoint{
			TaskID:        taskID,
			Phase:         "exploration",
			StateSnapshot: json.RawMessage(`{}`),
		}
		_, err := cp.Save(ctx, checkpoint)
		require.NoError(t, err)
	}

	// 验证有10个
	list, err := cp.List(ctx, taskID, 20)
	require.NoError(t, err)
	assert.Len(t, list, 10)

	// 修剪，只保留最新的5个
	err = cp.Prune(ctx, taskID, 5)
	require.NoError(t, err)

	// 验证还剩5个
	list, err = cp.List(ctx, taskID, 20)
	require.NoError(t, err)
	assert.Len(t, list, 5)
}

func TestCheckpointer_Concurrent(t *testing.T) {
	ctx := context.Background()
	cp := NewCheckpointer()

	taskID := "task-concurrent"
	const numGoroutines = 10

	// 并发保存
	done := make(chan error, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(_ int) {
			checkpoint := core.Checkpoint{
				TaskID:        taskID,
				Phase:         "exploration",
				StateSnapshot: json.RawMessage(`{}`),
			}
			_, err := cp.Save(ctx, checkpoint)
			done <- err
		}(i)
	}

	// 等待所有完成
	for i := 0; i < numGoroutines; i++ {
		err := <-done
		require.NoError(t, err)
	}

	// 验证所有都保存成功
	list, err := cp.List(ctx, taskID, 20)
	require.NoError(t, err)
	assert.Len(t, list, numGoroutines)
}

func TestCheckpointer_IsolationByTask(t *testing.T) {
	ctx := context.Background()
	cp := NewCheckpointer()

	// 为两个不同的任务创建检查点
	task1 := "task-1"
	task2 := "task-2"

	for i := 1; i <= 3; i++ {
		cp1 := core.Checkpoint{TaskID: task1, Phase: "exploration", StateSnapshot: json.RawMessage(`{}`)}
		cp2 := core.Checkpoint{TaskID: task2, Phase: "exploration", StateSnapshot: json.RawMessage(`{}`)}

		_, err := cp.Save(ctx, cp1)
		require.NoError(t, err)
		_, err = cp.Save(ctx, cp2)
		require.NoError(t, err)
	}

	// 验证隔离
	list1, err := cp.List(ctx, task1, 10)
	require.NoError(t, err)
	assert.Len(t, list1, 3)

	list2, err := cp.List(ctx, task2, 10)
	require.NoError(t, err)
	assert.Len(t, list2, 3)

	// 删除 task1 的所有检查点（逐个删除）
	for _, meta := range list1 {
		err := cp.Delete(ctx, meta.ID)
		require.NoError(t, err)
	}

	list1, _ = cp.List(ctx, task1, 10)
	assert.Empty(t, list1)

	list2, _ = cp.List(ctx, task2, 10)
	assert.Len(t, list2, 3) // task2 不受影响
}
