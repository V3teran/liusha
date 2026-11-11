package task

import (
	"context"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 task 表的所有持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT / RETURNING 路径的统一列序，与 scan() 字段一一对应。
const colsSelect = "id, assignment_id, brief, target_host, status, paused, heartbeat_at, paused_ms, " +
	"created_at, ended_at, error_message"

const (
	defaultListLimit = 50 // 对齐 httpapi parseTaskFilter 的缺省 50——两层钳制数值必须一致
	maxListLimit     = 200
)

// Create 建一个新 task。Brief 必填；TargetHost 可空（runner 回填）。
func (s *Store) Create(ctx context.Context, p NewParams) (Task, error) {
	if p.AssignmentID == "" {
		return Task{}, fmt.Errorf("create task: assignment_id 必填")
	}
	if p.Brief == "" {
		return Task{}, fmt.Errorf("create task: brief 必填")
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO task (assignment_id, brief, target_host, status)
		VALUES ($1, $2, $3, 'active')
		RETURNING `+colsSelect, p.AssignmentID, p.Brief, p.TargetHost)
	var t Task
	if err := scan(row, &t); err != nil {
		return Task{}, fmt.Errorf("create task: %w", err)
	}
	return t, nil
}

// GetByID 按主键读取（不限 status）。
func (s *Store) GetByID(ctx context.Context, id string) (Task, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+colsSelect+" FROM task WHERE id=$1", id)
	var t Task
	if err := scan(row, &t); err != nil {
		return Task{}, fmt.Errorf("get task %s: %w", id, err)
	}
	return t, nil
}

// scan 是 colsSelect 列序的统一反序列化点。
func scan(r db.RowScanner, t *Task) error {
	var status string
	if err := r.Scan(&t.ID, &t.AssignmentID, &t.Brief, &t.TargetHost, &status, &t.Paused,
		&t.HeartbeatAt, &t.PausedMs,
		&t.CreatedAt, &t.EndedAt, &t.ErrorMessage); err != nil {
		return err
	}
	t.Status = Status(status)
	return nil
}

// ── 任务模块列表（前端 /tasks 页）───────────────────────────────────────────

// ListFilter 是任务列表的筛选条件（URL query 直读，语义对齐 parseTrafficFilter：
// 坏参数退化成不筛，不打 400）。Since/Until 按 created_at 过滤，零值=不筛。
type ListFilter struct {
	Search string    // brief / 批次ID 子串（ILIKE）
	Source string    // direct|proxy（所属 assignment 的真实来源）
	Status string    // active|completed|aborted
	Since  time.Time // 创建时间下界（含）
	Until  time.Time // 创建时间上界（含）
	Limit  int
	Offset int
}

// Summary 是任务列表行：task 本体 + 派生列（JOIN assignment/cron_schedule/
// conversation + finding/探索图步数计数）。列表页与详情板共用这一个形状。
type Summary struct {
	ID              string
	Brief           string
	TargetHost      string
	Paused          bool // 暂停中（仅 active 有意义）
	Status          Status
	AssignmentID    string
	AssignmentTitle string // 批次标题（assignment.title；空回退首个任务 brief）
	AssignmentIndex int    // 批内序号（1-based，按 created_at 排）
	AssignmentTotal int    // 批内任务总数
	Source          string // direct|proxy（assignment.source 原值；定时克隆批次落 direct）
	ScheduleID      string // 来源定时模板 id（assignment.schedule_id；非定时、或模板已删除为空）
	CronExpr        string // 来源模板的 cron 表达式（非定时为空）
	ConversationID  string
	FindingCount    int
	// StepCount 是认知循环推理步数（reasoning 终态数，最近一次 dispatched 之后）——
	// 与对话时间线卡片的「第 N 步」同一口径。会话无推理步（后台任务/旧数据）时
	// 回退探索图 action 节点数。
	StepCount int
	// TokensIn/TokensOut 是本任务累计 LLM 输入/输出 tokens（llm_invocation 全量
	// SUM）——任务列表的消耗信号，拆双向与对话页同形式（输入=上下文成本、
	// 输出=生成成本，二者成本结构不同，合计会抹掉这个差异）。
	TokensIn  int64
	TokensOut int64
	CreatedAt time.Time
	EndedAt   *time.Time
}

// summaryStepCount 是 StepCount 的统一口径表达式：会话内最近一次 dispatched 之后的
// reasoning 终态数，无推理步（后台任务/旧数据）回退探索图 action 节点数。
// 列表与单查共用一份——内联副本漂移正是 GetSummary 500（15 列 vs 17 目标）的根因。
const summaryStepCount = `COALESCE(NULLIF((SELECT count(*) FROM message m
		WHERE m.conversation_id = c.id
		  AND m.metadata->>'kind' = 'reasoning'
		  AND m.seq > COALESCE((SELECT max(seq) FROM message
		        WHERE conversation_id = c.id
		          AND metadata->>'kind' = 'task_status'
		          AND metadata->>'status' = 'dispatched'), 0)), 0),
	(SELECT count(*) FROM exploration_node n WHERE n.task_id = t.id AND n.kind = 'action'))`

// summarySelect 是任务列表行 SELECT 的唯一事实源，与 scanSummary 17 目标一一对应。
const summarySelect = `
	t.id, t.brief, COALESCE(t.target_host,''), t.status::text, t.paused,
	t.assignment_id::text,
	COALESCE(NULLIF(a.title, ''), t.brief) AS assignment_title,
	ROW_NUMBER() OVER (PARTITION BY t.assignment_id ORDER BY t.created_at, t.id),
	COUNT(*)     OVER (PARTITION BY t.assignment_id),
	a.source,
	COALESCE(a.schedule_id::text, ''),
	COALESCE(cs.cron_expr, ''),
	COALESCE(c.id::text, ''),
	(SELECT count(*) FROM finding f WHERE f.task_id = t.id),
	` + summaryStepCount + `,
	(SELECT COALESCE(SUM(li.in_tokens), 0) FROM llm_invocation li WHERE li.task_id = t.id),
	(SELECT COALESCE(SUM(li.out_tokens), 0) FROM llm_invocation li WHERE li.task_id = t.id),
	t.created_at, t.ended_at`

// summaryWhere 是 List/Count 共用的筛选子句（$1 source / $2 status / $3 search /
// $4 since / $5 until）。since/until 传 NULL 表示不筛该端。
const summaryWhere = `
	WHERE ($1 = '' OR a.source = $1)
	  AND ($2 = '' OR t.status = $2)
	  AND ($3 = '' OR t.brief ILIKE '%' || $3 || '%' OR t.assignment_id::text ILIKE '%' || $3 || '%')
	  AND ($4::timestamptz IS NULL OR t.created_at >= $4)
	  AND ($5::timestamptz IS NULL OR t.created_at <= $5)`

// timeArg 把零值时间折叠成 NULL（pgx 传零值 time.Time 会落成 year-1 而非 NULL）。
func timeArg(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func scanSummary(r db.RowScanner, s *Summary) error {
	var status, source string
	if err := r.Scan(&s.ID, &s.Brief, &s.TargetHost, &status, &s.Paused,
		&s.AssignmentID, &s.AssignmentTitle, &s.AssignmentIndex, &s.AssignmentTotal, &source, &s.ScheduleID,
		&s.CronExpr, &s.ConversationID, &s.FindingCount, &s.StepCount,
		&s.TokensIn, &s.TokensOut, &s.CreatedAt, &s.EndedAt); err != nil {
		return err
	}
	s.Status = Status(status)
	s.Source = source
	return nil
}

// ListFiltered 按筛选分页列出任务（created_at DESC）。来源经 JOIN assignment
// 解析，对话链接 LEFT JOIN conversation，步数/漏洞数为关联子查询。
func (s *Store) ListFiltered(ctx context.Context, f ListFilter) ([]Summary, error) {
	f.Limit = clampListLimit(f.Limit)
	if f.Offset < 0 {
		f.Offset = 0
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+summarySelect+`
		FROM task t
		JOIN assignment a ON a.id = t.assignment_id
		LEFT JOIN cron_schedule cs ON cs.id = a.schedule_id
		LEFT JOIN conversation c ON c.task_id = t.id`+summaryWhere+`
		ORDER BY t.created_at DESC, t.id LIMIT $6 OFFSET $7`,
		f.Source, f.Status, f.Search, timeArg(f.Since), timeArg(f.Until), f.Limit, f.Offset)
	if err != nil {
		return nil, fmt.Errorf("list tasks filtered: %w", err)
	}
	defer rows.Close()
	var out []Summary
	for rows.Next() {
		var sm Summary
		if err := scanSummary(rows, &sm); err != nil {
			return nil, fmt.Errorf("scan task summary: %w", err)
		}
		out = append(out, sm)
	}
	return out, rows.Err()
}

// CountFiltered 与 ListFiltered 同口径计数（分页 total）。
func (s *Store) CountFiltered(ctx context.Context, f ListFilter) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM task t
		JOIN assignment a ON a.id = t.assignment_id`+summaryWhere,
		f.Source, f.Status, f.Search, timeArg(f.Since), timeArg(f.Until)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count tasks filtered: %w", err)
	}
	return n, nil
}

// GetSummary 取单个任务的列表形状（详情板），列序与 scanSummary 17 目标严格一致
// （summarySelect 的单行变体）。pgx.ErrNoRows 由调用方映射 404。
//
// 批内序号/总数不能用 ListFiltered 的窗口函数——WHERE t.id=$1 先把同批兄弟行
// 滤掉，COUNT OVER PARTITION 恒 1；此处用关联标量子查询对全批计数。
func (s *Store) GetSummary(ctx context.Context, id string) (Summary, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT t.id, t.brief, COALESCE(t.target_host,''), t.status::text, t.paused,
			t.assignment_id::text,
			COALESCE(NULLIF(a.title, ''), t.brief) AS assignment_title,
			(SELECT count(*) + 1 FROM task t2
			 WHERE t2.assignment_id = t.assignment_id
			   AND (t2.created_at, t2.id) < (t.created_at, t.id)),
			(SELECT count(*) FROM task t3 WHERE t3.assignment_id = t.assignment_id),
			a.source,
			COALESCE(a.schedule_id::text, ''),
			COALESCE(cs.cron_expr, ''),
			COALESCE(c.id::text, ''),
			(SELECT count(*) FROM finding f WHERE f.task_id = t.id),
			`+summaryStepCount+`,
			(SELECT COALESCE(SUM(li.in_tokens), 0) FROM llm_invocation li WHERE li.task_id = t.id),
			(SELECT COALESCE(SUM(li.out_tokens), 0) FROM llm_invocation li WHERE li.task_id = t.id),
			t.created_at, t.ended_at
		FROM task t
		JOIN assignment a ON a.id = t.assignment_id
		LEFT JOIN cron_schedule cs ON cs.id = a.schedule_id
		LEFT JOIN conversation c ON c.task_id = t.id
		WHERE t.id = $1`, id)
	var sm Summary
	if err := scanSummary(row, &sm); err != nil {
		return Summary{}, fmt.Errorf("get task summary %s: %w", id, err)
	}
	return sm, nil
}

// clampListLimit 收敛 Limit 到 [1, maxListLimit]，<=0 取默认。
func clampListLimit(n int) int {
	if n <= 0 {
		return defaultListLimit
	}
	if n > maxListLimit {
		return maxListLimit
	}
	return n
}
