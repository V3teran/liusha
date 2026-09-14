package core

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInMemoryStateManager_Basic 测试基础 CRUD 操作
func TestInMemoryStateManager_Basic(t *testing.T) {
	ctx := context.Background()
	manager := NewInMemoryStateManager[map[string]any]()

	t.Run("创建状态", func(t *testing.T) {
		state := &State[map[string]any]{
			TaskID: "task-1",
			Data:   map[string]any{"count": 0},
			Metadata: Metadata{
				Phase: "planning",
			},
		}

		err := manager.Create(ctx, state)
		require.NoError(t, err)
		assert.Equal(t, int64(1), state.Version)
		assert.Greater(t, state.Metadata.CreatedAt, int64(0))
	})

	t.Run("获取状态", func(t *testing.T) {
		state, err := manager.Get(ctx, "task-1")
		require.NoError(t, err)
		assert.Equal(t, "task-1", state.TaskID)
		assert.Equal(t, 0, state.Data["count"])
	})

	t.Run("更新状态", func(t *testing.T) {
		state, _ := manager.Get(ctx, "task-1")
		state.Data["count"] = 10
		state.Metadata.Phase = "executing"

		err := manager.Update(ctx, state)
		require.NoError(t, err)
		assert.Equal(t, int64(2), state.Version)

		// 验证更新生效
		updated, _ := manager.Get(ctx, "task-1")
		assert.Equal(t, 10, updated.Data["count"])
		assert.Equal(t, "executing", updated.Metadata.Phase)
	})

	t.Run("删除状态", func(t *testing.T) {
		err := manager.Delete(ctx, "task-1")
		require.NoError(t, err)

		_, err = manager.Get(ctx, "task-1")
		require.Error(t, err)
	})
}

// TestInMemoryStateManager_OptimisticLock 测试乐观锁
func TestInMemoryStateManager_OptimisticLock(t *testing.T) {
	ctx := context.Background()
	manager := NewInMemoryStateManager[map[string]any]()

	// 创建初始状态
	state := &State[map[string]any]{
		TaskID: "task-1",
		Data:   map[string]any{"count": 0},
	}
	err := manager.Create(ctx, state)
	require.NoError(t, err)

	t.Run("版本冲突检测", func(t *testing.T) {
		// 两个并发读取
		state1, _ := manager.Get(ctx, "task-1")
		state2, _ := manager.Get(ctx, "task-1")

		// 第一个更新成功
		state1.Data["count"] = 10
		err := manager.Update(ctx, state1)
		require.NoError(t, err)

		// 第二个更新失败（版本冲突）
		state2.Data["count"] = 20
		err = manager.Update(ctx, state2)
		require.Error(t, err)

		var versionErr ErrVersionConflict
		assert.ErrorAs(t, err, &versionErr)
		assert.Equal(t, "task-1", versionErr.TaskID)
	})
}

