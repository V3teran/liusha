package finding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/logx"
)

// findingLog 包级 logger，用于 Save 路径的 info / warn 记录。
var findingLog = logx.New("finding")

// Store 封装 finding 表的所有持久化操作。
//
// 设计要点：
//   - severity 自由文本（前端按前缀配色）
//   - dedup 在 DB 层完成（(task_id, dedup_key) ON CONFLICT），调用方无需自查
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT / RETURNING 路径的统一列序，与 scan() 字段一一对应。
const colsSelect = "id, task_id::text AS task_id, " +
	"agent_run_id, host, severity, summary, target, evaluation, " +
	"COALESCE(cwe_id, ''), COALESCE(owasp_category, ''), first_seen_at, COALESCE(remediation, ''), " +
	"status, COALESCE(triage_note, ''), triaged_at, created_at, seq, repro"

// Save 按 (task_id, dedup_key) UPSERT 一行 finding。
//
// 流程：INSERT ON CONFLICT → commit。dedup 在 DB 层无声完成，对调用方幂等。
func (s *Store) Save(ctx context.Context, f Finding) (Finding, error) {
	if f.Host == "" {
		return Finding{}, fmt.Errorf("finding.Host 必填")
	}
	if f.Summary == "" {
		return Finding{}, fmt.Errorf("finding.Summary 必填")
	}
	if f.Severity == "" {
		f.Severity = "medium"
	}
	for _, p := range []*json.RawMessage{&f.Target, &f.Evaluation} {
		if *p == nil {
			*p = json.RawMessage("{}")
		}
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Finding{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // commit 后 rollback 是 no-op

	// UNIQUE(task_id, dedup_key)：dedup_key 是 PG 生成列（host + cwe + 规范化路径 + method +
	// parameter；target 缺 path 时取 summary 前 40 字），单个 task 内去重。
	// 多 agent 并发写同一漏洞时，ON CONFLICT 保留首个写入（first_seen_at 取较早），后续 dup
	// 不报错而是返回 existing 行——LLM 视角 Save 始终幂等成功，dedup 在 DB 层无声完成。
	row := tx.QueryRow(ctx, `
		INSERT INTO finding
			(task_id, agent_run_id, host, severity, summary, target, evaluation,
			 cwe_id, owasp_category, remediation, repro)
		VALUES ($1::uuid, $2,$3,$4,$5,$6,$7, NULLIF($8,''), NULLIF($9,''), NULLIF($10,''), $11)
		ON CONFLICT (task_id, dedup_key) DO UPDATE
		SET first_seen_at = LEAST(finding.first_seen_at, EXCLUDED.first_seen_at)
		RETURNING `+colsSelect,
		f.TaskID,
		f.AgentRunID, f.Host, f.Severity,
		f.Summary, f.Target, f.Evaluation,
		f.CWEID, f.OWASPCategory, f.Remediation, f.Repro)

	var saved Finding
	if err := scan(row, &saved); err != nil {
		return Finding{}, fmt.Errorf("upsert finding: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Finding{}, fmt.Errorf("commit: %w", err)
	}

	// dup 命中：DB 层把后续重复写无声合并到已有行；saved 是 existing 行内容。
	// agent_run_id 不会被覆盖（DO UPDATE 只动 first_seen_at），所以 saved.AgentRunID 反映首次写入者。
	dedupHit := f.AgentRunID != nil && saved.AgentRunID != nil && *f.AgentRunID != *saved.AgentRunID
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

// ListByTask 列出 task 下所有 finding（按 created_at desc）。
func (s *Store) ListByTask(ctx context.Context, taskID string) ([]Finding, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+colsSelect+` FROM finding WHERE task_id=$1::uuid ORDER BY created_at DESC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list findings by task: %w", err)
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
	return out, rows.Err()
}

// LedgerRow 是全局漏洞台账的一行：finding 主体 + JOIN task/assignment/conversation
// 派生的跳转上下文。漏洞页跨 task/host 全量展示用，区别于 per-task 的 Finding 列表。
type LedgerRow struct {
	Finding
	Source         string // 关联 assignment 的 source（direct 人工下发 / proxy 代理捕获）
	ConversationID string // 关联会话 id（详情跳对话用；无会话为空）
}

// LedgerFilter 是台账查询的可选筛选（零值=不筛该维度）+ 分页。
// Limit<=0 时 ListAll 不分页（历史行为，内部调用方/测试用）；handler 层始终传 >0。
type LedgerFilter struct {
	// 范围：只看这个任务 / 这个对话的漏洞（从任务、对话跳来时用；一个任务至多一个对话）
	TaskID         string
	ConversationID string
	Host           string
	Severity       string
	Status         string
	Source         string // direct / proxy（下发来源）
	Search         string // 关键词：匹配漏洞摘要、主机、路径与所属任务的目标（包含、大小写不敏感、按字面）
	Limit          int
	Offset         int
}

// ledgerFrom 是台账查询共用的 FROM 子句：漏洞经任务带出批次（来源）与对话（跳转、按对话筛选）。
const ledgerFrom = `
		FROM finding f
		JOIN task t ON t.id = f.task_id
		JOIN assignment a ON a.id = t.assignment_id
		LEFT JOIN conversation c ON c.task_id = t.id`

// ledgerWhere 拼 LedgerFilter 的 WHERE 子句（ListAll / CountAll 共用，防筛选口径漂移）。
func ledgerWhere(f LedgerFilter) (string, []any) {
	where := make([]string, 0, 5)
	args := make([]any, 0, 5)
	idx := 1
	add := func(clause string, val any) {
		where = append(where, fmt.Sprintf(clause, idx))
		args = append(args, val)
		idx++
	}
	if f.TaskID != "" {
		add("f.task_id = $%d", f.TaskID)
	}
	if f.ConversationID != "" {
		add("c.id = $%d", f.ConversationID)
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
	if f.Source != "" {
		add("a.source = $%d", f.Source)
	}
	if strings.TrimSpace(f.Search) != "" {
		add("(f.summary ILIKE $%[1]d OR f.host ILIKE $%[1]d OR f.target->>'path' ILIKE $%[1]d OR t.goal ILIKE $%[1]d)",
			db.ContainsPattern(f.Search))
	}
	if len(where) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(where, " AND "), args
}

// ListAll 全局漏洞台账查询：跨任务 / 主机平铺列出漏洞，JOIN task/assignment/conversation 带出来源与所属会话，按可选维度筛选。
//
// 不指定任务时跨任务一网打尽，按 seq desc 排序（对外顺序号倒序，最新发现优先，
// 且是全局单调序，不会像 created_at 那样同批写入并列时出现顺序不稳定）。
// Limit>0 时分页（Offset 配合翻页）；Limit<=0 时不限（历史全量口径，内部调用方用）。
//
// 不做去重聚合：漏洞按「每次扫描各自独立」建模——同一个洞被多次扫描就是多条独立 finding，
// 各自有各自的 triage 处置态，互不影响。台账平铺全部，不折叠。
func (s *Store) ListAll(ctx context.Context, f LedgerFilter) ([]LedgerRow, error) {
	where, args := ledgerWhere(f)
	q := `SELECT ` + ledgerCols + `, a.source, COALESCE(c.id::text, '')` + ledgerFrom +
		where + " ORDER BY f.seq DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", f.Limit, f.Offset)
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

// CountAll 返回同筛选口径下的全局总行数（分页 total；忽略 Limit/Offset）。
func (s *Store) CountAll(ctx context.Context, f LedgerFilter) (int, error) {
	where, args := ledgerWhere(f)
	q := `SELECT count(*)` + ledgerFrom + where
	var n int
	if err := s.pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count all findings: %w", err)
	}
	return n, nil
}

// TaskFacet 是台账「任务」筛选的一个选项：有漏洞的任务及其漏洞数。同一目标常被反复下发，
// 目标相同的任务靠创建时间区分。
type TaskFacet struct {
	TaskID    string
	Goal      string
	CreatedAt time.Time
	Count     int
}

// TaskFacets 列出有漏洞的任务（任务筛选的选项），按最近一次发现倒序，至多 limit 个，more 报告
// 是否还有更多（选项须靠 goalQuery 按目标搜索缩小，而不是一次全列出——任务会持续增长）。
// 按分面惯例，结构化筛选（来源、状态、严重度、主机）照常生效，范围（任务 / 对话）不作用于
// 任务选项——限定在一个任务或对话上时其余任务仍可选；台账关键词搜索针对漏洞本身，不作用于任务选项。
func (s *Store) TaskFacets(ctx context.Context, f LedgerFilter, goalQuery string, limit int) (facets []TaskFacet, more bool, err error) {
	f.TaskID, f.ConversationID, f.Search = "", "", ""
	where, args := ledgerWhere(f)
	if strings.TrimSpace(goalQuery) != "" {
		args = append(args, db.ContainsPattern(goalQuery))
		cond := fmt.Sprintf("t.goal ILIKE $%d", len(args))
		if where == "" {
			where = " WHERE " + cond
		} else {
			where += " AND " + cond
		}
	}
	args = append(args, limit+1)
	rows, err := s.pool.Query(ctx, `SELECT t.id::text, t.goal, t.created_at, count(*)`+ledgerFrom+where+`
		GROUP BY t.id, t.goal, t.created_at
		ORDER BY max(f.seq) DESC
		LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, false, fmt.Errorf("finding task facets: %w", err)
	}
	defer rows.Close()
	out := []TaskFacet{}
	for rows.Next() {
		var fc TaskFacet
		if err := rows.Scan(&fc.TaskID, &fc.Goal, &fc.CreatedAt, &fc.Count); err != nil {
			return nil, false, fmt.Errorf("scan task facet: %w", err)
		}
		out = append(out, fc)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("finding task facets: %w", err)
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// Stat 是台账头部统计 chip 的数据源：与 ListAll/CountAll 同一筛选口径
// （ledgerWhere 单一口径），按 status / severity 维度聚合——口径与列表一致，
// 前端不再拿当前页行自算（同屏两口径的自相矛盾曾发生在 LLM 审计页）。
type Stat struct {
	Total      int            `json:"total"`
	ByStatus   map[string]int `json:"by_status"`
	BySeverity map[string]int `json:"by_severity"`
}

// StatFiltered 按筛选聚合 status/severity 分布（单条 SQL 条件计数，与翻页无关）。
func (s *Store) StatFiltered(ctx context.Context, f LedgerFilter) (Stat, error) {
	where, args := ledgerWhere(f)
	q := `SELECT count(*),
			count(*) FILTER (WHERE f.status = 'open'),
			count(*) FILTER (WHERE f.status = 'confirmed'),
			count(*) FILTER (WHERE f.severity = 'critical'),
			count(*) FILTER (WHERE f.severity = 'high'),
			count(*) FILTER (WHERE f.severity = 'medium'),
			count(*) FILTER (WHERE f.severity = 'low')
` + ledgerFrom + where
	var st Stat
	var open, confirmed, critical, high, medium, low int
	if err := s.pool.QueryRow(ctx, q, args...).
		Scan(&st.Total, &open, &confirmed, &critical, &high, &medium, &low); err != nil {
		return Stat{}, fmt.Errorf("stat findings: %w", err)
	}
	st.ByStatus = map[string]int{"open": open, "confirmed": confirmed}
	st.BySeverity = map[string]int{"critical": critical, "high": high, "medium": medium, "low": low}
	return st, nil
}

// UpdateTriage 人工处置一条 finding：状态 + 严重度 + 备注，triaged_at 打当前时刻，RETURNING 更新后的行。
//
// status 必须是五态之一（DB CHECK 兜底，这里前置校验给出友好错误）。
// severity 传空则不改（保留扫描时 LLM 定的值）；非空则直接覆盖——人工可修正 LLM 定级（triage 标配）。
// note 可空。finding 不存在返错（ErrNoRows）。返回行含后端权威 triaged_at（前端据此覆盖乐观值）。
func (s *Store) UpdateTriage(ctx context.Context, id, status, severity, note string) (Finding, error) {
	if id == "" {
		return Finding{}, fmt.Errorf("finding.UpdateTriage: id 必填")
	}
	if !validStatus(status) {
		return Finding{}, fmt.Errorf("finding.UpdateTriage: 非法 status %q（须为 open/confirmed/fixed/false_positive/accepted）", status)
	}
	// severity 用 NULLIF 空跳过：传空保留原值，非空覆盖。COALESCE 让 SQL 单句表达「空则不改」。
	row := s.pool.QueryRow(ctx,
		`UPDATE finding
		 SET status = $1, severity = COALESCE(NULLIF($2, ''), severity),
		     triage_note = NULLIF($3, ''), triaged_at = now()
		 WHERE id = $4
		 RETURNING `+colsSelect,
		status, severity, note, id)
	var updated Finding
	if err := scan(row, &updated); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Finding{}, fmt.Errorf("update finding triage %s: not found", id)
		}
		return Finding{}, fmt.Errorf("update finding triage %s: %w", id, err)
	}
	findingLog.Info().Str("finding_id", id).Str("status", status).Str("severity", updated.Severity).Msg("finding triaged ✓")
	return updated, nil
}

// validStatus 校验 triage 五态（与 DB CHECK 约束 finding_status_check 一致）。
func validStatus(s string) bool {
	switch s {
	case "open", "confirmed", "fixed", "false_positive", "accepted":
		return true
	}
	return false
}

// ledgerCols 是台账 JOIN 查询的列序（= colsSelect 但每列显式加 f. 前缀）。
// 不用程序化前缀：colsSelect 含 COALESCE(...) 等内部带逗号的表达式，
// 按 ", " 切分会劈碎；且 JOIN task 后 id/status/created_at 列名歧义，必须 f. 限定。
// 与 colsSelect 手工对齐；scanLedger 列序 = 本常量 + 末尾 source, conversation_id。
const ledgerCols = "f.id, f.task_id::text AS task_id, " +
	"f.agent_run_id, f.host, f.severity, f.summary, f.target, f.evaluation, " +
	"COALESCE(f.cwe_id, ''), COALESCE(f.owasp_category, ''), f.first_seen_at, COALESCE(f.remediation, ''), " +
	"f.status, COALESCE(f.triage_note, ''), f.triaged_at, f.created_at, f.seq, f.repro"

// scanLedger 扫 ledgerCols 列序 + 末尾 source, conversation_id（比 scan() 多两列）。
func scanLedger(r db.RowScanner, out *LedgerRow) error {
	var agentID *string
	var triagedAt *time.Time
	if err := r.Scan(
		&out.ID, &out.TaskID,
		&agentID, &out.Host, &out.Severity,
		&out.Summary, &out.Target, &out.Evaluation,
		&out.CWEID, &out.OWASPCategory, &out.FirstSeenAt, &out.Remediation,
		&out.Status, &out.TriageNote, &triagedAt,
		&out.CreatedAt, &out.Seq, &out.Repro,
		&out.Source, &out.ConversationID,
	); err != nil {
		return err
	}
	out.AgentRunID = agentID
	out.TriagedAt = triagedAt
	return nil
}

// scan 是 colsSelect 列序的统一反序列化点。
// agentID 用指针接住 NULL（AgentRunID 是 *string，保留 nil）。
func scan(r db.RowScanner, f *Finding) error {
	var agentID *string
	var triagedAt *time.Time
	if err := r.Scan(
		&f.ID, &f.TaskID,
		&agentID, &f.Host, &f.Severity,
		&f.Summary, &f.Target, &f.Evaluation,
		&f.CWEID, &f.OWASPCategory, &f.FirstSeenAt, &f.Remediation,
		&f.Status, &f.TriageNote, &triagedAt,
		&f.CreatedAt, &f.Seq, &f.Repro,
	); err != nil {
		return err
	}
	f.AgentRunID = agentID
	f.TriagedAt = triagedAt
	return nil
}
