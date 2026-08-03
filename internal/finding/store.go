package finding

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
// 0081 加 status / triage_note / triaged_at（triage 处置态）。
const colsSelect = "id, task_id::text AS task_id, " +
	"hunter_id, source_traffic_id, host, severity, summary, target, evidence, " +
	"COALESCE(cwe_id, ''), COALESCE(owasp_category, ''), first_seen_at, COALESCE(remediation, ''), " +
	"depends_on::text[], status, COALESCE(triage_note, ''), triaged_at, created_at"

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

// LedgerRow 是全局漏洞台账的一行：finding 主体 + JOIN task/assignment 派生的 ScenarioID / Source。
// 漏洞管理页跨 task/host 全量展示用，区别于 per-task 的 VulnFinding 列表。
type LedgerRow struct {
	VulnFinding
	ScenarioID string // 关联 task 的 scenario_id
	Source     string // 关联 assignment 的 source（manual 主动下发 / auto 被动代理）
}

// LedgerFilter 是台账查询的可选筛选（零值=不筛该维度）。
type LedgerFilter struct {
	Host       string
	Severity   string
	Status     string
	ScenarioID string
	Source     string // manual / auto（下发来源）
	Limit      int
}

// ListAll 全局漏洞台账查询：跨 task/host 平铺列出漏洞，JOIN task/assignment 带出 scenario_id 与 source，按可选维度筛选。
//
// 本方法不受 task/scenario 作用域约束，跨场景一网打尽，按 created_at desc 排序。
//
// 不做去重聚合：漏洞按「每次扫描各自独立」建模——同一个洞被多次扫描就是多条独立 finding，
// 各自有各自的 triage 处置态，互不影响。台账平铺全部，不折叠。
func (s *Store) ListAll(ctx context.Context, f LedgerFilter) ([]LedgerRow, error) {
	where := make([]string, 0, 4)
	args := make([]any, 0, 5)
	idx := 1
	add := func(clause string, val any) {
		where = append(where, fmt.Sprintf(clause, idx))
		args = append(args, val)
		idx++
	}
	if f.Host != "" {
		add("f.host = $%d", f.Host)
	}
	if f.Severity != "" {
		add("f.severity = $%d", f.Severity)
	}
	if f.Status != "" {
		add("f.status = $%d", f.Status)
	}
	if f.ScenarioID != "" {
		add("t.scenario_id = $%d", f.ScenarioID)
	}
	if f.Source != "" {
		add("a.source = $%d", f.Source)
	}

	q := `SELECT ` + ledgerCols + `, t.scenario_id, a.source
		FROM finding f
		JOIN task t ON t.id = f.task_id
		JOIN assignment a ON a.id = t.assignment_id`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY f.created_at DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT $%d", idx)
		args = append(args, f.Limit)
	}

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list all findings: %w", err)
	}
	defer rows.Close()

	var out []LedgerRow
	for rows.Next() {
		var r LedgerRow
		if err := scanLedger(rows, &r); err != nil {
			return nil, fmt.Errorf("scan ledger row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateTriage 人工处置一条 finding：状态 + 严重度 + 备注，triaged_at 打当前时刻，RETURNING 更新后的行。
//
// status 必须是五态之一（DB CHECK 兜底，这里前置校验给出友好错误）。
// severity 传空则不改（保留扫描时 LLM 定的值）；非空则直接覆盖——人工可修正 LLM 定级（triage 标配）。
// note 可空。finding 不存在返错（ErrNoRows）。返回行含后端权威 triaged_at（前端据此覆盖乐观值）。
func (s *Store) UpdateTriage(ctx context.Context, id, status, severity, note string) (VulnFinding, error) {
	if id == "" {
		return VulnFinding{}, fmt.Errorf("finding.UpdateTriage: id 必填")
	}
	if !validStatus(status) {
		return VulnFinding{}, fmt.Errorf("finding.UpdateTriage: 非法 status %q（须为 open/confirmed/fixed/false_positive/accepted）", status)
	}
	// severity 用 NULLIF 空跳过：传空保留原值，非空覆盖。COALESCE 让 SQL 单句表达「空则不改」。
	row := s.pool.QueryRow(ctx,
		`UPDATE finding
		 SET status = $1, severity = COALESCE(NULLIF($2, ''), severity),
		     triage_note = NULLIF($3, ''), triaged_at = now()
		 WHERE id = $4
		 RETURNING `+colsSelect,
		status, severity, note, id)
	var updated VulnFinding
	if err := scan(row, &updated); err != nil {
		if err == pgx.ErrNoRows {
			return VulnFinding{}, fmt.Errorf("update finding triage %s: not found", id)
		}
		return VulnFinding{}, fmt.Errorf("update finding triage %s: %w", id, err)
	}
	findingLog.Info().Str("finding_id", id).Str("status", status).Str("severity", updated.Severity).Msg("finding triaged ✓")
	return updated, nil
}

// validStatus 校验 triage 五态（与 DB CHECK 约束、迁移 0081 保持一致）。
func validStatus(s string) bool {
	switch s {
	case "open", "confirmed", "fixed", "false_positive", "accepted":
		return true
	}
	return false
}

// ledgerCols 是台账 JOIN 查询的列序（= colsSelect 但每列显式加 f. 前缀）。
// 不用程序化前缀：colsSelect 含 COALESCE(...) / depends_on::text[] 等内部带逗号的表达式，
// 按 ", " 切分会劈碎；且 JOIN task 后 id/status/created_at 列名歧义，必须 f. 限定。
// 与 colsSelect 手工对齐；scanLedger 列序 = 本常量 + 末尾 scenario_id, source。
const ledgerCols = "f.id, f.task_id::text AS task_id, " +
	"f.hunter_id, f.source_traffic_id, f.host, f.severity, f.summary, f.target, f.evidence, " +
	"COALESCE(f.cwe_id, ''), COALESCE(f.owasp_category, ''), f.first_seen_at, COALESCE(f.remediation, ''), " +
	"f.depends_on::text[], f.status, COALESCE(f.triage_note, ''), f.triaged_at, f.created_at"

// scanLedger 扫 ledgerCols 列序 + 末尾 scenario_id, source（比 scan() 多两列）。
func scanLedger(r scanner, out *LedgerRow) error {
	var hunterID *string
	var sourceTrafficID *int64
	var dependsOn []string
	var triagedAt *time.Time
	if err := r.Scan(
		&out.ID, &out.TaskID,
		&hunterID, &sourceTrafficID, &out.Host, &out.Severity,
		&out.Summary, &out.Target, &out.Evidence,
		&out.CWEID, &out.OWASPCategory, &out.FirstSeenAt, &out.Remediation,
		&dependsOn,
		&out.Status, &out.TriageNote, &triagedAt,
		&out.CreatedAt,
		&out.ScenarioID, &out.Source,
	); err != nil {
		return err
	}
	out.HunterID = hunterID
	out.SourceTrafficID = sourceTrafficID
	out.DependsOn = dependsOn
	out.TriagedAt = triagedAt
	return nil
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
	var triagedAt *time.Time
	if err := r.Scan(
		&f.ID, &f.TaskID,
		&hunterID, &sourceTrafficID, &f.Host, &f.Severity,
		&f.Summary, &f.Target, &f.Evidence,
		&f.CWEID, &f.OWASPCategory, &f.FirstSeenAt, &f.Remediation,
		&dependsOn,
		&f.Status, &f.TriageNote, &triagedAt,
		&f.CreatedAt,
	); err != nil {
		return err
	}
	f.HunterID = hunterID
	f.SourceTrafficID = sourceTrafficID
	f.DependsOn = dependsOn
	f.TriagedAt = triagedAt
	return nil
}
