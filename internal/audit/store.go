package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/db"
)

// Store 封装 audit_log 表的所有持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Append 单条插入审计事件。
// 必填：Actor / Action / TargetKind / TargetID；Metadata 为空时落 '{}'。
func (s *Store) Append(ctx context.Context, e Event) (int64, error) {
	if e.Actor == "" || e.Action == "" {
		return 0, fmt.Errorf("audit: Actor + Action 必填")
	}
	if e.TargetKind == "" || e.TargetID == "" {
		return 0, fmt.Errorf("audit: TargetKind + TargetID 必填")
	}
	meta := e.Metadata
	if len(meta) == 0 {
		meta = json.RawMessage("{}")
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO audit_log (actor, action, target_kind, target_id, metadata)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		e.Actor, e.Action, e.TargetKind, e.TargetID, meta,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("append audit: %w", err)
	}
	return id, nil
}

// Filter 是审计事件的列表筛选：零值字段不参与过滤。Actor 是搜索框输入（按字面包含匹配，
// 经 EscapeLike 转义）；Start/End 零值表示不限。分页沿用全站 offset 语义（Limit=0 → 缺省）。
type Filter struct {
	Actor      string
	Action     string
	TargetKind string
	TargetID   string
	Start      time.Time
	End        time.Time
	Limit      int
	Offset     int
}

// Page 是一页审计事件与总数（total 是筛选全集的行数，与翻页无关）。
type Page struct {
	Items []Event
	Total int
}

// ListFilter 缺省值。audit_log 量级极小（每次敏感操作一行），不设更小的上限。
const (
	defaultListLimit = 50
	maxListLimit     = 500
)

// List 按筛选列审计事件，created_at 倒序。total 恒为筛选全集行数。
func (s *Store) List(ctx context.Context, f Filter) (Page, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	conds := []string{"1=1"}
	args := []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, strings.Replace(cond, "?", "$"+fmt.Sprint(len(args)), 1))
	}
	if actor := strings.TrimSpace(f.Actor); actor != "" {
		add("actor ILIKE ?", db.ContainsPattern(actor))
	}
	if f.Action != "" {
		add("action = ?", f.Action)
	}
	if f.TargetKind != "" {
		add("target_kind = ?", f.TargetKind)
	}
	if f.TargetID != "" {
		add("target_id = ?", f.TargetID)
	}
	if !f.Start.IsZero() {
		add("created_at >= ?", f.Start)
	}
	if !f.End.IsZero() {
		add("created_at < ?", f.End)
	}
	where := strings.Join(conds, " AND ")

	var total int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM audit_log WHERE "+where, args...).Scan(&total); err != nil {
		return Page{}, fmt.Errorf("count audit_log: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, actor, action, target_kind, target_id, metadata, created_at
		FROM audit_log WHERE `+where+`
		ORDER BY created_at DESC, id DESC
		LIMIT $`+fmt.Sprint(len(args)+1)+` OFFSET $`+fmt.Sprint(len(args)+2),
		append(args, limit, offset)...)
	if err != nil {
		return Page{}, fmt.Errorf("list audit_log: %w", err)
	}
	defer rows.Close()

	items := make([]Event, 0, limit)
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Actor, &e.Action, &e.TargetKind, &e.TargetID, &e.Metadata, &e.CreatedAt); err != nil {
			return Page{}, fmt.Errorf("scan audit event: %w", err)
		}
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("list audit_log: %w", err)
	}
	return Page{Items: items, Total: total}, nil
}
