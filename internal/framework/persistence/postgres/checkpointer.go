package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/V3teran/liusha/internal/framework/core"
)

// Checkpointer 是 PostgreSQL 版检查点管理器（生产用）。
type Checkpointer struct {
	pool *pgxpool.Pool
}

// NewCheckpointer 创建 PostgreSQL 版检查点管理器。
func NewCheckpointer(pool *pgxpool.Pool) *Checkpointer {
	return &Checkpointer{pool: pool}
}

// Save 保存检查点。
func (c *Checkpointer) Save(ctx context.Context, checkpoint core.Checkpoint) (core.CheckpointID, error) {
	// 生成 ID
	id := core.CheckpointID(uuid.New().String())
	checkpoint.ID = id
	checkpoint.CreatedAt = time.Now()

	// 计算大小
	data, err := json.Marshal(checkpoint)
	if err != nil {
		return "", err
	}
	checkpoint.SizeBytes = int64(len(data))

	// 序列化字段
	labelsJSON, _ := json.Marshal(checkpoint.Labels)

	query := `
		INSERT INTO framework_checkpoint (
			id, task_id, state_snapshot, phase, component_states, labels, size_bytes, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err = c.pool.Exec(ctx, query,
		id,
		checkpoint.TaskID,
		checkpoint.StateSnapshot,
		checkpoint.Phase,
		checkpoint.ComponentStates,
		labelsJSON,
		checkpoint.SizeBytes,
		checkpoint.CreatedAt,
	)

	if err != nil {
		return "", err
	}

	return id, nil
}

// Load 加载检查点。
func (c *Checkpointer) Load(ctx context.Context, id core.CheckpointID) (*core.Checkpoint, error) {
	query := `
		SELECT id, task_id, state_snapshot, phase, component_states, labels, size_bytes, created_at
		FROM framework_checkpoint
		WHERE id = $1
	`

	var checkpoint core.Checkpoint
	var labelsJSON []byte

	err := c.pool.QueryRow(ctx, query, id).Scan(
		&checkpoint.ID,
		&checkpoint.TaskID,
		&checkpoint.StateSnapshot,
		&checkpoint.Phase,
		&checkpoint.ComponentStates,
		&labelsJSON,
		&checkpoint.SizeBytes,
		&checkpoint.CreatedAt,
	)

	if err != nil {
		return nil, core.ErrCheckpointNotFound{CheckpointID: id}
	}

	// 反序列化 labels
	if len(labelsJSON) > 0 {
		json.Unmarshal(labelsJSON, &checkpoint.Labels)
	}

	return &checkpoint, nil
}

// List 列出任务的所有检查点（按时间倒序）。
func (c *Checkpointer) List(ctx context.Context, taskID string, limit int) ([]core.CheckpointMeta, error) {
	query := `
		SELECT id, task_id, phase, labels, created_at, size_bytes
		FROM framework_checkpoint
		WHERE task_id = $1
		ORDER BY created_at DESC
	`

	// 添加限制
	if limit > 0 {
		query += ` LIMIT $2`
	}

	var rows interface{ Close() }
	var err error

	if limit > 0 {
		rows, err = c.pool.Query(ctx, query, taskID, limit)
	} else {
		rows, err = c.pool.Query(ctx, query, taskID)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []core.CheckpointMeta

	for rows.(interface{ Next() bool }).Next() {
		var meta core.CheckpointMeta
		var labelsJSON []byte

		err := rows.(interface {
			Scan(dest ...interface{}) error
		}).Scan(
			&meta.ID,
			&meta.TaskID,
			&meta.Phase,
			&labelsJSON,
			&meta.CreatedAt,
			&meta.SizeBytes,
		)

		if err != nil {
			return nil, err
		}

		// 反序列化 labels
		if len(labelsJSON) > 0 {
			json.Unmarshal(labelsJSON, &meta.Labels)
		}

		result = append(result, meta)
	}

	return result, nil
}

// Delete 删除检查点。
func (c *Checkpointer) Delete(ctx context.Context, id core.CheckpointID) error {
	query := `DELETE FROM framework_checkpoint WHERE id = $1`

	result, err := c.pool.Exec(ctx, query, id)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return core.ErrCheckpointNotFound{CheckpointID: id}
	}

	return nil
}

// Latest 获取任务的最新检查点。
func (c *Checkpointer) Latest(ctx context.Context, taskID string) (*core.Checkpoint, error) {
	metas, err := c.List(ctx, taskID, 1)
	if err != nil {
		return nil, err
	}

	if len(metas) == 0 {
		return nil, core.ErrCheckpointNotFound{CheckpointID: core.CheckpointID(taskID + "-latest")}
	}

	return c.Load(ctx, metas[0].ID)
}

// Prune 清理过期检查点（保留最近 N 个）。
func (c *Checkpointer) Prune(ctx context.Context, taskID string, keepCount int) error {
	query := `
		DELETE FROM framework_checkpoint
		WHERE task_id = $1
		AND id NOT IN (
			SELECT id FROM framework_checkpoint
			WHERE task_id = $1
			ORDER BY created_at DESC
			LIMIT $2
		)
	`

	_, err := c.pool.Exec(ctx, query, taskID, keepCount)
	return err
}
