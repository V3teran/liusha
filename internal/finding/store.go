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
// owner_type/owner_id 双轨：未填则空串（COALESCE 折叠 NULL）。
const colsSelect = "id, engagement_id, " +
	"COALESCE(owner_type, '') AS owner_type, " +
	"COALESCE(owner_id::text, '') AS owner_id, " +
	"agent_run_id, source_flow_id, host, severity, summary, target, evidence, created_at"

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
			(engagement_id, owner_type, owner_id, agent_run_id, source_flow_id, host, severity, summary, target, evidence)
		VALUES (NULLIF($1,'')::uuid, NULLIF($2,''), NULLIF($3,'')::uuid, $4,$5,$6,$7,$8,$9,$10)
		RETURNING `+colsSelect,
		f.EngagementID, f.OwnerType, f.OwnerID,
		f.TaskID, f.SourceFlowID, f.Host, f.Severity,
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

// ListByEngagement 列出 engagement / owner 下所有 finding（按 created_at desc）。
//
// 双轨切读：参数 ID 可以是旧 engagement.id 或新 owner_id。SQL OR 让 viewer 传新 owner_id 时
// 也命中。专用 ListByOwner 走纯 owner 路径；本方法保留兼容旧 caller。
// dedup 由 LLM 写 finding 前自查 read_findings 决定，Store 不做。
func (s *Store) ListByEngagement(ctx context.Context, engagementID string) ([]VulnFinding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+colsSelect+`
		FROM finding
		WHERE engagement_id=$1 OR owner_id=$1::uuid
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

// ListByOwner 列出 owner（passive_session / active_scan）下所有 finding（按 created_at desc）。
// 新 polymorphic 路径——commit B5 切读后取代 ListByEngagement。
func (s *Store) ListByOwner(ctx context.Context, ownerType, ownerID string) ([]VulnFinding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+colsSelect+`
		FROM finding
		WHERE owner_type=$1 AND owner_id=$2::uuid
		ORDER BY created_at DESC`, ownerType, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list findings by owner: %w", err)
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
	return out, rows.Err()
}

// ListByOwnerAndHost 列出 owner + host 下的 finding（按 created_at desc）。
// 新 polymorphic 路径——commit B5 切读后取代 ListByEngagementAndHost。
func (s *Store) ListByOwnerAndHost(ctx context.Context, ownerType, ownerID, host string, limit int) ([]VulnFinding, error) {
	q := `SELECT ` + colsSelect + ` FROM finding WHERE owner_type=$1 AND owner_id=$2::uuid AND host=$3 ORDER BY created_at DESC`
	args := []any{ownerType, ownerID, host}
	if limit > 0 {
		q += ` LIMIT $4`
		args = append(args, limit)
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list findings by owner+host: %w", err)
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
	return out, rows.Err()
}

// ListByEngagementAndHost 列出当前 engagement + host 下的 finding（按 created_at desc）。
//
// 用于 hunter user prompt 段 3 注入"该 host 已有 finding"——隔离每次 engagement，
// 不被跨次扫描的历史污染。
// limit ≤ 0 不限制。
func (s *Store) ListByEngagementAndHost(ctx context.Context, engagementID, host string, limit int) ([]VulnFinding, error) {
	// 双轨切读：ID 可以是旧 engagement.id 或新 owner_id。
	q := `SELECT ` + colsSelect + ` FROM finding WHERE (engagement_id=$1 OR owner_id=$1::uuid) AND host=$2 ORDER BY created_at DESC`
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

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scan 是 colsSelect 列序的统一反序列化点。
func scan(r scanner, f *VulnFinding) error {
	var target, evidence []byte
	if err := r.Scan(
		&f.ID, &f.EngagementID,
		&f.OwnerType, &f.OwnerID,
		&f.TaskID, &f.SourceFlowID, &f.Host, &f.Severity,
		&f.Summary, &target, &evidence,
		&f.CreatedAt,
	); err != nil {
		return err
	}
	f.Target, f.Evidence = target, evidence
	return nil
}
