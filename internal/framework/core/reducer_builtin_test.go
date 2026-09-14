package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReplaceReducer 测试替换归约器
func TestReplaceReducer(t *testing.T) {
	t.Run("替换整数", func(t *testing.T) {
		reducer := &ReplaceReducer[int]{}
		result, err := reducer.Reduce(10, 20)
		require.NoError(t, err)
		assert.Equal(t, 20, result)
		assert.Equal(t, "replace", reducer.Name())
	})

	t.Run("替换字符串", func(t *testing.T) {
		reducer := &ReplaceReducer[string]{}
		result, err := reducer.Reduce("old", "new")
		require.NoError(t, err)
		assert.Equal(t, "new", result)
	})

	t.Run("替换复杂结构", func(t *testing.T) {
		type Config struct {
			Host string
			Port int
		}
		reducer := &ReplaceReducer[Config]{}
		old := Config{Host: "localhost", Port: 8080}
		new := Config{Host: "0.0.0.0", Port: 9090}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, new, result)
	})
}

// TestMergeMapReducer 测试 Map 合并归约器
func TestMergeMapReducer(t *testing.T) {
	reducer := &MergeMapReducer{}

	t.Run("合并不重叠的键", func(t *testing.T) {
		old := map[string]any{"a": 1, "b": 2}
		new := map[string]any{"c": 3, "d": 4}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"a": 1, "b": 2, "c": 3, "d": 4}, result)
	})

	t.Run("新值覆盖旧值", func(t *testing.T) {
		old := map[string]any{"a": 1, "b": 2}
		new := map[string]any{"b": 20, "c": 3}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"a": 1, "b": 20, "c": 3}, result)
	})

	t.Run("空旧 Map", func(t *testing.T) {
		old := map[string]any{}
		new := map[string]any{"a": 1}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"a": 1}, result)
	})

	t.Run("空新 Map", func(t *testing.T) {
		old := map[string]any{"a": 1}
		new := map[string]any{}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"a": 1}, result)
	})

	t.Run("nil Map", func(t *testing.T) {
		old := map[string]any{"a": 1}
		result, err := reducer.Reduce(old, nil)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"a": 1}, result)
	})

	t.Run("归约器名称", func(t *testing.T) {
		assert.Equal(t, "merge_map", reducer.Name())
	})
}

// TestDeepMergeMapReducer 测试深度合并 Map 归约器
func TestDeepMergeMapReducer(t *testing.T) {
	reducer := &DeepMergeMapReducer{}

	t.Run("单层合并", func(t *testing.T) {
		old := map[string]any{"a": 1, "b": 2}
		new := map[string]any{"b": 20, "c": 3}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"a": 1, "b": 20, "c": 3}, result)
	})

	t.Run("嵌套 Map 深度合并", func(t *testing.T) {
		old := map[string]any{
			"config": map[string]any{
				"host": "localhost",
				"port": 8080,
			},
			"debug": true,
		}
		new := map[string]any{
			"config": map[string]any{
				"port":    9090,
				"timeout": 30,
			},
		}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)

		expected := map[string]any{
			"config": map[string]any{
				"host":    "localhost",
				"port":    9090,
				"timeout": 30,
			},
			"debug": true,
		}
		assert.Equal(t, expected, result)
	})

	t.Run("多层嵌套合并", func(t *testing.T) {
		old := map[string]any{
			"a": map[string]any{
				"b": map[string]any{
					"c": 1,
					"d": 2,
				},
			},
		}
		new := map[string]any{
			"a": map[string]any{
				"b": map[string]any{
					"d": 20,
					"e": 3,
				},
			},
		}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)

		expected := map[string]any{
			"a": map[string]any{
				"b": map[string]any{
					"c": 1,
					"d": 20,
					"e": 3,
				},
			},
		}
		assert.Equal(t, expected, result)
	})

	t.Run("非 Map 类型直接覆盖", func(t *testing.T) {
		old := map[string]any{"value": "old"}
		new := map[string]any{"value": "new"}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"value": "new"}, result)
	})

	t.Run("归约器名称", func(t *testing.T) {
		assert.Equal(t, "deep_merge_map", reducer.Name())
	})
}

