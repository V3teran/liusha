package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PGCheckpointStore 是基于 PostgreSQL 的 CheckpointStore 实现。
// 表结构见 db/migrations/0110_actor_checkpoint.up.sql。
type PGCheckpointStore struct{ pool *pgxpool.Pool }

// NewPGCheckpointStore 构造 PGCheckpointStore。
func NewPGCheckpointStore(pool *pgxpool.Pool) *PGCheckpointStore {
	return &PGCheckpointStore{pool: pool}
}

// Write UPSERT 一条 Checkpoint（同 task_id+action_id 只保留最新步）。
func (s *PGCheckpointStore) Write(ctx context.Context, cp Checkpoint) error {
	hyp, err := json.Marshal(cp.Hypotheses)
	if err != nil {
		hyp = []byte("[]")
	}
	const q = `
		INSERT INTO actor_checkpoint (task_id, action_id, step_idx, thought, hypotheses, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (task_id, action_id) DO UPDATE
		SET step_idx   = EXCLUDED.step_idx,
		    thought    = EXCLUDED.thought,
		    hypotheses = EXCLUDED.hypotheses,
		    created_at = EXCLUDED.created_at`
	createdAt := cp.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	_, err = s.pool.Exec(ctx, q, cp.TaskID, cp.ActionID, cp.StepIdx, cp.Thought, hyp, createdAt)
	if err != nil {
		return fmt.Errorf("checkpoint: write (task=%s action=%s): %w", cp.TaskID, cp.ActionID, err)
	}
	return nil
}

// Last 返回 (task_id, action_id) 对应的最新 Checkpoint；不存在时返回 nil, nil。
func (s *PGCheckpointStore) Last(ctx context.Context, taskID, actionID string) (*Checkpoint, error) {
	const q = `SELECT task_id, action_id, step_idx, thought, hypotheses, created_at
	           FROM actor_checkpoint WHERE task_id = $1 AND action_id = $2`
	row := s.pool.QueryRow(ctx, q, taskID, actionID)
	var cp Checkpoint
	var hyp []byte
	if err := row.Scan(&cp.TaskID, &cp.ActionID, &cp.StepIdx, &cp.Thought, &hyp, &cp.CreatedAt); err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("checkpoint: last (task=%s action=%s): %w", taskID, actionID, err)
	}
	_ = json.Unmarshal(hyp, &cp.Hypotheses)
	return &cp, nil
}

func isNoRows(err error) bool {
	return err != nil && err.Error() == "no rows in result set"
}
