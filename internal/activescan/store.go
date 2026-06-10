package activescan

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 active_scan 表的所有持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT 路径的统一列序，与 scan() 字段顺序一一对应。
// error_message 用 COALESCE 把 NULL 折成空串（Scan.ErrorMessage 是 string 不接 NULL）。
const colsSelect = "id, brief, target_host, status, created_at, " +
	"ended_at, COALESCE(error_message, '')"

// Create 建一个新 active scan。
//
// brief 必填（用户自然语言任务简报）；targetHost 0045 起 NOT NULL DEFAULT 空串，
// 调用方未识别目标 host 时直接传空串。无唯一约束，可并发多个 active scan。
// 不设 expires_at：active 任务跑完即终态，无时间窗轮转。
func (s *Store) Create(ctx context.Context, brief, targetHost string) (Scan, error) {
	if brief == "" {
		return Scan{}, fmt.Errorf("create active scan: brief 必填")
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO active_scan (brief, target_host, status)
		VALUES ($1, $2, 'active')
		RETURNING `+colsSelect, brief, targetHost)
	var sc Scan
	if err := scan(row, &sc); err != nil {
		return Scan{}, fmt.Errorf("create active scan: %w", err)
	}
	return sc, nil
}

const (
	defaultListLimit = 20
	maxListLimit     = 200
)

// List 按 created_at DESC 列出最近的 active scans。
func (s *Store) List(ctx context.Context, limit int) ([]Scan, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	rows, err := s.pool.Query(ctx,
		"SELECT "+colsSelect+" FROM active_scan ORDER BY created_at DESC LIMIT $1",
		limit)
	if err != nil {
		return nil, fmt.Errorf("list active scans: %w", err)
	}
	defer rows.Close()

	var out []Scan
	for rows.Next() {
		var sc Scan
		if err := scan(rows, &sc); err != nil {
			return nil, fmt.Errorf("scan active scan: %w", err)
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// GetByID 按主键读取（不限 status）。
func (s *Store) GetByID(ctx context.Context, id string) (Scan, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+colsSelect+" FROM active_scan WHERE id=$1", id)
	var sc Scan
	if err := scan(row, &sc); err != nil {
		return Scan{}, fmt.Errorf("get active scan %s: %w", id, err)
	}
	return sc, nil
}

// Abort 把 scan 置为 aborted，写 ended_at / error_message。
// 0042 后无 *_count 兜底——读路径直接查附属表。
func (s *Store) Abort(ctx context.Context, id, errMsg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE active_scan SET
			status='aborted',
			ended_at=now(),
			error_message=$1
		WHERE id=$2`, errMsg, id)
	if err != nil {
		return fmt.Errorf("abort active scan %s: %w", id, err)
	}
	return nil
}

// SetTargetHost 回填 target_host（scanner 从 brief 抽到真实 host 时）。
// API 创建时留空（不在 API 层 parse brief）；幂等覆盖，best-effort 不阻塞扫描。
func (s *Store) SetTargetHost(ctx context.Context, id, host string) error {
	_, err := s.pool.Exec(ctx, `UPDATE active_scan SET target_host=$1 WHERE id=$2`, host, id)
	if err != nil {
		return fmt.Errorf("set target_host %s: %w", id, err)
	}
	return nil
}

// Complete 把 scan 置为 completed（orchestrator run 自然跑完的成功终态），写 ended_at。
// 与 Abort 区别：completed 无 error_message（成功收尾），aborted 带原因（用户停/取消/错误）。
func (s *Store) Complete(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE active_scan SET
			status='completed',
			ended_at=now()
		WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("complete active scan %s: %w", id, err)
	}
	return nil
}

// Reopen 把已终态（completed/aborted）的 scan 置回 active，清 ended_at/error_message。
// 用于多轮对话的动作续接：同一 scan 上重跑 agent，复用 owner 作用域黑板。
func (s *Store) Reopen(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE active_scan SET
			status='active',
			ended_at=NULL,
			error_message=NULL
		WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("reopen active scan %s: %w", id, err)
	}
	return nil
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scan 是 colsSelect 列序的统一反序列化点。
//
// target_host 用 *string 接住 NULL 列；空字符串语义在 Scan struct 层（empty = NULL）。
func scan(r scanner, sc *Scan) error {
	var targetHost *string
	if err := r.Scan(&sc.ID, &sc.Brief, &targetHost, &sc.Status,
		&sc.CreatedAt,
		&sc.EndedAt, &sc.ErrorMessage); err != nil {
		return err
	}
	if targetHost != nil {
		sc.TargetHost = *targetHost
	}
	return nil
}
