package finding

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/logx"
)

// engagementCounter 是 Save 成功后用于 best-effort 维护 engagement.finding_count 的最小接口。
type engagementCounter interface {
	IncrementFindingCount(ctx context.Context, id string, n int) error
}

// findingLog 包级 logger，用于 best-effort 计数失败的 warn。
var findingLog = logx.New("vulnfinding")

// SavedHook 是 Save 成功后的异步回调签名（用于 lesson_extract 蒸馏等订阅者）。
//
// caller 持久化路径不应被 hook 阻塞——Store 用独立 context + 新 goroutine 触发；
// 实现方应自行 recover panic（Store 不替你兜底）。
type SavedHook func(ctx context.Context, engagementID string, f VulnFinding)

// Store 封装 finding 表的所有持久化操作。
//
// v0024 agentic-lean：
//   - 删 kind / confidence / dedup_key 列（信息融入 summary 自由文本）
//   - severity 自由文本（前端按前缀配色）
//   - dedup 由 LLM 调用方自决（write 前调 findings() 自查）
//   - 删 OnReSaved hook（无 dedup_key 后无法精准识别"重发现"；append-only 每次都触发 OnSaved）
type Store struct {
	pool *pgxpool.Pool

	mu    sync.RWMutex
	hooks []SavedHook

	engCounter engagementCounter
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// WithCounter 链式注入 engagement 计数维护器。
func (s *Store) WithCounter(c engagementCounter) *Store {
	s.engCounter = c
	return s
}

// colsSelect 是所有 SELECT / RETURNING 路径的统一列序，与 scan() 字段一一对应。
// v0024：lean schema——删 kind/confidence/dedup_key 列。
const colsSelect = "id, engagement_id, agent_run_id, source_flow_id, host, severity, summary, target, evidence, created_at"

// OnSaved 注册 INSERT 后的异步回调（lesson_extract 蒸馏挂这里）。
func (s *Store) OnSaved(hook SavedHook) {
	if hook == nil {
		return
	}
	s.mu.Lock()
	s.hooks = append(s.hooks, hook)
	s.mu.Unlock()
}

// Save 永远 INSERT 一行新 finding（append-only）。
//
// 流程：INSERT → commit → fireSavedHooks（lesson_extract 异步蒸馏）。
// dedup 由调用方自决（写 finding 前先 findings() 看 host 已有的）；Store 不做去重。
func (s *Store) Save(ctx context.Context, f VulnFinding) (VulnFinding, error) {
	if f.Host == "" {
		return VulnFinding{}, fmt.Errorf("finding.Host 必填")
	}
	if f.Summary == "" {
		return VulnFinding{}, fmt.Errorf("finding.Summary 必填")
	}
	if f.Severity == "" {
		f.Severity = "medium"
	}
	for _, p := range []*json.RawMessage{&f.Target, &f.Evidence} {
		if *p == nil {
			*p = json.RawMessage("{}")
		}
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return VulnFinding{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // commit 后 rollback 是 no-op

	row := tx.QueryRow(ctx, `
		INSERT INTO finding
			(engagement_id, agent_run_id, source_flow_id, host, severity, summary, target, evidence)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING `+colsSelect,
		f.EngagementID, f.TaskID, f.SourceFlowID, f.Host, f.Severity,
		f.Summary, f.Target, f.Evidence)

	var saved VulnFinding
	if err := scan(row, &saved); err != nil {
		return VulnFinding{}, fmt.Errorf("insert finding: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return VulnFinding{}, fmt.Errorf("commit: %w", err)
	}

	if s.engCounter != nil {
		if err := s.engCounter.IncrementFindingCount(context.Background(), saved.EngagementID, 1); err != nil {
			findingLog.Warn().Err(err).Str("engagement_id", saved.EngagementID).
				Msg("engagement.finding_count 增量维护失败")
		}
	}

	findingLog.Info().
		Str("finding_id", saved.ID).
		Str("severity", saved.Severity).
		Str("host", saved.Host).
		Str("engagement_id", saved.EngagementID).
		Int("summary_len", len(saved.Summary)).
		Msg("finding saved ✓")
	s.fireSavedHooks(saved)
	return saved, nil
}

// GetByID 按主键读取 finding。
func (s *Store) GetByID(ctx context.Context, id string) (VulnFinding, error) {
	row := s.pool.QueryRow(ctx,
		"SELECT "+colsSelect+" FROM finding WHERE id=$1", id)
	var f VulnFinding
	if err := scan(row, &f); err != nil {
		return VulnFinding{}, fmt.Errorf("get finding %s: %w", id, err)
	}
	return f, nil
}

// ListByEngagement 列出 engagement 下所有 finding（按 created_at desc）。
//
// v0024：去掉 DISTINCT ON (host, dedup_key) 因为 dedup_key 列已删；
// dedup 由 LLM 写 finding 前自查 findings() 决定，Store 不做。
func (s *Store) ListByEngagement(ctx context.Context, engagementID string) ([]VulnFinding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+colsSelect+`
		FROM finding
		WHERE engagement_id = $1
		ORDER BY created_at DESC`, engagementID)
	if err != nil {
		return nil, fmt.Errorf("list findings: %w", err)
	}
	defer rows.Close()

	var out []VulnFinding
	for rows.Next() {
		var f VulnFinding
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

// ListByHost 列出某 host 在所有 engagement 下的 finding（按 created_at desc）。
// 用于 hunter agent user prompt 拼装"该 host 已有 finding"段，让 LLM 自决 dedup。
func (s *Store) ListByHost(ctx context.Context, host string) ([]VulnFinding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+colsSelect+`
		FROM finding
		WHERE host = $1
		ORDER BY created_at DESC`, host)
	if err != nil {
		return nil, fmt.Errorf("list findings by host: %w", err)
	}
	defer rows.Close()

	var out []VulnFinding
	for rows.Next() {
		var f VulnFinding
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

// CountByEngagement 返回某 engagement 下 finding 总数（用于 Rotator 阈值检查）。
func (s *Store) CountByEngagement(ctx context.Context, engagementID string) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM finding WHERE engagement_id=$1`, engagementID,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("count findings by engagement: %w", err)
	}
	return n, nil
}

// fireSavedHooks 异步触发所有 OnSaved 订阅。
func (s *Store) fireSavedHooks(f VulnFinding) {
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
func scan(r scanner, f *VulnFinding) error {
	var target, evidence []byte
	if err := r.Scan(
		&f.ID, &f.EngagementID, &f.TaskID, &f.SourceFlowID, &f.Host, &f.Severity,
		&f.Summary, &target, &evidence,
		&f.CreatedAt,
	); err != nil {
		return err
	}
	f.Target, f.Evidence = target, evidence
	return nil
}
