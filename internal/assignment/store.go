package assignment

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 assignment 表的持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT / RETURNING 路径的统一列序，与 scan() 字段一一对应。
const colsSelect = "id, source, payload, title, schedule_id, created_at"

const (
	defaultListLimit = 20
	maxListLimit     = 200
)

// Create 建一个新 assignment（下发容器）。Items 序列化进 payload jsonb。
// 调用方随后据返回的 assignment.ID 展开子 task（task.assignment_id 强外键）。
func (s *Store) Create(ctx context.Context, p NewParams) (Assignment, error) {
	switch p.Source {
	case SourceManual, SourceAuto:
	default:
		return Assignment{}, fmt.Errorf("create assignment: 非法 source %q", p.Source)
	}
	items := p.Items
	if items == nil {
		items = []Item{}
	}
	payload, err := json.Marshal(items)
	if err != nil {
		return Assignment{}, fmt.Errorf("marshal assignment payload: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO assignment (source, payload, title, schedule_id)
		VALUES ($1, $2, $3, $4)
		RETURNING `+colsSelect,
		string(p.Source), payload, p.Title, p.ScheduleID)
	var a Assignment
	if err := scan(row, &a); err != nil {
		return Assignment{}, fmt.Errorf("create assignment: %w", err)
	}
	return a, nil
}

// GetByID 按主键读取。
func (s *Store) GetByID(ctx context.Context, id string) (Assignment, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+colsSelect+" FROM assignment WHERE id=$1", id)
	var a Assignment
	if err := scan(row, &a); err != nil {
		return Assignment{}, fmt.Errorf("get assignment %s: %w", id, err)
	}
	return a, nil
}

// List 按 created_at DESC 列出最近的 assignment。scenarioID 为空时不过滤。
func (s *Store) List(ctx context.Context,  limit int) ([]Assignment, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	q := "SELECT " + colsSelect + " FROM assignment"
	args := []any{}
	if false {
		q += " WHERE scenario_id=$1"
		args = append(args)
	}
	q += " ORDER BY created_at DESC LIMIT $" + fmt.Sprint(len(args)+1)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list assignments: %w", err)
	}
	defer rows.Close()

	var out []Assignment
	for rows.Next() {
		var a Assignment
		if err := scan(rows, &a); err != nil {
			return nil, fmt.Errorf("scan assignment: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeriveStatus 读时从子 task 聚合派生 assignment 整体状态（不落存储）。
//
// 规则（见 spec §3.2 "整体状态由子 task 聚合派生"）：
//   - 无子 task            → empty
//   - 任一子 task status='active' → running
//   - 全部终态、含 completed     → done
//   - 全部终态、无 completed     → aborted
func (s *Store) DeriveStatus(ctx context.Context, assignmentID string) (Status, error) {
	var total, active, completed int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE status='active'),
		       count(*) FILTER (WHERE status='completed')
		FROM task WHERE assignment_id=$1`, assignmentID).Scan(&total, &active, &completed)
	if err != nil {
		return "", fmt.Errorf("derive assignment %s status: %w", assignmentID, err)
	}
	switch {
	case total == 0:
		return StatusEmpty, nil
	case active > 0:
		return StatusRunning, nil
	case completed > 0:
		return StatusDone, nil
	default:
		return StatusAborted, nil
	}
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scan 是 colsSelect 列序的统一反序列化点。
func scan(r scanner, a *Assignment) error {
	var source string
	if err := r.Scan(&a.ID, &source, &a.Payload, &a.Title,
		&a.ScheduleID, &a.CreatedAt); err != nil {
		return err
	}
	a.Source = Source(source)
	return nil
}
