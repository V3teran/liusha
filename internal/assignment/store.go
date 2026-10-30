package assignment

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 assignment 表的持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT / RETURNING 路径的统一列序，与 scan() 字段一一对应。
const colsSelect = "id, source, payload, title, description, schedule_id, created_at"

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
		INSERT INTO assignment (source, payload, title, description, schedule_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+colsSelect,
		string(p.Source), payload, p.Title, p.Description, p.ScheduleID)
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

// scan 是 colsSelect 列序的统一反序列化点。
func scan(r db.RowScanner, a *Assignment) error {
	var source string
	if err := r.Scan(&a.ID, &source, &a.Payload, &a.Title, &a.Description,
		&a.ScheduleID, &a.CreatedAt); err != nil {
		return err
	}
	a.Source = Source(source)
	return nil
}
