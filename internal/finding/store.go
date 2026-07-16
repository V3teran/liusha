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
// 0059 加 depends_on uuid[]（组合漏洞依赖：c.depends_on = [a.id, b.id]）。
const colsSelect = "id, task_id::text AS task_id, " +
	"hunter_id, source_traffic_id, host, severity, summary, target, evidence, " +
	"COALESCE(cwe_id, ''), COALESCE(owasp_category, ''), first_seen_at, COALESCE(remediation, ''), " +
	"depends_on::text[], created_at"

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

	// 0074 UNIQUE(task_id, dedup_key) — dedup_key 是 PG generated column（host + summary 前 60 字，
	// 宁可误判不漏判），单次扫描（task）内去重。
	// orchestrator / exploitation agent 并发写同一漏洞时，ON CONFLICT 保留首个写入（first_seen_at 取较早），后续 dup
	// 不报错而是返回 existing 行——LLM 视角 Save 始终幂等成功，dedup 在 DB 层无声完成。
	// depends_on 是 uuid[]，empty slice → DEFAULT '{}'（PG 数组默认值）
	deps := f.DependsOn
	if deps == nil {
		deps = []string{}
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO finding
			(task_id, hunter_id, source_traffic_id, host, severity, summary, target, evidence,
			 cwe_id, owasp_category, remediation, depends_on)
		VALUES ($1::uuid, $2,$3,$4,$5,$6,$7,$8, NULLIF($9,''), NULLIF($10,''), NULLIF($11,''), $12::uuid[])
		ON CONFLICT (task_id, dedup_key) DO UPDATE
		SET first_seen_at = LEAST(finding.first_seen_at, EXCLUDED.first_seen_at)
		RETURNING `+colsSelect,
		f.TaskID,
		f.HunterID, f.SourceTrafficID, f.Host, f.Severity,
		f.Summary, f.Target, f.Evidence,
		f.CWEID, f.OWASPCategory, f.Remediation, deps)

	var saved VulnFinding
	if err := scan(row, &saved); err != nil {
		return VulnFinding{}, fmt.Errorf("upsert finding: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return VulnFinding{}, fmt.Errorf("commit: %w", err)
	}

	// dup 命中：DB 层把后续重复写无声合并到已有行；saved 是 existing 行内容。
	// hunter_id 不会被覆盖（DO UPDATE 只动 first_seen_at），所以 saved.HunterID 反映首次写入者。
	dedupHit := f.HunterID != nil && saved.HunterID != nil && *f.HunterID != *saved.HunterID
	ev := findingLog.Info()
	if dedupHit {
		ev = ev.Bool("dedup_hit", true)
	}
	ev.Str("finding_id", saved.ID).
		Str("severity", saved.Severity).
		Str("host", saved.Host).
		Str("task_id", saved.TaskID).
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
//
// dependsOn（组合漏洞依赖 finding id 数组）：len>0 才更新，nil/空切片不动——支持「事后补依赖」。
// 这是收尾复盘漏洞组合链的关键路径：首次 write_finding 各个击破时漏洞往往未察觉组合，
// 后期复盘识别出「a + b = c」时用 update_finding 给 c 补 depends_on=[a,b]。
// （write_finding 重写无法补——dedup ON CONFLICT 只更 first_seen_at，故补依赖必走本方法。）
func (s *Store) Update(ctx context.Context, id, summary, severity string, target, evidence json.RawMessage, dependsOn []string) error {
	if id == "" {
		return fmt.Errorf("finding.Update: id 必填")
	}

	// 动态拼 SET 子句，传入空值的字段不动
	sets := make([]string, 0, 5)
	args := make([]any, 0, 6)
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
	if len(dependsOn) > 0 {
		// 补组合漏洞依赖：uuid[] 强转（同 Save）；len>0 才更新，避免误清空已有依赖。
		sets = append(sets, fmt.Sprintf("depends_on = $%d::uuid[]", argIdx))
		args = append(args, dependsOn)
		argIdx++
	}

	if len(sets) == 0 {
		return fmt.Errorf("finding.Update: 至少提供一个可更新字段（summary/severity/target/evidence/depends_on）")
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

// ListByTask 列出 task 下所有 finding（按 created_at desc）。
func (s *Store) ListByTask(ctx context.Context, taskID string) ([]VulnFinding, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+colsSelect+` FROM finding WHERE task_id=$1::uuid ORDER BY created_at DESC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list findings by task: %w", err)
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

// ListByTaskAndHost 列出 task + host 下的 finding（按 created_at desc）。
func (s *Store) ListByTaskAndHost(ctx context.Context, taskID, host string, limit int) ([]VulnFinding, error) {
	q := `SELECT ` + colsSelect + ` FROM finding WHERE task_id=$1::uuid AND host=$2 ORDER BY created_at DESC`
	args := []any{taskID, host}
	if limit > 0 {
		q += ` LIMIT $3`
		args = append(args, limit)
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list findings by task+host: %w", err)
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

// ListByHost 跨 task 列出某 host 的所有 finding（按 created_at desc，见 spec §8.1）。
// 用于 host 漏洞全景：passive「这个 host 所有漏洞」、active 复盘「目标历史漏洞」。
// finding 表本有 host 列 + 索引，纯 SQL，不受 task 作用域约束。
func (s *Store) ListByHost(ctx context.Context, host string, limit int) ([]VulnFinding, error) {
	q := `SELECT ` + colsSelect + ` FROM finding WHERE host=$1 ORDER BY created_at DESC`
	args := []any{host}
	if limit > 0 {
		q += ` LIMIT $2`
		args = append(args, limit)
	}
	rows, err := s.pool.Query(ctx, q, args...)
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
	return out, rows.Err()
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scan 是 colsSelect 列序的统一反序列化点。
// hunterID / sourceTrafficID 用指针接住 NULL；HunterID 是 *string 保留 nil，SourceTrafficID 是 *int64 同。
// DependsOn 是 uuid[]，扫到 []string（pgx v5 默认 codec）。
func scan(r scanner, f *VulnFinding) error {
	var hunterID *string
	var sourceTrafficID *int64
	var dependsOn []string
	if err := r.Scan(
		&f.ID, &f.TaskID,
		&hunterID, &sourceTrafficID, &f.Host, &f.Severity,
		&f.Summary, &f.Target, &f.Evidence,
		&f.CWEID, &f.OWASPCategory, &f.FirstSeenAt, &f.Remediation,
		&dependsOn,
		&f.CreatedAt,
	); err != nil {
		return err
	}
	f.HunterID = hunterID
	f.SourceTrafficID = sourceTrafficID
	f.DependsOn = dependsOn
	return nil
}
