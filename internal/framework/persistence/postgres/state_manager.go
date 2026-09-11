package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/V3teran/liusha/internal/framework/core"
)

// StateManager 是 PostgreSQL 版状态管理器（生产用）。
type StateManager[T any] struct {
	pool *pgxpool.Pool
}

// NewStateManager 创建 PostgreSQL 版状态管理器。
func NewStateManager[T any](pool *pgxpool.Pool) *StateManager[T] {
	return &StateManager[T]{pool: pool}
}

// Get 获取任务状态。
func (s *StateManager[T]) Get(ctx context.Context, taskID string) (*core.State[T], error) {
	query := `
		SELECT state_json, version
		FROM framework_task_state
		WHERE task_id = $1
	`

	var stateJSON []byte
	var version int64

	err := s.pool.QueryRow(ctx, query, taskID).Scan(&stateJSON, &version)
	if err != nil {
		return nil, core.ErrTaskNotFound{TaskID: taskID}
	}

	var state core.State[T]
	if err := json.Unmarshal(stateJSON, &state); err != nil {
		return nil, err
	}

	state.Version = version
	return &state, nil
}

// Create 创建新任务状态。
func (s *StateManager[T]) Create(ctx context.Context, state *core.State[T]) error {
	// 序列化状态
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return err
	}

	query := `
		INSERT INTO framework_task_state (task_id, state_json, version, created_at, updated_at)
		VALUES ($1, $2, 0, $3, $3)
	`

	now := time.Now()
	_, err = s.pool.Exec(ctx, query, state.TaskID, stateJSON, now)
	if err != nil {
		return err
	}

	state.Version = 0
	return nil
}

// Update 更新任务状态（乐观锁）。
func (s *StateManager[T]) Update(ctx context.Context, state *core.State[T]) error {
	// 序列化状态
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return err
	}

	newVersion := state.Version + 1

	query := `
		UPDATE framework_task_state
		SET state_json = $1, version = $2, updated_at = $3
		WHERE task_id = $4 AND version = $5
	`

	result, err := s.pool.Exec(ctx, query,
		stateJSON,
		newVersion,
		time.Now(),
		state.TaskID,
		state.Version,
	)

	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		// 乐观锁冲突，读取当前版本
		current, err := s.Get(ctx, state.TaskID)
		if err != nil {
			return err
		}
		return core.ErrVersionConflict{
			TaskID:          state.TaskID,
			ExpectedVersion: state.Version,
			ActualVersion:   current.Version,
		}
	}

	state.Version = newVersion
	return nil
}

// Delete 删除任务状态。
func (s *StateManager[T]) Delete(ctx context.Context, taskID string) error {
	query := `DELETE FROM framework_task_state WHERE task_id = $1`

	result, err := s.pool.Exec(ctx, query, taskID)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return core.ErrTaskNotFound{TaskID: taskID}
	}

	return nil
}

// List 列出任务状态（分页）。
func (s *StateManager[T]) List(ctx context.Context, filter core.StateFilter) ([]*core.State[T], error) {
	// 构建查询
	query := `SELECT state_json, version FROM framework_task_state WHERE 1=1`
	args := []interface{}{}
	argIdx := 1

	// 应用过滤器（简化版，实际需要解析 JSONB）
	// PostgreSQL 的 JSONB 查询较复杂，这里仅做示例

	// 分页
	if filter.Limit > 0 {
		query += ` LIMIT $` + string(rune('0'+argIdx))
		args = append(args, filter.Limit)
		argIdx++
	}
	if filter.Offset > 0 {
		query += ` OFFSET $` + string(rune('0'+argIdx))
		args = append(args, filter.Offset)
		argIdx++
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*core.State[T]

	for rows.Next() {
		var stateJSON []byte
		var version int64

		if err := rows.Scan(&stateJSON, &version); err != nil {
			return nil, err
		}

		var state core.State[T]
		if err := json.Unmarshal(stateJSON, &state); err != nil {
			return nil, err
		}

		state.Version = version
		result = append(result, &state)
	}

	return result, nil
}
