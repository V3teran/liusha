package core

import (
	"context"
	"encoding/json"
	"time"
)

// CheckpointID 是检查点的唯一标识符。
type CheckpointID string

// Checkpoint 是任务执行的完整快照。
// 包含状态、配置、运行时上下文，支持完整恢复。
type Checkpoint struct {
	// 检查点 ID
	ID CheckpointID `json:"id"`

	// 任务 ID
	TaskID string `json:"task_id"`

	// 状态快照（序列化的 State[T]）
	StateSnapshot json.RawMessage `json:"state_snapshot"`

	// 当前执行阶段
	Phase string `json:"phase"`

	// 各组件的内部状态（key: 组件名, value: 状态 JSON）
	ComponentStates map[string]json.RawMessage `json:"component_states"`

	// 检查点标签（用于分类和检索）
	Labels map[string]string `json:"labels,omitempty"`

	// 创建时间
	CreatedAt time.Time `json:"created_at"`

	// 检查点大小（字节）
	SizeBytes int64 `json:"size_bytes,omitempty"`
}

// CheckpointMeta 是检查点的元信息（列表查询时返回）。
type CheckpointMeta struct {
	ID        CheckpointID      `json:"id"`
	TaskID    string            `json:"task_id"`
	Phase     string            `json:"phase"`
	Labels    map[string]string `json:"labels,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	SizeBytes int64             `json:"size_bytes,omitempty"`
}

// Checkpointer 是持久化检查点的统一接口。
// 支持保存、加载、列表、删除操作。
type Checkpointer interface {
	// Save 保存检查点，返回生成的 ID
	Save(ctx context.Context, checkpoint Checkpoint) (CheckpointID, error)

	// Load 加载指定检查点
	Load(ctx context.Context, id CheckpointID) (*Checkpoint, error)

	// List 列出任务的所有检查点（按时间倒序）
	List(ctx context.Context, taskID string, limit int) ([]CheckpointMeta, error)

	// Delete 删除检查点
	Delete(ctx context.Context, id CheckpointID) error

	// Latest 获取任务的最新检查点
	Latest(ctx context.Context, taskID string) (*Checkpoint, error)

	// Prune 清理过期检查点（保留最近 N 个）
	Prune(ctx context.Context, taskID string, keepCount int) error
}

// CheckpointStrategy 是检查点保存策略。
type CheckpointStrategy interface {
	// ShouldSave 判断当前是否应该保存检查点
	ShouldSave(ctx context.Context, phase string, elapsed time.Duration) bool
}

// IntervalCheckpointStrategy 按时间间隔保存。
type IntervalCheckpointStrategy struct {
	Interval time.Duration
	lastSave time.Time
}

func (s *IntervalCheckpointStrategy) ShouldSave(ctx context.Context, phase string, elapsed time.Duration) bool {
	if time.Since(s.lastSave) >= s.Interval {
		s.lastSave = time.Now()
		return true
	}
	return false
}

// PhaseCheckpointStrategy 每个阶段结束后保存。
type PhaseCheckpointStrategy struct {
	phases map[string]bool
}

func NewPhaseCheckpointStrategy(phases []string) *PhaseCheckpointStrategy {
	m := make(map[string]bool)
	for _, p := range phases {
		m[p] = true
	}
	return &PhaseCheckpointStrategy{phases: m}
}

func (s *PhaseCheckpointStrategy) ShouldSave(ctx context.Context, phase string, elapsed time.Duration) bool {
	return s.phases[phase]
}

// Recoverable 是支持状态恢复的组件接口。
// 所有需要 checkpoint 的组件必须实现此接口。
type Recoverable interface {
	// ExportState 导出组件内部状态（用于 checkpoint）
	ExportState() (json.RawMessage, error)

	// ImportState 导入状态（用于恢复）
	ImportState(data json.RawMessage) error
}
