// Package executionplan 管理 execution_plan 表：Planner 产出的待执行 Move。
// 与世界模型（已观察状态）分离，支持 PAE 循环的 Plan 阶段。
package executionplan

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/worldmodel"
)

// MoveKind 对齐杀伤链阶段（与 internal/planner.MoveKind 一致）
type MoveKind string

const (
	MoveEnumerate MoveKind = "enumerate"
	MoveProbe     MoveKind = "probe"
	MoveExploit   MoveKind = "exploit"
	MoveEscalate  MoveKind = "escalate"
	MovePersist   MoveKind = "persist"
)

// Status 表示 Move 执行状态
type Status string

const (
	StatusPending   Status = "pending"
	StatusExecuting Status = "executing"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

// Move 表示一个待执行的探索动作
type Move struct {
	ID        uuid.UUID              `json:"id"`
	TaskID    string                 `json:"task_id"`
	Kind      MoveKind               `json:"kind"`
	Domain    string                 `json:"domain"`
	TargetRef worldmodel.TargetRef   `json:"target_ref"`
	Priority  int                    `json:"priority"`
	Status    Status                 `json:"status"`
	DependsOn []uuid.UUID            `json:"depends_on,omitempty"`
	Reason    string                 `json:"reason"`
	CreatedAt time.Time              `json:"created_at"`
	StartedAt *time.Time             `json:"started_at,omitempty"`
	CompletedAt *time.Time           `json:"completed_at,omitempty"`
	ErrorMessage string               `json:"error_message,omitempty"`
}

// Store 管理 execution_plan 表的 CRUD
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 创建 execution_plan store
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Create 创建一个新的 Move（Planner 调用）
func (s *Store) Create(ctx context.Context, m Move) (Move, error) {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	if m.Priority == 0 {
		m.Priority = 5 // 默认优先级
	}
	if m.Status == "" {
		m.Status = StatusPending
	}

	targetRefJSON, err := json.Marshal(m.TargetRef)
	if err != nil {
		return Move{}, fmt.Errorf("marshal target_ref: %w", err)
	}

	query := `
		INSERT INTO execution_plan (id, task_id, kind, domain, target_ref, priority, status, depends_on, reason, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
		RETURNING id, created_at
	`
	err = s.pool.QueryRow(ctx, query,
		m.ID, m.TaskID, m.Kind, m.Domain, targetRefJSON, m.Priority, m.Status, m.DependsOn, m.Reason,
	).Scan(&m.ID, &m.CreatedAt)
	if err != nil {
		return Move{}, fmt.Errorf("insert execution_plan: %w", err)
	}

	return m, nil
}

// ListPending 获取指定 task 的所有待执行 Move（按优先级降序）
func (s *Store) ListPending(ctx context.Context, taskID string) ([]Move, error) {
	query := `
		SELECT id, task_id, kind, domain, target_ref, priority, status, depends_on, reason,
		       created_at, started_at, completed_at, error_message
		FROM execution_plan
		WHERE task_id = $1 AND status = $2
		ORDER BY priority DESC, created_at ASC
	`
	rows, err := s.pool.Query(ctx, query, taskID, StatusPending)
	if err != nil {
		return nil, fmt.Errorf("query pending moves: %w", err)
	}
	defer rows.Close()

	return s.scanMoves(rows)
}

// ListAll 获取指定 task 的所有 Move（按创建时间升序）
func (s *Store) ListAll(ctx context.Context, taskID string) ([]Move, error) {
	query := `
		SELECT id, task_id, kind, domain, target_ref, priority, status, depends_on, reason,
		       created_at, started_at, completed_at, error_message
		FROM execution_plan
		WHERE task_id = $1
		ORDER BY created_at ASC
	`
	rows, err := s.pool.Query(ctx, query, taskID)
	if err != nil {
		return nil, fmt.Errorf("query all moves: %w", err)
	}
	defer rows.Close()

	return s.scanMoves(rows)
}

// GetByID 根据 ID 获取单个 Move
func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (Move, error) {
	query := `
		SELECT id, task_id, kind, domain, target_ref, priority, status, depends_on, reason,
		       created_at, started_at, completed_at, error_message
		FROM execution_plan
		WHERE id = $1
	`
	row := s.pool.QueryRow(ctx, query, id)
	return s.scanMove(row)
}

// MarkExecuting 标记 Move 为执行中
func (s *Store) MarkExecuting(ctx context.Context, id uuid.UUID) error {
	query := `
		UPDATE execution_plan
		SET status = $1, started_at = now()
		WHERE id = $2 AND status = $3
	`
	tag, err := s.pool.Exec(ctx, query, StatusExecuting, id, StatusPending)
	if err != nil {
		return fmt.Errorf("mark executing: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("move not found or already executing")
	}
	return nil
}

// MarkCompleted 标记 Move 为完成
func (s *Store) MarkCompleted(ctx context.Context, id uuid.UUID) error {
	query := `
		UPDATE execution_plan
		SET status = $1, completed_at = now()
		WHERE id = $2 AND status = $3
	`
	tag, err := s.pool.Exec(ctx, query, StatusCompleted, id, StatusExecuting)
	if err != nil {
		return fmt.Errorf("mark completed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("move not found or not executing")
	}
	return nil
}

// MarkFailed 标记 Move 为失败
func (s *Store) MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error {
	query := `
		UPDATE execution_plan
		SET status = $1, completed_at = now(), error_message = $2
		WHERE id = $3 AND status = $4
	`
	tag, err := s.pool.Exec(ctx, query, StatusFailed, errMsg, id, StatusExecuting)
	if err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("move not found or not executing")
	}
	return nil
}

// DeleteByTaskID 删除指定 task 的所有 Move（任务结束清理）
func (s *Store) DeleteByTaskID(ctx context.Context, taskID string) error {
	query := `DELETE FROM execution_plan WHERE task_id = $1`
	_, err := s.pool.Exec(ctx, query, taskID)
	if err != nil {
		return fmt.Errorf("delete moves: %w", err)
	}
	return nil
}

// scanMove 扫描单行
func (s *Store) scanMove(row pgx.Row) (Move, error) {
	var m Move
	var targetRefJSON []byte
	err := row.Scan(
		&m.ID, &m.TaskID, &m.Kind, &m.Domain, &targetRefJSON, &m.Priority, &m.Status, &m.DependsOn, &m.Reason,
		&m.CreatedAt, &m.StartedAt, &m.CompletedAt, &m.ErrorMessage,
	)
	if err != nil {
		return Move{}, err
	}

	if err := json.Unmarshal(targetRefJSON, &m.TargetRef); err != nil {
		return Move{}, fmt.Errorf("unmarshal target_ref: %w", err)
	}

	return m, nil
}

// scanMoves 扫描多行
func (s *Store) scanMoves(rows pgx.Rows) ([]Move, error) {
	var moves []Move
	for rows.Next() {
		var m Move
		var targetRefJSON []byte
		err := rows.Scan(
			&m.ID, &m.TaskID, &m.Kind, &m.Domain, &targetRefJSON, &m.Priority, &m.Status, &m.DependsOn, &m.Reason,
			&m.CreatedAt, &m.StartedAt, &m.CompletedAt, &m.ErrorMessage,
		)
		if err != nil {
			return nil, fmt.Errorf("scan move: %w", err)
		}

		if err := json.Unmarshal(targetRefJSON, &m.TargetRef); err != nil {
			return nil, fmt.Errorf("unmarshal target_ref: %w", err)
		}

		moves = append(moves, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return moves, nil
}
