package lesson

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 lesson 表的所有持久化操作。
type Store struct{ pool *pgxpool.Pool }

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT / RETURNING 路径的统一列序，与 scan() 字段一一对应。
// v0030：删 tenant_id（单租户）；v0031：删 source_owner_id / source_finding_id /
// structured_payload（实测从未写入实际值的死字段）。
const colsSelect = "id, host, kind, content, content_hash, priority, hit_count, created_at, updated_at"

// ContentHash 计算给定 content 的 SHA-256 hex 字符串（64 字符）。
//
// 内部 normalize：strings.TrimSpace。LLM 输出可能带前后空白。
func ContentHash(content string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(content)))
	return hex.EncodeToString(sum[:])
}

// Add UPSERT by (host, content_hash)：
//   - 新内容 → INSERT，返回新建 Lesson
//   - 重复内容 → UPDATE：priority 取较大值；hit_count++；updated_at = now()
//
// caller 必填：Host、Kind（KindLesson 或 KindHint）、Content。
// Priority 越界（< 1 或 > 10）clamp 到默认值 5。
func (s *Store) Add(ctx context.Context, l Lesson) (Lesson, error) {
	if l.Host == "" {
		return Lesson{}, fmt.Errorf("lesson.Add: Host 必填")
	}
	if l.Kind == "" {
		return Lesson{}, fmt.Errorf("lesson.Add: Kind 必填（KindLesson | KindHint）")
	}
	if l.Content == "" {
		return Lesson{}, fmt.Errorf("lesson.Add: Content 必填")
	}
	if l.Priority < 1 || l.Priority > 10 {
		l.Priority = 5
	}
	l.ContentHash = ContentHash(l.Content)

	row := s.pool.QueryRow(ctx, `
		INSERT INTO lesson
			(host, kind, content, content_hash, priority)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (host, content_hash) DO UPDATE
		  SET priority   = GREATEST(lesson.priority, EXCLUDED.priority),
		      hit_count  = lesson.hit_count + 1,
		      updated_at = now()
		RETURNING `+colsSelect,
		l.Host, l.Kind, l.Content, l.ContentHash, l.Priority)

	var saved Lesson
	if err := scan(row, &saved); err != nil {
		return Lesson{}, fmt.Errorf("save lesson: %w", err)
	}
	return saved, nil
}

// ListByHost 按 host 拉 top-N lesson（含全部 kind），按 priority desc, updated_at desc。
//
// 不按 kind 过滤——caller 需要分段渲染时自行按 l.Kind 区分。
// limit ≤ 0 用默认 20。host 必填（空时报错）。
func (s *Store) ListByHost(ctx context.Context, host string, limit int) ([]Lesson, error) {
	if host == "" {
		return nil, fmt.Errorf("lesson.ListByHost: host 必填")
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+colsSelect+`
		FROM lesson
		WHERE host=$1
		ORDER BY priority DESC, updated_at DESC
		LIMIT $2`, host, limit)
	if err != nil {
		return nil, fmt.Errorf("list lessons by host: %w", err)
	}
	defer rows.Close()

	var out []Lesson
	for rows.Next() {
		var l Lesson
		if err := scan(rows, &l); err != nil {
			return nil, fmt.Errorf("scan lesson: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lessons: %w", err)
	}
	return out, nil
}

// ListGlobalHints 拉 (host=HostGlobalHint, kind=KindHint) 的全局业务规则 hint top-N。
//
// 设计意图：业务规则数据化——把硬规则迁到 lesson 表 (host='*', kind='hint') 行，
// 启动时由 SeedDefaultHints 同步入库；hunter 装配 prompt 时与具体 host lesson 一起渲染。
//
// limit ≤ 0 用默认 20。
func (s *Store) ListGlobalHints(ctx context.Context, limit int) ([]Lesson, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+colsSelect+`
		FROM lesson
		WHERE host=$1 AND kind=$2
		ORDER BY priority DESC, updated_at DESC
		LIMIT $3`, HostGlobalHint, KindHint, limit)
	if err != nil {
		return nil, fmt.Errorf("list global hints: %w", err)
	}
	defer rows.Close()

	var out []Lesson
	for rows.Next() {
		var l Lesson
		if err := scan(rows, &l); err != nil {
			return nil, fmt.Errorf("scan global hint: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate global hints: %w", err)
	}
	return out, nil
}

// Prune 保留 host 下 top-N 条 lesson（按 priority desc, updated_at desc），
// 超出的删除。用于后台 LRU eviction，避免单 host lesson 无限增长。
//
// keep ≤ 0 时跳过（无操作）；空 host 跳过。
func (s *Store) Prune(ctx context.Context, host string, keep int) error {
	if host == "" {
		return fmt.Errorf("lesson.Prune: host 必填")
	}
	if keep <= 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		DELETE FROM lesson
		WHERE id IN (
			SELECT id FROM lesson
			WHERE host=$1
			ORDER BY priority DESC, updated_at DESC
			OFFSET $2
		)`, host, keep)
	if err != nil {
		return fmt.Errorf("prune lessons: %w", err)
	}
	return nil
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scan 是 colsSelect 列序的统一反序列化点。
func scan(r scanner, l *Lesson) error {
	return r.Scan(
		&l.ID, &l.Host, &l.Kind, &l.Content, &l.ContentHash,
		&l.Priority, &l.HitCount, &l.CreatedAt, &l.UpdatedAt,
	)
}
