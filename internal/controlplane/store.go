// Package controlplane 实现任务控制平面，允许人工干预运行中的任务。
package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Command 定义控制命令类型
type Command string

const (
	CommandAdjustGoal Command = "adjust_goal" // 调整任务目标
	CommandInjectMove Command = "inject_move" // 注入新的 Move
	CommandPause      Command = "pause"       // 暂停任务
	CommandResume     Command = "resume"      // 恢复任务
	CommandTerminate  Command = "terminate"   // 终止任务
)

// Status 定义事件处理状态
type Status string

const (
	StatusPending   Status = "pending"   // 待处理
	StatusProcessed Status = "processed" // 已处理
	StatusFailed    Status = "failed"    // 处理失败
)

// ControlEvent 是一条人工干预事件
type ControlEvent struct {
	ID          uuid.UUID       `json:"id"`
	TaskID      string          `json:"task_id"`
	Command     Command         `json:"command"`
	Payload     json.RawMessage `json:"payload"`
	CreatedAt   time.Time       `json:"created_at"`
	ProcessedAt *time.Time      `json:"processed_at,omitempty"`
	Status      Status          `json:"status"`
	Error       *string         `json:"error,omitempty"`
}

// AdjustGoalPayload 调整目标的参数
type AdjustGoalPayload struct {
	NewGoal string `json:"new_goal"`
}

// InjectMovePayload 注入 Move 的参数
type InjectMovePayload struct {
	Kind     string          `json:"kind"`      // enumerate/probe/exploit/escalate/persist
	Target   json.RawMessage `json:"target"`    // {domain, ref_kind, locator}
	Reason   string          `json:"reason"`    // 注入原因（人工判断）
	Priority int             `json:"priority"`  // 优先级
}

// Store 管理 task_control_event 表
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 创建 Store
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Create 创建控制事件
func (s *Store) Create(ctx context.Context, taskID string, command Command, payload json.RawMessage) (uuid.UUID, error) {
	if taskID == "" {
		return uuid.Nil, fmt.Errorf("controlplane: taskID 为空")
	}
	if command == "" {
		return uuid.Nil, fmt.Errorf("controlplane: command 为空")
	}

	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO task_control_event (task_id, command, payload)
		VALUES ($1, $2, $3)
		RETURNING id
	`, taskID, command, payload).Scan(&id)

	if err != nil {
		return uuid.Nil, fmt.Errorf("controlplane: insert failed: %w", err)
	}

	return id, nil
}

// ListPending 读取指定 Task 的待处理事件
func (s *Store) ListPending(ctx context.Context, taskID string) ([]ControlEvent, error) {
	if taskID == "" {
		return nil, fmt.Errorf("controlplane: taskID 为空")
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, task_id, command, payload, created_at, processed_at, status, error
		FROM task_control_event
		WHERE task_id = $1 AND status = 'pending'
		ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("controlplane: query failed: %w", err)
	}
	defer rows.Close()

	var events []ControlEvent
	for rows.Next() {
		var e ControlEvent
		if err := rows.Scan(&e.ID, &e.TaskID, &e.Command, &e.Payload, &e.CreatedAt, &e.ProcessedAt, &e.Status, &e.Error); err != nil {
			return nil, fmt.Errorf("controlplane: scan failed: %w", err)
		}
		events = append(events, e)
	}

	return events, rows.Err()
}

// MarkProcessed 标记事件为已处理
func (s *Store) MarkProcessed(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("controlplane: id 为空")
	}

	_, err := s.pool.Exec(ctx, `
		UPDATE task_control_event
		SET status = 'processed', processed_at = NOW()
		WHERE id = $1
	`, id)

	if err != nil {
		return fmt.Errorf("controlplane: mark processed failed: %w", err)
	}

	return nil
}

// MarkFailed 标记事件为处理失败
func (s *Store) MarkFailed(ctx context.Context, id uuid.UUID, errorMsg string) error {
	if id == uuid.Nil {
		return fmt.Errorf("controlplane: id 为空")
	}

	_, err := s.pool.Exec(ctx, `
		UPDATE task_control_event
		SET status = 'failed', processed_at = NOW(), error = $2
		WHERE id = $1
	`, id, errorMsg)

	if err != nil {
		return fmt.Errorf("controlplane: mark failed failed: %w", err)
	}

	return nil
}

// GetByID 读取单个事件
func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (ControlEvent, error) {
	if id == uuid.Nil {
		return ControlEvent{}, fmt.Errorf("controlplane: id 为空")
	}

	var e ControlEvent
	err := s.pool.QueryRow(ctx, `
		SELECT id, task_id, command, payload, created_at, processed_at, status, error
		FROM task_control_event
		WHERE id = $1
	`, id).Scan(&e.ID, &e.TaskID, &e.Command, &e.Payload, &e.CreatedAt, &e.ProcessedAt, &e.Status, &e.Error)

	if err != nil {
		return ControlEvent{}, fmt.Errorf("controlplane: get by id failed: %w", err)
	}

	return e, nil
}

// ListByTask 读取指定 Task 的所有控制事件（含历史）
func (s *Store) ListByTask(ctx context.Context, taskID string, limit int) ([]ControlEvent, error) {
	if taskID == "" {
		return nil, fmt.Errorf("controlplane: taskID 为空")
	}
	if limit <= 0 {
		limit = 100
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, task_id, command, payload, created_at, processed_at, status, error
		FROM task_control_event
		WHERE task_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("controlplane: query failed: %w", err)
	}
	defer rows.Close()

	var events []ControlEvent
	for rows.Next() {
		var e ControlEvent
		if err := rows.Scan(&e.ID, &e.TaskID, &e.Command, &e.Payload, &e.CreatedAt, &e.ProcessedAt, &e.Status, &e.Error); err != nil {
			return nil, fmt.Errorf("controlplane: scan failed: %w", err)
		}
		events = append(events, e)
	}

	return events, rows.Err()
}