// TestInMemoryStateManager_UpdateWith 测试 Reducer 集成
func TestInMemoryStateManager_UpdateWith(t *testing.T) {
	ctx := context.Background()

	t.Run("使用 AddIntReducer 累加计数", func(t *testing.T) {
		manager := NewInMemoryStateManager[int]()

		// 创建初始状态
		state := &State[int]{
			TaskID: "counter",
			Data:   10,
		}
		err := manager.Create(ctx, state)
		require.NoError(t, err)

		// 使用 Reducer 累加
		reducer := &AddIntReducer{}
		err = manager.UpdateWith(ctx, "counter", 5, reducer)
		require.NoError(t, err)

		// 验证结果
		updated, _ := manager.Get(ctx, "counter")
		assert.Equal(t, 15, updated.Data)
		assert.Equal(t, int64(2), updated.Version)
	})

	t.Run("使用 MergeMapReducer 合并配置", func(t *testing.T) {
		manager := NewInMemoryStateManager[map[string]any]()

		// 创建初始状态
		state := &State[map[string]any]{
			TaskID: "config",
			Data:   map[string]any{"host": "localhost", "port": 8080},
		}
		err := manager.Create(ctx, state)
		require.NoError(t, err)

		// 使用 Reducer 合并
		reducer := &MergeMapReducer{}
		newData := map[string]any{"port": 9090, "debug": true}
		err = manager.UpdateWith(ctx, "config", newData, reducer)
		require.NoError(t, err)

		// 验证结果
		updated, _ := manager.Get(ctx, "config")
		assert.Equal(t, "localhost", updated.Data["host"])
		assert.Equal(t, 9090, updated.Data["port"])
		assert.Equal(t, true, updated.Data["debug"])
	})

	t.Run("使用 AppendSliceReducer 追加日志", func(t *testing.T) {
		manager := NewInMemoryStateManager[[]string]()

		// 创建初始状态
		state := &State[[]string]{
			TaskID: "logs",
			Data:   []string{"log1", "log2"},
		}
		err := manager.Create(ctx, state)
		require.NoError(t, err)

		// 使用 Reducer 追加
		reducer := &AppendSliceReducer[string]{}
		newLogs := []string{"log3", "log4"}
		err = manager.UpdateWith(ctx, "logs", newLogs, reducer)
		require.NoError(t, err)

		// 验证结果
		updated, _ := manager.Get(ctx, "logs")
		assert.Equal(t, []string{"log1", "log2", "log3", "log4"}, updated.Data)
	})

	t.Run("使用 CustomReducer 自定义逻辑", func(t *testing.T) {
		manager := NewInMemoryStateManager[int]()

		// 创建初始状态
		state := &State[int]{
			TaskID: "multiply",
			Data:   3,
		}
		err := manager.Create(ctx, state)
		require.NoError(t, err)

		// 自定义乘法 Reducer
		reducer := NewCustomReducer("multiply", func(old, new int) (int, error) {
			return old * new, nil
		})

		err = manager.UpdateWith(ctx, "multiply", 4, reducer)
		require.NoError(t, err)

		// 验证结果
		updated, _ := manager.Get(ctx, "multiply")
		assert.Equal(t, 12, updated.Data)
	})
}

// TestInMemoryStateManager_ConcurrentUpdates 测试并发更新
func TestInMemoryStateManager_ConcurrentUpdates(t *testing.T) {
	ctx := context.Background()
	manager := NewInMemoryStateManager[int]()

	// 创建初始状态
	state := &State[int]{
		TaskID: "counter",
		Data:   0,
	}
	err := manager.Create(ctx, state)
	require.NoError(t, err)

	t.Run("并发 UpdateWith 自动重试", func(t *testing.T) {
		const goroutines = 10
		const incrementsPerGoroutine = 10

		var wg sync.WaitGroup
		reducer := &AddIntReducer{}

		// 启动多个 goroutine 并发累加
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < incrementsPerGoroutine; j++ {
					err := manager.UpdateWith(ctx, "counter", 1, reducer)
					assert.NoError(t, err)
				}
			}()
		}

		wg.Wait()

		// 验证最终结果
		final, _ := manager.Get(ctx, "counter")
		expected := goroutines * incrementsPerGoroutine
		assert.Equal(t, expected, final.Data)
	})

	t.Run("并发 Map 合并无数据丢失", func(t *testing.T) {
		manager := NewInMemoryStateManager[map[string]any]()

		state := &State[map[string]any]{
			TaskID: "concurrent-map",
			Data:   make(map[string]any),
		}
		err := manager.Create(ctx, state)
		require.NoError(t, err)

		const goroutines = 5
		var wg sync.WaitGroup
		reducer := &MergeMapReducer{}

		// 每个 goroutine 写入不同的键
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				key := string(rune('a' + index))
				newData := map[string]any{key: index}
				err := manager.UpdateWith(ctx, "concurrent-map", newData, reducer)
				assert.NoError(t, err)
			}(i)
		}

		wg.Wait()

		// 验证所有键都存在
		final, _ := manager.Get(ctx, "concurrent-map")
		assert.Equal(t, goroutines, len(final.Data))
		for i := 0; i < goroutines; i++ {
			key := string(rune('a' + i))
			assert.Equal(t, i, final.Data[key])
		}
	})
}

