package finding

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

// Store 封装 finding 表的所有持久化操作。
//
// 设计要点：
//   - severity 自由文本（前端按前缀配色）
//   - dedup 由 LLM 调用方自决（write 前调 read_findings 自查）
type Store struct {
	pool *pgxpool.Pool

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
const colsSelect = "id, engagement_id, agent_run_id, source_flow_id, host, severity, summary, target, evidence, created_at"

// Save 永远 INSERT 一行新 finding（append-only）。
//
// 流程：INSERT → commit → 维护 engagement.finding_count。
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
	return saved, nil
}

// Update 部分更新一条 finding 的可变字段（summary / severity / target / evidence）。
//
// 设计意图：read_findings 看到等价但更有价值（更详细 PoC / 更精准描述 / 更高 severity）
// 时，update_finding 工具调本方法覆盖；created_at 保持首次发现时间不变。
//
// 字段语义：传空字符串 / nil 表示**不更新该字段**（zero-value 跳过，保留原值）。
// summary 强制非空（finding lean schema 核心字段）。
//
// id 必填；finding 不存在返错。
func (s *Store) Update(ctx context.Context, id, summary, severity string, target, evidence json.RawMessage) error {
	if id == "" {
		return fmt.Errorf("finding.Update: id 必填")
	}

	// 动态拼 SET 子句，传入空值的字段不动
	sets := make([]string, 0, 4)
	args := make([]any, 0, 5)
	argIdx := 1

	if summary != "" {
		sets = append(sets, fmt.Sprintf("summary = $%d", argIdx))
		args = append(args, summary)
		argIdx++
	}
	if severity != "" {
		sets = append(sets, fmt.Sprintf("severity = $%d", argIdx))
		args = append(args, severity)
		argIdx++
	}
	if len(target) > 0 {
		sets = append(sets, fmt.Sprintf("target = $%d", argIdx))
		args = append(args, target)
		argIdx++
	}
	if len(evidence) > 0 {
		sets = append(sets, fmt.Sprintf("evidence = $%d", argIdx))
		args = append(args, evidence)
		argIdx++
	}

	if len(sets) == 0 {
		return fmt.Errorf("finding.Update: 至少提供一个可更新字段（summary/severity/target/evidence）")
	}

	args = append(args, id)
	q := fmt.Sprintf(`UPDATE finding SET %s WHERE id = $%d`, strings.Join(sets, ", "), argIdx)

	tag, err := s.pool.Exec(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("update finding %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("update finding %s: not found", id)
	}

	findingLog.Info().
		Str("finding_id", id).
		Int("fields_updated", len(sets)).
		Msg("finding updated ✓")
	return nil
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
// dedup 由 LLM 写 finding 前自查 read_findings 决定，Store 不做。
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

// ListByEngagementAndHost 列出当前 engagement + host 下的 finding（按 created_at desc）。
//
// 用于 hunter user prompt 段 3 注入"该 host 已有 finding"——隔离每次 engagement，
// 不被历史扫描污染（旧实现 ListByHost 跨 engagement，已被 v1.1 重设计弃用）。
// limit ≤ 0 不限制。
func (s *Store) ListByEngagementAndHost(ctx context.Context, engagementID, host string, limit int) ([]VulnFinding, error) {
	q := `SELECT ` + colsSelect + ` FROM finding WHERE engagement_id=$1 AND host=$2 ORDER BY created_at DESC`
	args := []any{engagementID, host}
	if limit > 0 {
		q += ` LIMIT $3`
		args = append(args, limit)
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list findings by engagement+host: %w", err)
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

// CountAndLatestByEngagementAndHost 返回 engagement+host 范围下 finding 总数 + 最新一条概要。
//
// 用于 reviewer 评估 prompt 注入"该 host 已有 N 个 finding，最新：…"——
// reviewer 视野从"当前 agent_run"扩到"engagement 内本 host 全部"，符合"整个 host 状态做决策"直觉。
// 同时不跨 engagement，保证每次扫描独立。
//
// 实现：单次 SQL 用 count(*) OVER () window，LIMIT 1 拿最新一行。
//   - 0 行：count=0, latest=nil
//   - ≥1 行：count=total, latest=最新一条（仅填 id/severity/summary/created_at）
func (s *Store) CountAndLatestByEngagementAndHost(ctx context.Context, engagementID, host string) (int, *VulnFinding, error) {
	if engagementID == "" || host == "" {
		return 0, nil, fmt.Errorf("CountAndLatestByEngagementAndHost: engagementID + host 都必填")
	}
	row := s.pool.QueryRow(ctx, `
		SELECT id, severity, summary, created_at, count(*) OVER () AS total
		FROM finding
		WHERE engagement_id=$1 AND host=$2
		ORDER BY created_at DESC
		LIMIT 1`, engagementID, host)
	var f VulnFinding
	var total int
	if err := row.Scan(&f.ID, &f.Severity, &f.Summary, &f.CreatedAt, &total); err != nil {
		if err == pgx.ErrNoRows {
			return 0, nil, nil
		}
		return 0, nil, fmt.Errorf("count and latest finding by engagement+host: %w", err)
	}
	return total, &f, nil
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
