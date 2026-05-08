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
// *engagement.Store 自动满足；nil 时跳过维护（测试或未装配场景）。
type engagementCounter interface {
	IncrementFindingCount(ctx context.Context, id string, n int) error
}

// findingLog 包级 logger，用于 best-effort 计数失败的 warn。
var findingLog = logx.New("vulnfinding")

// SavedHook 是 Save 成功后的异步回调签名。
// caller 持久化路径不应被 hook 阻塞——Store 用独立 context + 新 goroutine 触发；
// 实现方应自行 recover panic（Store 不替你兜底）。
type SavedHook func(ctx context.Context, engagementID string, f VulnFinding)

// Store 封装 finding 表的所有持久化操作。
//
// v1.2 改 append-only：每次 Save 都 INSERT 新行（不再 UPSERT 合并 evidence），
// 完整审计轨迹保留；用 SELECT EXISTS 提前判断 isFirstSeen 决定触发哪个钩子。
//
// 钩子分两路：
//   - hooks (OnSaved)        仅在首次发现 INSERT 触发，用于 lesson_extract 蒸馏（避免重复 LLM 调用）
//   - reSavedHooks (OnReSaved) 仅在重发现 INSERT 触发，用于 lesson hit_count++ 等
//     "经验复用计数"语义；不调 LLM
type Store struct {
	pool *pgxpool.Pool

	mu           sync.RWMutex
	hooks        []SavedHook
	reSavedHooks []SavedHook

	// engCounter 可空：装配时通过 WithCounter 注入；Save 成功后 best-effort
	// 给 engagement.finding_count +1（失败仅 warn，Abort 时 SELECT count(*) 兜底）。
	engCounter engagementCounter
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// WithCounter 链式注入 engagement 计数维护器。返回原 Store 便于装配链式调用。
//
// 用法：finding.NewStore(pool).WithCounter(engs)
func (s *Store) WithCounter(c engagementCounter) *Store {
	s.engCounter = c
	return s
}

// colsSelect 是所有 SELECT / RETURNING 路径的统一列序，与 scan() 字段一一对应。
const colsSelect = "id, engagement_id, agent_run_id, source_flow_id, host, kind, severity, title, target, evidence, confidence, dedup_key, created_at"

// OnSaved 注册首次发现 INSERT 后的异步回调（lesson_extract 蒸馏挂这里）。
// v1 单进程内订阅，无需分布式 pub/sub；多个 hook 按注册顺序异步触发。
func (s *Store) OnSaved(hook SavedHook) {
	if hook == nil {
		return
	}
	s.mu.Lock()
	s.hooks = append(s.hooks, hook)
	s.mu.Unlock()
}

// OnReSaved 注册"重发现"INSERT 后的异步回调。
//
// 用于 lesson.TouchByFinding 等"经验复用计数"语义——每次重扫确认漏洞依然存在，
// 给对应 lesson hit_count+1，体现可信度。不调 LLM，仅做轻量计数。
func (s *Store) OnReSaved(hook SavedHook) {
	if hook == nil {
		return
	}
	s.mu.Lock()
	s.reSavedHooks = append(s.reSavedHooks, hook)
	s.mu.Unlock()
}

// Save 永远 INSERT 一行新 finding（append-only）。
//
// 流程（事务内）：
//  1. SELECT EXISTS 看 (host, dedup_key) 之前是否已存在 → existedBefore
//  2. INSERT 新行（无 ON CONFLICT）
//  3. 提交事务
//  4. existedBefore=false → fireSavedHooks（lesson_extract 蒸馏首次发现）
//     existedBefore=true  → fireReSavedHooks（lesson hit_count++）
//
// 返回 (VulnFinding, isFirstSeen, error)：caller 可凭 isFirstSeen 决定 UI 显示"新发现"还是"重复发现"。
//
// lesson 表通过 OnReSaved 钩子的 lesson.TouchByFinding 累积 hit_count，
// 比 finding 行数更适合"该漏洞被验证过几次"语义。
func (s *Store) Save(ctx context.Context, f VulnFinding) (VulnFinding, bool, error) {
	if f.Host == "" {
		return VulnFinding{}, false, fmt.Errorf("finding.Host 必填")
	}
	if f.Severity == "" {
		f.Severity = SeverityMedium
	}
	// agentic 路线：confidence 必填——LLM 按 high/medium/low 自评，不再有占位 "unverified" 兜底
	if f.Confidence == "" {
		return VulnFinding{}, false, fmt.Errorf("finding.Confidence 必填（high|medium|low）")
	}
	for _, p := range []*json.RawMessage{&f.Target, &f.Evidence} {
		if *p == nil {
			*p = json.RawMessage("{}")
		}
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return VulnFinding{}, false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // commit 后 rollback 是 no-op

	var existedBefore bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM finding WHERE host=$1 AND dedup_key=$2)`,
		f.Host, f.DedupKey,
	).Scan(&existedBefore); err != nil {
		return VulnFinding{}, false, fmt.Errorf("check existence: %w", err)
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO finding
			(engagement_id, agent_run_id, source_flow_id, host, kind, severity, title, target, evidence, confidence, dedup_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING `+colsSelect,
		f.EngagementID, f.TaskID, f.SourceFlowID, f.Host, f.Kind, f.Severity, f.Title,
		f.Target, f.Evidence, f.Confidence, f.DedupKey)

	var saved VulnFinding
	if err := scan(row, &saved); err != nil {
		return VulnFinding{}, false, fmt.Errorf("insert finding: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return VulnFinding{}, false, fmt.Errorf("commit: %w", err)
	}

	// best-effort 维护 engagement.finding_count（commit 后才发；失败仅 warn 不影响业务）。
	// 用 context.Background()：业务 ctx 取消不应阻断埋点（与 LLM instrument 同模式）。
	if s.engCounter != nil {
		if err := s.engCounter.IncrementFindingCount(context.Background(), saved.EngagementID, 1); err != nil {
			findingLog.Warn().Err(err).Str("engagement_id", saved.EngagementID).
				Msg("engagement.finding_count 增量维护失败（Abort 时会重算兜底）")
		}
	}

	isFirstSeen := !existedBefore
	findingLog.Info().
		Str("finding_id", saved.ID).
		Str("kind", saved.Kind).
		Str("severity", string(saved.Severity)).
		Str("confidence", string(saved.Confidence)).
		Str("dedup_key", saved.DedupKey).
		Str("host", saved.Host).
		Str("engagement_id", saved.EngagementID).
		Bool("first_seen", isFirstSeen).
		Msg("finding saved ✓")
	if isFirstSeen {
		s.fireSavedHooks(saved)
	} else {
		s.fireReSavedHooks(saved)
	}
	return saved, isFirstSeen, nil
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

// ListByEngagement 列出 engagement.target_host 下每个 (host, dedup_key) 的**最新一行** finding。
//
// 用 DISTINCT ON 去重——append-only 模式下同 (host, dedup_key) 可能多行，
// 报告通常只关心最新状态；具体审计轨迹（每次发现）由直接查 finding 表给出。
//
// v1.2 语义：报告以 host 为单位（贴合"看这站发现了什么"的直觉），
// 跨 engagement 重发现的也能看到。
func (s *Store) ListByEngagement(ctx context.Context, engagementID string) ([]VulnFinding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (host, dedup_key) `+colsSelect+`
		FROM finding
		WHERE host = (SELECT target_host FROM engagement WHERE id=$1)
		ORDER BY host, dedup_key, created_at DESC`, engagementID)
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

// CountByEngagement 返回某 engagement 下 finding 总数（用于 Rotator 阈值检查）。
//
// append-only 后，本 engagement 写入的所有行（含同 dedup_key 的重发现行）都计入；
// 反映"本 engagement 触发了多少次写入"，比"独立漏洞数"更适合阈值用途。
func (s *Store) CountByEngagement(ctx context.Context, engagementID string) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM finding WHERE engagement_id=$1`, engagementID,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("count findings by engagement: %w", err)
	}
	return n, nil
}

// HasDedupKey 报告 (engagement_id, dedup_key) 在 finding 表中是否存在。
//
// 用于 BACDoneValidator：当 LLM 声明 reason=finding_written 时，系统层校验
// 本 engagement 内确实写过该 dedup_key 的 finding。
//
// append-only 后多行也只要存在 ≥1 行就 true，逻辑不变。
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

// fireSavedHooks 异步触发所有 OnSaved 订阅（首次发现 INSERT 路径）：
// - 用独立 context.Background() 避免 caller cancel 时 lesson_extract 也被 cancel
// - 每个 hook 单独 goroutine，互不阻塞
// - 快照 hooks 切片再释放锁，避免 hook 内回调 OnSaved 引发死锁
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

// fireReSavedHooks 异步触发所有 OnReSaved 订阅（重发现 INSERT 路径）。
// 实现细节同 fireSavedHooks，但消费 reSavedHooks 队列。
func (s *Store) fireReSavedHooks(f VulnFinding) {
	s.mu.RLock()
	snapshot := make([]SavedHook, len(s.reSavedHooks))
	copy(snapshot, s.reSavedHooks)
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
		&f.ID, &f.EngagementID, &f.TaskID, &f.SourceFlowID, &f.Host, &f.Kind, &f.Severity, &f.Title,
		&target, &evidence, &f.Confidence, &f.DedupKey,
		&f.CreatedAt,
	); err != nil {
		return err
	}
	f.Target, f.Evidence = target, evidence
	return nil
}
