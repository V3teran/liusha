package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/window"
)

type Postgres struct{ pool *pgxpool.Pool }

func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

func (s *Postgres) CreateWindow(ctx context.Context, w window.AnalysisWindow) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO analysis_window (id, engagement_id, trigger_kind, flow_count, started_at)
         VALUES ($1, $2, $3, $4, $5)`,
		w.ID, w.EngagementID, string(w.TriggerKind), w.FlowCount, w.StartedAt,
	)
	if err != nil {
		return fmt.Errorf("创建 analysis_window 失败: %w", err)
	}
	return nil
}

func (s *Postgres) GetWindow(ctx context.Context, id uuid.UUID) (window.AnalysisWindow, error) {
	var w window.AnalysisWindow
	var kind string
	err := s.pool.QueryRow(ctx,
		`SELECT id, engagement_id, trigger_kind, COALESCE(flow_count,0), started_at, ended_at
         FROM analysis_window WHERE id = $1`, id,
	).Scan(&w.ID, &w.EngagementID, &kind, &w.FlowCount, &w.StartedAt, &w.EndedAt)
	if err != nil {
		return window.AnalysisWindow{}, fmt.Errorf("查询 analysis_window 失败: %w", err)
	}
	w.TriggerKind = window.TriggerKind(kind)
	return w, nil
}

func (s *Postgres) CreateTask(ctx context.Context, t window.AgentTask) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO agent_task (id, window_id, role, status, cost_micro_usd)
         VALUES ($1, $2, $3, $4, $5)`,
		t.ID, t.WindowID, t.Role, string(t.Status), t.CostMicroUSD,
	)
	if err != nil {
		return fmt.Errorf("创建 agent_task 失败: %w", err)
	}
	return nil
}

func (s *Postgres) ListTasks(ctx context.Context, windowID uuid.UUID) ([]window.AgentTask, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, window_id, role, status, cost_micro_usd
         FROM agent_task WHERE window_id = $1 ORDER BY ctid`, windowID,
	)
	if err != nil {
		return nil, fmt.Errorf("查询 agent_task 失败: %w", err)
	}
	defer rows.Close()
	var tasks []window.AgentTask
	for rows.Next() {
		var t window.AgentTask
		var status string
		if err := rows.Scan(&t.ID, &t.WindowID, &t.Role, &status, &t.CostMicroUSD); err != nil {
			return nil, err
		}
		t.Status = window.TaskStatus(status)
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}
