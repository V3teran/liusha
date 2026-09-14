package core

// StateReducer 状态归约器，定义如何合并新旧状态
// 用于并行节点更新时避免互相覆盖
type StateReducer[T any] interface {
	// Reduce 将新状态合并到旧状态，返回合并后的状态
	Reduce(old, new T) (T, error)

	// Name 返回 Reducer 名称（用于日志和调试）
	Name() string
}

// FieldReducer 字段级归约器（对不同字段使用不同策略）
type FieldReducer interface {
	// ReduceField 归约单个字段
	ReduceField(fieldName string, oldValue, newValue any) (any, error)

	// SupportedFields 返回支持的字段列表（空表示支持所有字段）
	SupportedFields() []string
}