// TestAppendSliceReducer 测试 Slice 追加归约器
func TestAppendSliceReducer(t *testing.T) {
	t.Run("追加整数切片", func(t *testing.T) {
		reducer := &AppendSliceReducer[int]{}
		old := []int{1, 2, 3}
		new := []int{4, 5}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2, 3, 4, 5}, result)
		assert.Equal(t, "append_slice", reducer.Name())
	})

	t.Run("追加字符串切片", func(t *testing.T) {
		reducer := &AppendSliceReducer[string]{}
		old := []string{"a", "b"}
		new := []string{"c", "d"}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b", "c", "d"}, result)
	})

	t.Run("空旧切片", func(t *testing.T) {
		reducer := &AppendSliceReducer[int]{}
		old := []int{}
		new := []int{1, 2}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2}, result)
	})

	t.Run("空新切片", func(t *testing.T) {
		reducer := &AppendSliceReducer[int]{}
		old := []int{1, 2}
		new := []int{}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2}, result)
	})

	t.Run("nil 切片", func(t *testing.T) {
		reducer := &AppendSliceReducer[int]{}
		result, err := reducer.Reduce(nil, []int{1, 2})
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2}, result)
	})
}

// TestUniqueAppendSliceReducer 测试去重追加 Slice 归约器
func TestUniqueAppendSliceReducer(t *testing.T) {
	t.Run("去重追加整数", func(t *testing.T) {
		reducer := &UniqueAppendSliceReducer[int]{}
		old := []int{1, 2, 3}
		new := []int{3, 4, 5}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2, 3, 4, 5}, result)
		assert.Equal(t, "unique_append_slice", reducer.Name())
	})

	t.Run("去重追加字符串", func(t *testing.T) {
		reducer := &UniqueAppendSliceReducer[string]{}
		old := []string{"a", "b", "c"}
		new := []string{"b", "c", "d"}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b", "c", "d"}, result)
	})

	t.Run("全部重复", func(t *testing.T) {
		reducer := &UniqueAppendSliceReducer[int]{}
		old := []int{1, 2, 3}
		new := []int{1, 2, 3}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2, 3}, result)
	})

	t.Run("全部不重复", func(t *testing.T) {
		reducer := &UniqueAppendSliceReducer[int]{}
		old := []int{1, 2}
		new := []int{3, 4}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2, 3, 4}, result)
	})

	t.Run("空旧切片", func(t *testing.T) {
		reducer := &UniqueAppendSliceReducer[int]{}
		old := []int{}
		new := []int{1, 2, 2, 3}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2, 3}, result)
	})
}

// TestAddIntReducer 测试整数累加归约器
func TestAddIntReducer(t *testing.T) {
	reducer := &AddIntReducer{}

	t.Run("正整数相加", func(t *testing.T) {
		result, err := reducer.Reduce(10, 20)
		require.NoError(t, err)
		assert.Equal(t, 30, result)
		assert.Equal(t, "add_int", reducer.Name())
	})

	t.Run("负整数相加", func(t *testing.T) {
		result, err := reducer.Reduce(-10, -20)
		require.NoError(t, err)
		assert.Equal(t, -30, result)
	})

	t.Run("正负整数相加", func(t *testing.T) {
		result, err := reducer.Reduce(10, -5)
		require.NoError(t, err)
		assert.Equal(t, 5, result)
	})

	t.Run("零值相加", func(t *testing.T) {
		result, err := reducer.Reduce(0, 10)
		require.NoError(t, err)
		assert.Equal(t, 10, result)
	})
}

// TestAddInt64Reducer 测试 64 位整数累加归约器
func TestAddInt64Reducer(t *testing.T) {
	reducer := &AddInt64Reducer{}

	t.Run("大整数相加", func(t *testing.T) {
		result, err := reducer.Reduce(int64(1000000000), int64(2000000000))
		require.NoError(t, err)
		assert.Equal(t, int64(3000000000), result)
		assert.Equal(t, "add_int64", reducer.Name())
	})
}

// TestAddFloat64Reducer 测试浮点数累加归约器
func TestAddFloat64Reducer(t *testing.T) {
	reducer := &AddFloat64Reducer{}

	t.Run("浮点数相加", func(t *testing.T) {
		result, err := reducer.Reduce(1.5, 2.3)
		require.NoError(t, err)
		assert.InDelta(t, 3.8, result, 0.0001)
		assert.Equal(t, "add_float64", reducer.Name())
	})

	t.Run("负浮点数相加", func(t *testing.T) {
		result, err := reducer.Reduce(-1.5, -2.3)
		require.NoError(t, err)
		assert.InDelta(t, -3.8, result, 0.0001)
	})
}

