package core

import (
	"fmt"
	"reflect"
)

// ReplaceReducer 替换归约器（当前默认行为）
// 直接用新状态替换旧状态
type ReplaceReducer[T any] struct{}

func (r *ReplaceReducer[T]) Reduce(old, new T) (T, error) {
	return new, nil
}

func (r *ReplaceReducer[T]) Name() string {
	return "replace"
}

// MergeMapReducer Map 合并归约器
// 新值覆盖旧值，保留旧值中不在新值中的键
type MergeMapReducer struct{}

func (r *MergeMapReducer) Reduce(old, new map[string]any) (map[string]any, error) {
	result := make(map[string]any)

	// 复制旧值
	for k, v := range old {
		result[k] = v
	}

	// 合并新值（覆盖）
	for k, v := range new {
		result[k] = v
	}

	return result, nil
}

func (r *MergeMapReducer) Name() string {
	return "merge_map"
}

// DeepMergeMapReducer 深度合并 Map 归约器
// 递归合并嵌套的 map
type DeepMergeMapReducer struct{}

func (r *DeepMergeMapReducer) Reduce(old, new map[string]any) (map[string]any, error) {
	return r.deepMerge(old, new), nil
}

func (r *DeepMergeMapReducer) deepMerge(old, new map[string]any) map[string]any {
	result := make(map[string]any)

	// 复制旧值
	for k, v := range old {
		result[k] = v
	}

	// 深度合并新值
	for k, newVal := range new {
		if oldVal, exists := result[k]; exists {
			// 如果新旧值都是 map，递归合并
			oldMap, oldIsMap := oldVal.(map[string]any)
			newMap, newIsMap := newVal.(map[string]any)
			if oldIsMap && newIsMap {
				result[k] = r.deepMerge(oldMap, newMap)
				continue
			}
		}
		// 否则直接覆盖
		result[k] = newVal
	}

	return result
}

func (r *DeepMergeMapReducer) Name() string {
	return "deep_merge_map"
}

// AppendSliceReducer Slice 追加归约器
// 将新 slice 追加到旧 slice 末尾
type AppendSliceReducer[T any] struct{}

func (r *AppendSliceReducer[T]) Reduce(old, new []T) ([]T, error) {
	result := make([]T, 0, len(old)+len(new))
	result = append(result, old...)
	result = append(result, new...)
	return result, nil
}

func (r *AppendSliceReducer[T]) Name() string {
	return "append_slice"
}

// UniqueAppendSliceReducer 去重追加 Slice 归约器
// 追加时跳过已存在的元素（需要元素可比较）
type UniqueAppendSliceReducer[T comparable] struct{}

func (r *UniqueAppendSliceReducer[T]) Reduce(old, new []T) ([]T, error) {
	// 构建已存在元素的集合
	existingSet := make(map[T]bool)
	for _, item := range old {
		existingSet[item] = true
	}

	// 追加不重复的新元素
	result := make([]T, len(old))
	copy(result, old)

	for _, item := range new {
		if !existingSet[item] {
			result = append(result, item)
			existingSet[item] = true
		}
	}

	return result, nil
}

func (r *UniqueAppendSliceReducer[T]) Name() string {
	return "unique_append_slice"
}

// AddIntReducer 整数累加归约器
type AddIntReducer struct{}

func (r *AddIntReducer) Reduce(old, new int) (int, error) {
	return old + new, nil
}

func (r *AddIntReducer) Name() string {
	return "add_int"
}

// AddInt64Reducer 64位整数累加归约器
type AddInt64Reducer struct{}

func (r *AddInt64Reducer) Reduce(old, new int64) (int64, error) {
	return old + new, nil
}

func (r *AddInt64Reducer) Name() string {
	return "add_int64"
}

// AddFloat64Reducer 浮点数累加归约器
type AddFloat64Reducer struct{}

func (r *AddFloat64Reducer) Reduce(old, new float64) (float64, error) {
	return old + new, nil
}

func (r *AddFloat64Reducer) Name() string {
	return "add_float64"
}

// MaxIntReducer 整数取最大值归约器
type MaxIntReducer struct{}

func (r *MaxIntReducer) Reduce(old, new int) (int, error) {
	if new > old {
		return new, nil
	}
	return old, nil
}

func (r *MaxIntReducer) Name() string {
	return "max_int"
}

// MinIntReducer 整数取最小值归约器
type MinIntReducer struct{}

func (r *MinIntReducer) Reduce(old, new int) (int, error) {
	if new < old {
		return new, nil
	}
	return old, nil
}

func (r *MinIntReducer) Name() string {
	return "min_int"
}

// LogicalOrReducer 逻辑或归约器
// 只要有一个为 true，结果就是 true
type LogicalOrReducer struct{}

func (r *LogicalOrReducer) Reduce(old, new bool) (bool, error) {
	return old || new, nil
}

func (r *LogicalOrReducer) Name() string {
	return "logical_or"
}

// LogicalAndReducer 逻辑与归约器
// 只有两个都为 true，结果才是 true
type LogicalAndReducer struct{}

func (r *LogicalAndReducer) Reduce(old, new bool) (bool, error) {
	return old && new, nil
}

func (r *LogicalAndReducer) Name() string {
	return "logical_and"
}

// StructMergeReducer 结构体字段合并归约器
// 使用反射将新结构体的非零值字段合并到旧结构体
type StructMergeReducer[T any] struct{}

func (r *StructMergeReducer[T]) Reduce(old, new T) (T, error) {
	oldVal := reflect.ValueOf(&old).Elem()
	newVal := reflect.ValueOf(new)

	if oldVal.Kind() != reflect.Struct {
		return new, fmt.Errorf("StructMergeReducer only works with structs")
	}

	// 遍历新结构体的字段
	for i := 0; i < newVal.NumField(); i++ {
		newField := newVal.Field(i)
		oldField := oldVal.Field(i)

		// 如果字段可设置且新字段非零值，则更新
		if oldField.CanSet() && !isZeroValue(newField) {
			oldField.Set(newField)
		}
	}

	return old, nil
}

func (r *StructMergeReducer[T]) Name() string {
	return "struct_merge"
}

// isZeroValue 判断是否是零值
func isZeroValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.String:
		return v.String() == ""
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return v.IsNil()
	default:
		return false
	}
}

// CustomReducer 自定义归约器
// 允许用户传入自定义的归约函数
type CustomReducer[T any] struct {
	name       string
	reduceFunc func(old, new T) (T, error)
}

func NewCustomReducer[T any](name string, reduceFunc func(old, new T) (T, error)) *CustomReducer[T] {
	return &CustomReducer[T]{
		name:       name,
		reduceFunc: reduceFunc,
	}
}

func (r *CustomReducer[T]) Reduce(old, new T) (T, error) {
	return r.reduceFunc(old, new)
}

func (r *CustomReducer[T]) Name() string {
	return r.name
}
