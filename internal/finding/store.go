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

// findingLog 包级 logger，用于 Save 路径的 info / warn 记录。
var findingLog = logx.New("vulnfinding")

// Store 封装 finding 表的所有持久化操作。
//
// 设计要点：
//   - severity 自由文本（前端按前缀配色）
//   - dedup 由 LLM 调用方自决（write 前调 read_findings 自查）
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT / RETURNING 路径的统一列序，与 scan() 字段一一对应。
const colsSelect = "id, owner_type, owner_id::text AS owner_id, " +
	"agent_run_id, source_flow_id, host, severity, summary, target, evidence, " +
	"COALESCE(cwe_id, ''), COALESCE(owasp_category, ''), first_seen_at, COALESCE(remediation, ''), created_at"

// Save 永远 INSERT 一行新 finding（append-only）。
//
// 流程：INSERT → commit。
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

	// 0048 加了 UNIQUE(owner_id, dedup_key) — dedup_key 是 PG generated column，公式见
	// db/migrations/0048_*.up.sql（host + CWE + target.path 强约束，宁可误判不漏判）。
	// commander / striker agent 并发写同一漏洞时，ON CONFLICT 保留首个写入（first_seen_at 取较早），后续 dup
	// 不报错而是返回 existing 行——LLM 视角 Save 始终幂等成功，dedup 在 DB 层无声完成。
	row := tx.QueryRow(ctx, `
		INSERT INTO finding
			(owner_type, owner_id, agent_run_id, source_flow_id, host, severity, summary, target, evidence,
			 cwe_id, owasp_category, remediation)
		VALUES ($1, $2::uuid, $3,$4,$5,$6,$7,$8,$9, NULLIF($10,''), NULLIF($11,''), NULLIF($12,''))
		ON CONFLICT (owner_id, dedup_key) DO UPDATE
		SET first_seen_at = LEAST(finding.first_seen_at, EXCLUDED.first_seen_at)
		RETURNING `+colsSelect,
		f.OwnerType, f.OwnerID,
		f.TaskID, f.SourceFlowID, f.Host, f.Severity,
		f.Summary, f.Target, f.Evidence,
		f.CWEID, f.OWASPCategory, f.Remediation)

	var saved VulnFinding
	if err := scan(row, &saved); err != nil {
		return VulnFinding{}, fmt.Errorf("upsert finding: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return VulnFinding{}, fmt.Errorf("commit: %w", err)
	}

	// dup 命中：DB 层把后续重复写无声合并到已有行；saved 是 existing 行内容。
	// agent_run_id 不会被覆盖（DO UPDATE 只动 first_seen_at），所以 saved.TaskID 反映首次写入者。
	dedupHit := f.TaskID != nil && saved.TaskID != nil && *f.TaskID != *saved.TaskID
	ev := findingLog.Info()
	if dedupHit {
		ev = ev.Bool("dedup_hit", true)
	}
	ev.Str("finding_id", saved.ID).
		Str("severity", saved.Severity).
		Str("host", saved.Host).
		Str("owner_id", saved.OwnerID).
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

// ListByOwner 列出 owner 下所有 finding（按 created_at desc）。
// ownerType 为 "" 时退化为仅按 owner_id 过滤——caller 仅持有 ID（如 HTTP URL :owner_id）时用。
// 利用 owner_id UUID 全局唯一性确保跨 owner_type 不冲突。
func (s *Store) ListByOwner(ctx context.Context, ownerType, ownerID string) ([]VulnFinding, error) {
	q := `SELECT ` + colsSelect + ` FROM finding WHERE owner_id=$1::uuid`
	args := []any{ownerID}
	if ownerType != "" {
		q += ` AND owner_type=$2`
		args = append(args, ownerType)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, q, args...)
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
// polymorphic 路径——按 (owner_type, owner_id, host) 过滤，取代旧的 ListByOwnerIDAndHost。
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

// ListByOwnerIDAndHost 列出当前 owner_id + host 下的 finding（按 created_at desc）。

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scan 是 colsSelect 列序的统一反序列化点。
// taskID / sourceFlowID 用指针接住 NULL；TaskID 是 *string 保留 nil，SourceFlowID 是 *int64 同。
func scan(r scanner, f *VulnFinding) error {
	var taskID *string
	var sourceFlowID *int64
	if err := r.Scan(
		&f.ID, &f.OwnerType, &f.OwnerID,
		&taskID, &sourceFlowID, &f.Host, &f.Severity,
		&f.Summary, &f.Target, &f.Evidence,
		&f.CWEID, &f.OWASPCategory, &f.FirstSeenAt, &f.Remediation,
		&f.CreatedAt,
	); err != nil {
		return err
	}
	f.TaskID = taskID
	f.SourceFlowID = sourceFlowID
	return nil
}