// TestMaxIntReducer 测试整数取最大值归约器
func TestMaxIntReducer(t *testing.T) {
	reducer := &MaxIntReducer{}

	t.Run("新值更大", func(t *testing.T) {
		result, err := reducer.Reduce(10, 20)
		require.NoError(t, err)
		assert.Equal(t, 20, result)
		assert.Equal(t, "max_int", reducer.Name())
	})

	t.Run("旧值更大", func(t *testing.T) {
		result, err := reducer.Reduce(20, 10)
		require.NoError(t, err)
		assert.Equal(t, 20, result)
	})

	t.Run("相等", func(t *testing.T) {
		result, err := reducer.Reduce(15, 15)
		require.NoError(t, err)
		assert.Equal(t, 15, result)
	})

	t.Run("负数比较", func(t *testing.T) {
		result, err := reducer.Reduce(-10, -5)
		require.NoError(t, err)
		assert.Equal(t, -5, result)
	})
}

// TestMinIntReducer 测试整数取最小值归约器
func TestMinIntReducer(t *testing.T) {
	reducer := &MinIntReducer{}

	t.Run("新值更小", func(t *testing.T) {
		result, err := reducer.Reduce(20, 10)
		require.NoError(t, err)
		assert.Equal(t, 10, result)
		assert.Equal(t, "min_int", reducer.Name())
	})

	t.Run("旧值更小", func(t *testing.T) {
		result, err := reducer.Reduce(10, 20)
		require.NoError(t, err)
		assert.Equal(t, 10, result)
	})

	t.Run("相等", func(t *testing.T) {
		result, err := reducer.Reduce(15, 15)
		require.NoError(t, err)
		assert.Equal(t, 15, result)
	})

	t.Run("负数比较", func(t *testing.T) {
		result, err := reducer.Reduce(-5, -10)
		require.NoError(t, err)
		assert.Equal(t, -10, result)
	})
}

// TestLogicalOrReducer 测试逻辑或归约器
func TestLogicalOrReducer(t *testing.T) {
	reducer := &LogicalOrReducer{}

	t.Run("false OR false", func(t *testing.T) {
		result, err := reducer.Reduce(false, false)
		require.NoError(t, err)
		assert.False(t, result)
		assert.Equal(t, "logical_or", reducer.Name())
	})

	t.Run("false OR true", func(t *testing.T) {
		result, err := reducer.Reduce(false, true)
		require.NoError(t, err)
		assert.True(t, result)
	})

	t.Run("true OR false", func(t *testing.T) {
		result, err := reducer.Reduce(true, false)
		require.NoError(t, err)
		assert.True(t, result)
	})

	t.Run("true OR true", func(t *testing.T) {
		result, err := reducer.Reduce(true, true)
		require.NoError(t, err)
		assert.True(t, result)
	})
}

// TestLogicalAndReducer 测试逻辑与归约器
func TestLogicalAndReducer(t *testing.T) {
	reducer := &LogicalAndReducer{}

	t.Run("false AND false", func(t *testing.T) {
		result, err := reducer.Reduce(false, false)
		require.NoError(t, err)
		assert.False(t, result)
		assert.Equal(t, "logical_and", reducer.Name())
	})

	t.Run("false AND true", func(t *testing.T) {
		result, err := reducer.Reduce(false, true)
		require.NoError(t, err)
		assert.False(t, result)
	})

	t.Run("true AND false", func(t *testing.T) {
		result, err := reducer.Reduce(true, false)
		require.NoError(t, err)
		assert.False(t, result)
	})

	t.Run("true AND true", func(t *testing.T) {
		result, err := reducer.Reduce(true, true)
		require.NoError(t, err)
		assert.True(t, result)
	})
}

// TestStructMergeReducer 测试结构体合并归约器
func TestStructMergeReducer(t *testing.T) {
	type TestStruct struct {
		Name    string
		Age     int
		Enabled bool
	}

	reducer := &StructMergeReducer[TestStruct]{}

	t.Run("合并非零字段", func(t *testing.T) {
		old := TestStruct{Name: "Alice", Age: 30, Enabled: true}
		new := TestStruct{Age: 35}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, "Alice", result.Name)
		assert.Equal(t, 35, result.Age)
		assert.True(t, result.Enabled)
		assert.Equal(t, "struct_merge", reducer.Name())
	})

	t.Run("新结构全为零值", func(t *testing.T) {
		old := TestStruct{Name: "Bob", Age: 25, Enabled: true}
		new := TestStruct{}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, old, result)
	})

	t.Run("旧结构为零值", func(t *testing.T) {
		old := TestStruct{}
		new := TestStruct{Name: "Charlie", Age: 40}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, "Charlie", result.Name)
		assert.Equal(t, 40, result.Age)
		assert.False(t, result.Enabled)
	})

	t.Run("部分字段更新", func(t *testing.T) {
		old := TestStruct{Name: "David", Age: 28, Enabled: false}
		new := TestStruct{Name: "David Jr.", Enabled: true}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, "David Jr.", result.Name)
		assert.Equal(t, 28, result.Age)
		assert.True(t, result.Enabled)
	})
}