// TestInMemoryStateManager_List 测试列表查询
func TestInMemoryStateManager_List(t *testing.T) {
	ctx := context.Background()
	manager := NewInMemoryStateManager[map[string]any]()

	// 创建多个状态
	states := []*State[map[string]any]{
		{
			TaskID: "task-1",
			Data:   map[string]any{},
			Metadata: Metadata{
				Phase:        "planning",
				ParentTaskID: "parent-1",
				Tags:         map[string]string{"env": "dev"},
			},
		},
		{
			TaskID: "task-2",
			Data:   map[string]any{},
			Metadata: Metadata{
				Phase:        "executing",
				ParentTaskID: "parent-1",
				Tags:         map[string]string{"env": "prod"},
			},
		},
		{
			TaskID: "task-3",
			Data:   map[string]any{},
			Metadata: Metadata{
				Phase:        "completed",
				ParentTaskID: "parent-2",
				Tags:         map[string]string{"env": "dev"},
			},
		},
	}

	for _, s := range states {
		err := manager.Create(ctx, s)
		require.NoError(t, err)
	}

	t.Run("按阶段过滤", func(t *testing.T) {
		results, err := manager.List(ctx, StateFilter{Phase: "planning"})
		require.NoError(t, err)
		assert.Equal(t, 1, len(results))
		assert.Equal(t, "task-1", results[0].TaskID)
	})

	t.Run("按父任务过滤", func(t *testing.T) {
		results, err := manager.List(ctx, StateFilter{ParentTaskID: "parent-1"})
		require.NoError(t, err)
		assert.Equal(t, 2, len(results))
	})

	t.Run("按标签过滤", func(t *testing.T) {
		results, err := manager.List(ctx, StateFilter{
			Tags: map[string]string{"env": "dev"},
		})
		require.NoError(t, err)
		assert.Equal(t, 2, len(results))
	})

	t.Run("分页", func(t *testing.T) {
		results, err := manager.List(ctx, StateFilter{
			Limit:  2,
			Offset: 0,
		})
		require.NoError(t, err)
		assert.Equal(t, 2, len(results))

		results, err = manager.List(ctx, StateFilter{
			Limit:  2,
			Offset: 2,
		})
		require.NoError(t, err)
		assert.Equal(t, 1, len(results))
	})
}

// TestInMemoryStateManager_UpdateWithRetry 测试重试机制
func TestInMemoryStateManager_UpdateWithRetry(t *testing.T) {
	ctx := context.Background()
	manager := NewInMemoryStateManager[int]()

	// 创建初始状态
	state := &State[int]{
		TaskID: "retry-test",
		Data:   0,
	}
	err := manager.Create(ctx, state)
	require.NoError(t, err)

	t.Run("冲突后自动重试成功", func(t *testing.T) {
		// 模拟并发场景：一个持续更新，另一个使用 UpdateWith
		done := make(chan bool)
		go func() {
			for i := 0; i < 5; i++ {
				s, _ := manager.Get(ctx, "retry-test")
				s.Data++
				_ = manager.Update(ctx, s)
				time.Sleep(time.Millisecond)
			}
			done <- true
		}()

		// UpdateWith 应该能自动重试并成功
		reducer := &AddIntReducer{}
		err := manager.UpdateWith(ctx, "retry-test", 100, reducer)
		require.NoError(t, err)

		<-done

		// 验证最终状态包含了所有更新
		final, _ := manager.Get(ctx, "retry-test")
		assert.GreaterOrEqual(t, final.Data, 100)
	})
}

// BenchmarkStateManager 性能基准测试
func BenchmarkStateManager(b *testing.B) {
	ctx := context.Background()

	b.Run("Create", func(b *testing.B) {
		manager := NewInMemoryStateManager[int]()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			state := &State[int]{
				TaskID: string(rune(i)),
				Data:   i,
			}
			_ = manager.Create(ctx, state)
		}
	})

	b.Run("Get", func(b *testing.B) {
		manager := NewInMemoryStateManager[int]()
		state := &State[int]{TaskID: "bench", Data: 42}
		_ = manager.Create(ctx, state)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = manager.Get(ctx, "bench")
		}
	})

	b.Run("UpdateWith", func(b *testing.B) {
		manager := NewInMemoryStateManager[int]()
		state := &State[int]{TaskID: "bench", Data: 0}
		_ = manager.Create(ctx, state)
		reducer := &AddIntReducer{}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = manager.UpdateWith(ctx, "bench", 1, reducer)
		}
	})
}
