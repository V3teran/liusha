package lesson

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 host_lesson 表的所有持久化操作。
type Store struct{ pool *pgxpool.Pool }

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT / RETURNING 路径的统一列序，与 scan() 字段一一对应。
const colsSelect = "id, tenant_id, host, content, content_hash, priority, source_engagement_id, source_finding_id, hit_count, created_at, updated_at"

// ContentHash 计算给定 content 的 SHA-256 hex 字符串（64 字符）。
//
// 内部 normalize：strings.TrimSpace。LLM 输出可能带前后空白。
func ContentHash(content string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(content)))
	return hex.EncodeToString(sum[:])
}

// Add UPSERT by (tenant, host, content_hash)：
//   - 新内容 → INSERT，返回新建 Lesson
//   - 重复内容 → UPDATE：priority 取较大值；hit_count++；updated_at = now()
//
// caller 需在 Lesson 中提供：TenantID（空 → "default"）、Host、Content、Priority；
// SourceEngagementID/SourceFindingID 可空（仅追溯用）；其他字段由 store 填充。
func (s *Store) Add(ctx context.Context, l Lesson) (Lesson, error) {
	if l.Host == "" {
		return Lesson{}, fmt.Errorf("lesson.Add: Host 必填")
	}
	if l.Content == "" {
		return Lesson{}, fmt.Errorf("lesson.Add: Content 必填")
	}
	if l.TenantID == "" {
		l.TenantID = "default"
	}
	if l.Priority < 1 || l.Priority > 10 {
		l.Priority = 5
	}
	l.ContentHash = ContentHash(l.Content)
	if len(l.Payload) == 0 {
		l.Payload = []byte("{}")
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO host_lesson
			(tenant_id, host, content, content_hash, priority, source_engagement_id, source_finding_id, payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (tenant_id, host, content_hash) DO UPDATE
		  SET priority   = GREATEST(host_lesson.priority, EXCLUDED.priority),
		      hit_count  = host_lesson.hit_count + 1,
		      payload    = EXCLUDED.payload,
		      updated_at = now()
		RETURNING `+colsSelect,
		l.TenantID, l.Host, l.Content, l.ContentHash, l.Priority,
		l.SourceEngagementID, l.SourceFindingID, l.Payload)

	var saved Lesson
	if err := scan(row, &saved); err != nil {
		return Lesson{}, fmt.Errorf("save lesson: %w", err)
	}
	return saved, nil
}

// ListByHost 按 (tenant, host) 拉 top-N lesson，priority desc, updated_at desc。
//
// limit ≤ 0 用默认 20；为空 host 返空切片（不报错）。
func (s *Store) ListByHost(ctx context.Context, tenant, host string, limit int) ([]Lesson, error) {
	if host == "" {
		return nil, nil
	}
	if tenant == "" {
		tenant = "default"
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+colsSelect+`
		FROM host_lesson
		WHERE tenant_id=$1 AND host=$2
		ORDER BY priority DESC, updated_at DESC
		LIMIT $3`, tenant, host, limit)
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

// TouchByDedup 按 (host, dedup_key) 找首发 finding 关联的 lesson，hit_count+1。
//
// v1.2 关键修复：finding 改 append-only 后，重发现的 finding.ID 是新行 ID，
// 与 host_lesson.source_finding_id（指向首发 finding）永远不匹配，旧的
// TouchByFinding(findingID) 永远 0 update。改成按 (host, dedup_key) 反查首发 ID 即可。
//
// 触发时机：vulnfinding.Store.OnReSaved（重发现路径）—— 表示"这条经验对应的漏洞被
// 又一次扫描验证存在"。hit_count 反映复用次数，可作为可信度排序依据。
//
// host 或 dedupKey 空时跳过；首发 finding 已被 hard delete 或没蒸馏过 lesson 时
// 静默 0 update（不报错）。
func (s *Store) TouchByDedup(ctx context.Context, host, dedupKey string) (int64, error) {
	if host == "" || dedupKey == "" {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE host_lesson SET hit_count = hit_count + 1, updated_at = now()
		WHERE source_finding_id IN (
			SELECT id FROM finding
			WHERE host=$1 AND dedup_key=$2
			ORDER BY created_at ASC
			LIMIT 1
		)`, host, dedupKey)
	if err != nil {
		return 0, fmt.Errorf("touch lessons by dedup %s/%s: %w", host, dedupKey, err)
	}
	return tag.RowsAffected(), nil
}

// Prune 保留 (tenant, host) 下 top-N 条 lesson（按 priority desc, updated_at desc），
// 超出的删除。用于后台 LRU eviction，避免单 host lesson 无限增长。
//
// keep ≤ 0 时跳过（无操作）；空 host 跳过。
func (s *Store) Prune(ctx context.Context, tenant, host string, keep int) error {
	if host == "" || keep <= 0 {
		return nil
	}
	if tenant == "" {
		tenant = "default"
	}
	_, err := s.pool.Exec(ctx, `
		DELETE FROM host_lesson
		WHERE id IN (
			SELECT id FROM host_lesson
			WHERE tenant_id=$1 AND host=$2
			ORDER BY priority DESC, updated_at DESC
			OFFSET $3
		)`, tenant, host, keep)
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
	var payload []byte
	if err := r.Scan(
		&l.ID, &l.TenantID, &l.Host, &l.Content, &l.ContentHash,
		&l.Priority, &l.SourceEngagementID, &l.SourceFindingID,
		&l.HitCount, &payload, &l.CreatedAt, &l.UpdatedAt,
	); err != nil {
		return err
	}
	l.Payload = payload
	return nil
}
