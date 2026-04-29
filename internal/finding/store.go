package finding

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SavedHook 是 Save 成功后的异步回调签名。
// caller 持久化路径不应被 hook 阻塞——Store 用独立 context + 新 goroutine 触发；
// 实现方应自行 recover panic（Store 不替你兜底）。
type SavedHook func(ctx context.Context, engagementID string, f Finding)

// Store 封装 finding 表的所有持久化操作。
type Store struct {
	pool *pgxpool.Pool

	mu    sync.RWMutex
	hooks []SavedHook
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT / RETURNING 路径的统一列序，与 scan() 字段一一对应。
const colsSelect = "id, engagement_id, task_id, kind, severity, title, target, evidence, payload, tool, confidence, dedup_key, created_at, updated_at"

// OnSaved 注册一个 Save 成功后的异步回调。
// v1 单进程内订阅，无需分布式 pub/sub；多个 hook 按注册顺序异步触发。
func (s *Store) OnSaved(hook SavedHook) {
	if hook == nil {
		return
	}
	s.mu.Lock()
	s.hooks = append(s.hooks, hook)
	s.mu.Unlock()
}

// Save 按 (engagement_id, dedup_key) UNIQUE 幂等写入 finding。
// 冲突时合并 evidence（jsonb || EXCLUDED.evidence，顶层键合并新值覆盖）+ 推进 updated_at。
// 返回新插入或合并后的 Finding；成功后异步触发已注册的 OnSaved hook（不阻塞返回）。
func (s *Store) Save(ctx context.Context, f Finding) (Finding, error) {
	if f.Severity == "" {
		f.Severity = SeverityMedium
	}
	if f.Confidence == "" {
		f.Confidence = ConfidenceUnverified
	}
	for _, p := range []*json.RawMessage{&f.Target, &f.Evidence, &f.Payload} {
		if *p == nil {
			*p = json.RawMessage("{}")
		}
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO finding
			(engagement_id, task_id, kind, severity, title, target, evidence, payload, tool, confidence, dedup_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (engagement_id, dedup_key) DO UPDATE
		  SET evidence   = finding.evidence || EXCLUDED.evidence,
		      updated_at = now()
		RETURNING `+colsSelect,
		f.EngagementID, f.TaskID, f.Kind, f.Severity, f.Title,
		[]byte(f.Target), []byte(f.Evidence), []byte(f.Payload),
		f.Tool, f.Confidence, f.DedupKey)

	var saved Finding
	if err := scan(row, &saved); err != nil {
		return Finding{}, fmt.Errorf("save finding: %w", err)
	}

	s.fireSavedHooks(saved)
	return saved, nil
}

// GetByID 按主键读取 finding。
func (s *Store) GetByID(ctx context.Context, id string) (Finding, error) {
	row := s.pool.QueryRow(ctx,
		"SELECT "+colsSelect+" FROM finding WHERE id=$1", id)
	var f Finding
	if err := scan(row, &f); err != nil {
		return Finding{}, fmt.Errorf("get finding %s: %w", id, err)
	}
	return f, nil
}

// ListByEngagement 按 created_at 升序列出 engagement 下所有 finding。
func (s *Store) ListByEngagement(ctx context.Context, engagementID string) ([]Finding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+colsSelect+`
		FROM finding
		WHERE engagement_id=$1
		ORDER BY created_at ASC`, engagementID)
	if err != nil {
		return nil, fmt.Errorf("list findings: %w", err)
	}
	defer rows.Close()

	var out []Finding
	for rows.Next() {
		var f Finding
		if err := scan(rows, &f); err != nil {
			return nil, fmt.Errorf("scan finding: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate findings: %w", err)
	}
	return out, nil
}

// HasDedupKey 报告 (engagement_id, dedup_key) 在 finding 表中是否存在。
//
// 用于 BACDoneValidator（黑客松借鉴共识 C）：当 LLM 声明 reason=finding_written 时，
// 系统层不能轻信 LLM——必须校验对应 finding 已真正落库。
func (s *Store) HasDedupKey(ctx context.Context, engagementID, dedupKey string) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM finding WHERE engagement_id=$1 AND dedup_key=$2)`,
		engagementID, dedupKey,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("check finding dedup_key: %w", err)
	}
	return exists, nil
}

// fireSavedHooks 异步触发所有已订阅 hook：
// - 用独立 context.Background() 避免 caller cancel 时 distill 也被 cancel
// - 每个 hook 单独 goroutine，互不阻塞
// - 快照 hooks 切片再释放锁，避免 hook 内回调 OnSaved 引发死锁
func (s *Store) fireSavedHooks(f Finding) {
	s.mu.RLock()
	snapshot := make([]SavedHook, len(s.hooks))
	copy(snapshot, s.hooks)
	s.mu.RUnlock()

	for _, hook := range snapshot {
		h := hook
		go h(context.Background(), f.EngagementID, f)
	}
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scan 是 colsSelect 列序的统一反序列化点。
func scan(r scanner, f *Finding) error {
	var target, evidence, payload []byte
	if err := r.Scan(
		&f.ID, &f.EngagementID, &f.TaskID, &f.Kind, &f.Severity, &f.Title,
		&target, &evidence, &payload, &f.Tool, &f.Confidence, &f.DedupKey,
		&f.CreatedAt, &f.UpdatedAt,
	); err != nil {
		return err
	}
	f.Target, f.Evidence, f.Payload = target, evidence, payload
	return nil
}