// TestCustomReducer 测试自定义归约器
func TestCustomReducer(t *testing.T) {
	t.Run("自定义字符串拼接", func(t *testing.T) {
		reducer := NewCustomReducer("concat", func(old, new string) (string, error) {
			return old + "," + new, nil
		})

		result, err := reducer.Reduce("a", "b")
		require.NoError(t, err)
		assert.Equal(t, "a,b", result)
		assert.Equal(t, "concat", reducer.Name())
	})

	t.Run("自定义整数乘法", func(t *testing.T) {
		reducer := NewCustomReducer("multiply", func(old, new int) (int, error) {
			return old * new, nil
		})

		result, err := reducer.Reduce(3, 4)
		require.NoError(t, err)
		assert.Equal(t, 12, result)
		assert.Equal(t, "multiply", reducer.Name())
	})

	t.Run("自定义带错误处理", func(t *testing.T) {
		reducer := NewCustomReducer("divide", func(old, new int) (int, error) {
			if new == 0 {
				return 0, ErrInvalidConfig{Field: "divisor", Reason: "除数不能为零"}
			}
			return old / new, nil
		})

		// 正常情况
		result, err := reducer.Reduce(10, 2)
		require.NoError(t, err)
		assert.Equal(t, 5, result)

		// 错误情况
		_, err = reducer.Reduce(10, 0)
		require.Error(t, err)
	})

	t.Run("自定义复杂类型", func(t *testing.T) {
		type Counter struct {
			Count int
			Label string
		}

		reducer := NewCustomReducer("increment_counter", func(old, new Counter) (Counter, error) {
			return Counter{
				Count: old.Count + new.Count,
				Label: new.Label,
			}, nil
		})

		old := Counter{Count: 5, Label: "old"}
		new := Counter{Count: 3, Label: "new"}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, 8, result.Count)
		assert.Equal(t, "new", result.Label)
	})
}

// TestReducerEdgeCases 测试边界情况
func TestReducerEdgeCases(t *testing.T) {
	t.Run("ReplaceReducer 处理 nil 指针", func(t *testing.T) {
		reducer := &ReplaceReducer[*int]{}
		var old *int = nil
		newVal := 10
		result, err := reducer.Reduce(old, &newVal)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, 10, *result)
	})

	t.Run("AppendSliceReducer 大切片", func(t *testing.T) {
		reducer := &AppendSliceReducer[int]{}
		old := make([]int, 1000)
		new := make([]int, 1000)
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Equal(t, 2000, len(result))
	})

	t.Run("MergeMapReducer 处理 nil 值", func(t *testing.T) {
		reducer := &MergeMapReducer{}
		old := map[string]any{"a": 1}
		new := map[string]any{"b": nil}
		result, err := reducer.Reduce(old, new)
		require.NoError(t, err)
		assert.Nil(t, result["b"])
		assert.Equal(t, 1, result["a"])
	})
}

// BenchmarkReducers 性能基准测试
func BenchmarkReducers(b *testing.B) {
	b.Run("ReplaceReducer", func(b *testing.B) {
		reducer := &ReplaceReducer[int]{}
		for i := 0; i < b.N; i++ {
			_, _ = reducer.Reduce(i, i+1)
		}
	})

	b.Run("MergeMapReducer", func(b *testing.B) {
		reducer := &MergeMapReducer{}
		old := map[string]any{"a": 1, "b": 2, "c": 3}
		new := map[string]any{"d": 4, "e": 5}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = reducer.Reduce(old, new)
		}
	})

	b.Run("AppendSliceReducer", func(b *testing.B) {
		reducer := &AppendSliceReducer[int]{}
		old := []int{1, 2, 3, 4, 5}
		new := []int{6, 7, 8, 9, 10}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = reducer.Reduce(old, new)
		}
	})

	b.Run("UniqueAppendSliceReducer", func(b *testing.B) {
		reducer := &UniqueAppendSliceReducer[int]{}
		old := []int{1, 2, 3, 4, 5}
		new := []int{3, 4, 5, 6, 7}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = reducer.Reduce(old, new)
		}
	})

	b.Run("DeepMergeMapReducer", func(b *testing.B) {
		reducer := &DeepMergeMapReducer{}
		old := map[string]any{
			"a": map[string]any{"b": map[string]any{"c": 1}},
		}
		new := map[string]any{
			"a": map[string]any{"b": map[string]any{"d": 2}},
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = reducer.Reduce(old, new)
		}
	})
}
