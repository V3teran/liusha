package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/framework/core"
)

// Checkpointer 基于 PostgreSQL 的检查点存储实现。
// 支持保存、加载、列表、删除和清理操作。
type Checkpointer struct {
	pool *pgxpool.Pool
}

// NewCheckpointer 构造 PostgreSQL Checkpointer。
func NewCheckpointer(pool *pgxpool.Pool) *Checkpointer {
	return &Checkpointer{pool: pool}
}

// Save 保存检查点，返回生成的 ID。
func (c *Checkpointer) Save(ctx context.Context, checkpoint core.Checkpoint) (core.CheckpointID, error) {
	// 生成 ID
	if checkpoint.ID == "" {
		checkpoint.ID = core.CheckpointID(uuid.New().String())
	}

	// 设置创建时间
	if checkpoint.CreatedAt.IsZero() {
		checkpoint.CreatedAt = time.Now()
	}

	// 计算大小
	data, err := json.Marshal(checkpoint)
	if err != nil {
		return "", fmt.Errorf("checkpoint: marshal: %w", err)
	}
	checkpoint.SizeBytes = int64(len(data))

	// 转换 ComponentStates 为 JSON
	var componentStates []byte
	if checkpoint.ComponentStates != nil {
		componentStates, _ = json.Marshal(checkpoint.ComponentStates)
	} else {
		componentStates = []byte("{}")
	}

	// 转换 Labels 为 JSON
	var labels []byte
	if checkpoint.Labels != nil {
		labels, _ = json.Marshal(checkpoint.Labels)
	} else {
		labels = []byte("{}")
	}

	const q = `
		INSERT INTO checkpoints (id, task_id, state_snapshot, phase, component_states, labels, size_bytes, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE
		SET state_snapshot = EXCLUDED.state_snapshot,
		    phase = EXCLUDED.phase,
		    component_states = EXCLUDED.component_states,
		    labels = EXCLUDED.labels,
		    size_bytes = EXCLUDED.size_bytes,
		    created_at = EXCLUDED.created_at`

	_, err = c.pool.Exec(ctx, q,
		checkpoint.ID,
		checkpoint.TaskID,
		checkpoint.StateSnapshot,
		checkpoint.Phase,
		componentStates,
		labels,
		checkpoint.SizeBytes,
		checkpoint.CreatedAt,
	)
	if err != nil {
		return "", fmt.Errorf("checkpoint: save (task=%s id=%s): %w", checkpoint.TaskID, checkpoint.ID, err)
	}

	return checkpoint.ID, nil
}

// Load 加载指定检查点。
func (c *Checkpointer) Load(ctx context.Context, id core.CheckpointID) (*core.Checkpoint, error) {
	const q = `
		SELECT id, task_id, state_snapshot, phase, component_states, labels, size_bytes, created_at
		FROM checkpoints WHERE id = $1`

	row := c.pool.QueryRow(ctx, q, id)
	var cp core.Checkpoint
	var componentStatesJSON []byte
	var labelsJSON []byte

	err := row.Scan(&cp.ID, &cp.TaskID, &cp.StateSnapshot, &cp.Phase, &componentStatesJSON, &labelsJSON, &cp.SizeBytes, &cp.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("checkpoint: load (id=%s): %w", id, err)
	}

	// 解析 ComponentStates
	if len(componentStatesJSON) > 0 {
		cp.ComponentStates = make(map[string]json.RawMessage)
		_ = json.Unmarshal(componentStatesJSON, &cp.ComponentStates)
	}

	// 解析 Labels
	if len(labelsJSON) > 0 {
		cp.Labels = make(map[string]string)
		_ = json.Unmarshal(labelsJSON, &cp.Labels)
	}

	return &cp, nil
}

// List 列出任务的所有检查点（按时间倒序）。
func (c *Checkpointer) List(ctx context.Context, taskID string, limit int) ([]core.CheckpointMeta, error) {
	if limit <= 0 {
		limit = 100
	}

	const q = `
		SELECT id, task_id, phase, labels, size_bytes, created_at
		FROM checkpoints
		WHERE task_id = $1
		ORDER BY created_at DESC
		LIMIT $2`

	rows, err := c.pool.Query(ctx, q, taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: list (task=%s): %w", taskID, err)
	}
	defer rows.Close()

	var metas []core.CheckpointMeta
	for rows.Next() {
		var meta core.CheckpointMeta
		var labelsJSON []byte

		err := rows.Scan(&meta.ID, &meta.TaskID, &meta.Phase, &labelsJSON, &meta.SizeBytes, &meta.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("checkpoint: list scan (task=%s): %w", taskID, err)
		}

		// 解析 Labels
		if len(labelsJSON) > 0 {
			meta.Labels = make(map[string]string)
			_ = json.Unmarshal(labelsJSON, &meta.Labels)
		}

		metas = append(metas, meta)
	}

	return metas, rows.Err()
}

// Latest 获取任务的最新检查点。
func (c *Checkpointer) Latest(ctx context.Context, taskID string) (*core.Checkpoint, error) {
	const q = `
		SELECT id, task_id, state_snapshot, phase, component_states, labels, size_bytes, created_at
		FROM checkpoints
		WHERE task_id = $1
		ORDER BY created_at DESC
		LIMIT 1`

	row := c.pool.QueryRow(ctx, q, taskID)
	var cp core.Checkpoint
	var componentStatesJSON []byte
	var labelsJSON []byte

	err := row.Scan(&cp.ID, &cp.TaskID, &cp.StateSnapshot, &cp.Phase, &componentStatesJSON, &labelsJSON, &cp.SizeBytes, &cp.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("checkpoint: latest (task=%s): %w", taskID, err)
	}

	// 解析 ComponentStates
	if len(componentStatesJSON) > 0 {
		cp.ComponentStates = make(map[string]json.RawMessage)
		_ = json.Unmarshal(componentStatesJSON, &cp.ComponentStates)
	}

	// 解析 Labels
	if len(labelsJSON) > 0 {
		cp.Labels = make(map[string]string)
		_ = json.Unmarshal(labelsJSON, &cp.Labels)
	}

	return &cp, nil
}

// Delete 删除检查点。
func (c *Checkpointer) Delete(ctx context.Context, id core.CheckpointID) error {
	const q = `DELETE FROM checkpoints WHERE id = $1`

	_, err := c.pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("checkpoint: delete (id=%s): %w", id, err)
	}

	return nil
}

// Prune 清理过期检查点（保留最近 N 个）。
func (c *Checkpointer) Prune(ctx context.Context, taskID string, keepCount int) error {
	if keepCount <= 0 {
		keepCount = 1
	}

	const q = `
		DELETE FROM checkpoints
		WHERE id IN (
			SELECT id FROM checkpoints
			WHERE task_id = $1
			ORDER BY created_at DESC
			OFFSET $2
		)`

	_, err := c.pool.Exec(ctx, q, taskID, keepCount)
	if err != nil {
		return fmt.Errorf("checkpoint: prune (task=%s keep=%d): %w", taskID, keepCount, err)
	}

	return nil
}

func isNoRows(err error) bool {
	return err != nil && err.Error() == "no rows in result set"
}
