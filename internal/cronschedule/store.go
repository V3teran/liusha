package cronschedule

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"

	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/db"
)

// Store 封装 cron_schedule 表的持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT / RETURNING 路径的统一列序，与 scan() 字段一一对应。
const colsSelect = "id, cron_expr, payload, title, description, enabled, next_run_at, last_run_at, created_at"

const (
	defaultListLimit = 20
	maxListLimit     = 200
)

// NextRun 解析标准 5 段 cron 表达式，返回 from 之后的下一次触发时间。
func NextRun(expr string, from time.Time) (time.Time, error) {
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return time.Time{}, fmt.Errorf("解析 cron_expr %q: %w", expr, err)
	}
	return sched.Next(from), nil
}

// Create 建一个新定时模板；cron_expr 当场校验并算出首次 next_run_at。
func (s *Store) Create(ctx context.Context, p NewParams) (CronSchedule, error) {
	if p.CronExpr == "" {
		return CronSchedule{}, fmt.Errorf("create cron_schedule: cron_expr 必填")
	}
	next, err := NextRun(p.CronExpr, time.Now())
	if err != nil {
		return CronSchedule{}, err
	}
	items := p.Items
	if items == nil {
		items = []assignment.Item{}
	}
	payload, err := json.Marshal(items)
	if err != nil {
		return CronSchedule{}, fmt.Errorf("marshal cron_schedule payload: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO cron_schedule (cron_expr, payload, title, description, next_run_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+colsSelect,
		p.CronExpr, payload, p.Title, p.Description, next)
	var c CronSchedule
	if err := scan(row, &c); err != nil {
		return CronSchedule{}, fmt.Errorf("create cron_schedule: %w", err)
	}
	return c, nil
}

// GetByID 按主键读取。
func (s *Store) GetByID(ctx context.Context, id string) (CronSchedule, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+colsSelect+" FROM cron_schedule WHERE id=$1", id)
	var c CronSchedule
	if err := scan(row, &c); err != nil {
		return CronSchedule{}, fmt.Errorf("get cron_schedule %s: %w", id, err)
	}
	return c, nil
}

// SetEnabled 切换定时模板的启用/停用（定时模板本身无终态，只有这一个开关）。
func (s *Store) SetEnabled(ctx context.Context, id string, enabled bool) error {
	tag, err := s.pool.Exec(ctx, "UPDATE cron_schedule SET enabled=$1 WHERE id=$2", enabled, id)
	if err != nil {
		return fmt.Errorf("set cron_schedule %s enabled=%v: %w", id, enabled, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("cron_schedule %s not found", id)
	}
	return nil
}

// ListDue 返回到点（enabled 且 next_run_at <= now）的定时模板，按 next_run_at 升序
// （最该先跑的排前面）。Scheduler 每轮 tick 调用。
func (s *Store) ListDue(ctx context.Context, now time.Time) ([]CronSchedule, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT "+colsSelect+" FROM cron_schedule WHERE enabled AND next_run_at <= $1 ORDER BY next_run_at", now)
	if err != nil {
		return nil, fmt.Errorf("list due cron_schedules: %w", err)
	}
	defer rows.Close()

	var out []CronSchedule
	for rows.Next() {
		var c CronSchedule
		if err := scan(rows, &c); err != nil {
			return nil, fmt.Errorf("scan cron_schedule: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkFired 触发后回写 last_run_at=firedAt，并按该模板的 cron_expr 推算下一次
// next_run_at。expectedNextRunAt 是触发方看到的到点时间（ListDue 返回值）：
// 单条 UPDATE 带此前置条件做乐观锁——多副本同时到点时只有一家改写成功，
// 其余收到 false 跳过，杜绝同一模板重复触发整个批次。
func (s *Store) MarkFired(ctx context.Context, id string, firedAt, expectedNextRunAt time.Time) (bool, error) {
	c, err := s.GetByID(ctx, id)
	if err != nil {
		return false, err
	}
	next, err := NextRun(c.CronExpr, firedAt)
	if err != nil {
		return false, err
	}
	tag, err := s.pool.Exec(ctx,
		"UPDATE cron_schedule SET last_run_at=$1, next_run_at=$2 WHERE id=$3 AND next_run_at=$4",
		firedAt, next, id, expectedNextRunAt,
	)
	if err != nil {
		return false, fmt.Errorf("mark cron_schedule %s fired: %w", id, err)
	}
	return tag.RowsAffected() > 0, nil
}

// scan 是 colsSelect 列序的统一反序列化点。
func scan(r db.RowScanner, c *CronSchedule) error {
	if err := r.Scan(&c.ID, &c.CronExpr, &c.Payload, &c.Title, &c.Description,
		&c.Enabled, &c.NextRunAt, &c.LastRunAt, &c.CreatedAt); err != nil {
		return err
	}
	return nil
}
