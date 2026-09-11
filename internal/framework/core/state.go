package core

import (
	"context"
	"encoding/json"
)

// State 是任务执行的统一状态容器。
// 泛型参数 T 是业务自定义的状态类型，编译期类型检查。
type State[T any] struct {
	// 任务标识
	TaskID string `json:"task_id"`

	// 业务状态（类型安全）
	Data T `json:"data"`

	// 状态元数据
	Metadata Metadata `json:"metadata"`

	// 版本号（乐观锁，防止并发冲突）
	Version int64 `json:"version"`
}

// Metadata 是状态的元信息。
type Metadata struct {
	// 任务状态：planning, executing, evaluating, completed, failed
	Phase string `json:"phase"`

	// 当前执行的节点 ID（用于恢复）
	CurrentNode string `json:"current_node,omitempty"`

	// 父任务 ID（子任务场景）
	ParentTaskID string `json:"parent_task_id,omitempty"`

	// 创建时间戳（Unix 毫秒）
	CreatedAt int64 `json:"created_at"`

	// 更新时间戳（Unix 毫秒）
	UpdatedAt int64 `json:"updated_at"`

	// 自定义标签
	Tags map[string]string `json:"tags,omitempty"`
}

// StateManager 管理任务状态的生命周期。
// 泛型接口，业务层注入具体类型。
type StateManager[T any] interface {
	// Get 获取任务状态（带缓存）
	Get(ctx context.Context, taskID string) (*State[T], error)

	// Create 创建新任务状态
	Create(ctx context.Context, state *State[T]) error

	// Update 更新任务状态（乐观锁，失败返回 ErrVersionConflict）
	Update(ctx context.Context, state *State[T]) error

	// Delete 删除任务状态
	Delete(ctx context.Context, taskID string) error

	// List 列出任务状态（分页）
	List(ctx context.Context, filter StateFilter) ([]*State[T], error)
}

// StateFilter 是状态查询过滤器。
type StateFilter struct {
	// 按阶段过滤
	Phase string

	// 按父任务过滤
	ParentTaskID string

	// 按标签过滤
	Tags map[string]string

	// 分页参数
	Limit  int
	Offset int
}

// StateSnapshot 创建状态快照（用于 checkpoint）。
func (s *State[T]) Snapshot() (json.RawMessage, error) {
	return json.Marshal(s)
}

// RestoreSnapshot 从快照恢复状态。
func RestoreSnapshot[T any](data json.RawMessage) (*State[T], error) {
	var state State[T]
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}
