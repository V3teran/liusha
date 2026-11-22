package task

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/explorationgraph"
)

// Store 封装 task 表的所有持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT / RETURNING 路径的统一列序，与 scan() 字段一一对应。
const colsSelect = "id, assignment_id, goal, target_host, status, paused, paused_ms, " +
	"created_at, ended_at, abort_reason, scenario, scenario_name, scenario_body"

const (
	defaultListLimit = 50 // 对齐 httpapi parseTaskFilter 的缺省 50——两层钳制数值必须一致
	maxListLimit     = 200
)

// Create 建一个新 task，并在同一事务内建它的根目标（目标描述 = Goal）：任务从诞生起
// 就有根目标，两份目标只经本包写入、不会分叉。
func (s *Store) Create(ctx context.Context, p NewParams) (Task, error) {
	if p.AssignmentID == "" {
		return Task{}, fmt.Errorf("create task: assignment_id 必填")
	}
	if p.Goal == "" {
		return Task{}, fmt.Errorf("create task: goal 必填")
	}
	var t Task
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO task (assignment_id, goal, target_host, status, scenario, scenario_name, scenario_body)
			VALUES ($1, $2, $3, 'active', $4, $5, $6)
			RETURNING `+colsSelect, append([]any{p.AssignmentID, p.Goal, targetHostOf(p.Goal)}, scenarioArgs(p.Scenario)...)...)
		if err := scan(row, &t); err != nil {
			return err
		}
		return explorationgraph.CreateRootObjective(ctx, tx, t.ID, p.Goal)
	})
	if err != nil {
		return Task{}, fmt.Errorf("create task: %w", err)
	}
	return t, nil
}

// SetGoal 改写进行中任务的目标——目标的唯一写入口：任务行（连同派生的 target_host）
// 与根目标描述在同一事务内写入；根目标版本随之递增，按旧目标做出的规划结论失效（见
// explorationgraph.GoalBasis）。内容不变什么都不写；任务已结束返回 ErrNotActive。
func (s *Store) SetGoal(ctx context.Context, id, goal string) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := SetGoalTx(ctx, tx, id, goal)
		return err
	})
	if err != nil {
		return fmt.Errorf("task %s 调整目标: %w", id, err)
	}
	return nil
}

// SetGoalTx 在调用方事务内执行 SetGoal（先锁任务行），返回目标是否有变化——供需要与
// 其它写入原子落定的调用方（如运行中引导）使用。
func SetGoalTx(ctx context.Context, tx pgx.Tx, id, goal string) (bool, error) {
	if goal == "" {
		return false, fmt.Errorf("goal 必填")
	}
	current, err := LockActive(ctx, tx, id)
	if err != nil {
		return false, err
	}
	if current.Goal == goal {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE task SET goal = $2, target_host = $3 WHERE id = $1`, id, goal, targetHostOf(goal)); err != nil {
		return false, fmt.Errorf("改写任务目标: %w", err)
	}
	if err := explorationgraph.WriteRootGoal(ctx, tx, id, goal); err != nil {
		return false, err
	}
	return true, nil
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

// Lock 在调用方事务内锁住任务行（FOR UPDATE），返回锁定时的任务行。运行中引导、收敛
// 完成都先锁任务行再写图，彼此串行：判定不会基于过期的状态。不存在返回包裹
// pgx.ErrNoRows 的错误。
func Lock(ctx context.Context, tx pgx.Tx, id string) (Task, error) {
	var t Task
	if err := scan(tx.QueryRow(ctx, "SELECT "+colsSelect+" FROM task WHERE id = $1 FOR UPDATE", id), &t); err != nil {
		return Task{}, fmt.Errorf("锁定任务 %s: %w", id, err)
	}
	return t, nil
}

// DirectoryLabels 给工作区的批次 / 任务目录取显示名：批次取批次名，任务取目标。
// 返回 ID → 显示名，查不到的 ID 不出现。
func (s *Store) DirectoryLabels(ctx context.Context, assignmentIDs, taskIDs []string) (map[string]string, map[string]string, error) {
	asgLabels := make(map[string]string, len(assignmentIDs))
	taskLabels := make(map[string]string, len(taskIDs))
	if len(assignmentIDs) > 0 {
		rows, err := s.pool.Query(ctx, `
			SELECT id::text, title FROM assignment WHERE id = ANY($1::uuid[])`, assignmentIDs)
		if err != nil {
			return nil, nil, fmt.Errorf("批次显示名: %w", err)
		}
		if err := collectLabels(rows, asgLabels); err != nil {
			return nil, nil, fmt.Errorf("批次显示名: %w", err)
		}
	}
	if len(taskIDs) > 0 {
		rows, err := s.pool.Query(ctx, `SELECT id::text, goal FROM task WHERE id = ANY($1::uuid[])`, taskIDs)
		if err != nil {
			return nil, nil, fmt.Errorf("任务显示名: %w", err)
		}
		if err := collectLabels(rows, taskLabels); err != nil {
			return nil, nil, fmt.Errorf("任务显示名: %w", err)
		}
	}
	return asgLabels, taskLabels, nil
}

// collectLabels 把 (id, label) 行收进 map（空显示名不收）。
func collectLabels(rows pgx.Rows, into map[string]string) error {
	defer rows.Close()
	for rows.Next() {
		var id, label string
		if err := rows.Scan(&id, &label); err != nil {
			return err
		}
		if label != "" {
			into[id] = label
		}
	}
	return rows.Err()
}

// LockActive 同 Lock，并要求任务进行中（暂停中也算）：已结束返回 ErrNotActive。
func LockActive(ctx context.Context, tx pgx.Tx, id string) (Task, error) {
	t, err := Lock(ctx, tx, id)
	if err != nil {
		return Task{}, err
	}
	if t.Status != StatusActive {
		return Task{}, ErrNotActive
	}
	return t, nil
}

// scan 是 colsSelect 列序的统一反序列化点。
func scan(r db.RowScanner, t *Task) error {
	var status string
	var scCode, scName, scBody *string
	if err := r.Scan(&t.ID, &t.AssignmentID, &t.Goal, &t.TargetHost, &status, &t.Paused,
		&t.PausedMs,
		&t.CreatedAt, &t.EndedAt, &t.AbortReason, &scCode, &scName, &scBody); err != nil {
		return err
	}
	t.Status = Status(status)
	t.Scenario = nil
	if scCode != nil {
		t.Scenario = &Scenario{Code: *scCode, Name: deref(scName), Body: deref(scBody)}
	}
	return nil
}

// scenarioArgs 把场景快照展开成 (scenario, scenario_name, scenario_body) 三个参数；无场景三列皆空。
func scenarioArgs(sc *Scenario) []any {
	if sc == nil {
		return []any{nil, nil, nil}
	}
	return []any{sc.Code, sc.Name, sc.Body}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ── 任务模块列表（前端 /tasks 页）───────────────────────────────────────────

// ListFilter 是任务列表的筛选条件，零值 = 不筛该维度。Since/Until 按 created_at 过滤。
type ListFilter struct {
	// 范围：只看这个任务 / 这个对话的任务（从其它页面跳来时用；一个任务至多一个对话）
	TaskID         string
	ConversationID string
	Search         string    // 目标 / 批次名 子串（按字面、大小写不敏感）
	Source         string    // direct|proxy（所属 assignment 的真实来源）
	Status         string    // active|completed|aborted
	Since          time.Time // 创建时间下界（含）
	Until          time.Time // 创建时间上界（含）
	Limit          int
	Offset         int
}

// Summary 是任务列表行：task 本体 + 派生列（JOIN assignment/cron_schedule/
// conversation + finding/探索图步数计数）。列表页与详情板共用这一个形状。
type Summary struct {
	ID              string
	Goal            string
	TargetHost      string
	Paused          bool // 暂停中（仅 active 有意义）
	Status          Status
	AssignmentID    string
	AssignmentTitle string // 批次名（assignment.title，写入时确定、不为空）
	AssignmentIndex int    // 批内序号（1-based，按 created_at 排）
	AssignmentTotal int    // 批内任务总数
	Source          string // direct|proxy（assignment.source 原值；定时克隆批次落 direct）
	ScheduleID      string // 来源定时模板 id（assignment.schedule_id；非定时、或模板已删除为空）
	CronExpr        string // 来源模板的 cron 表达式（非定时为空）
	ConversationID  string
	ScenarioCode    string // 建任务时选定的场景（空 = 无场景）
	ScenarioName    string // 场景名称快照
	ScenarioDeleted bool   // 场景此后已被删除（任务照常读自身快照）
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
	// AbortReason 是终止的原因（「用户手动停止」「任务运行超时」……）；未终止为空。
	AbortReason string
}

// summaryStepCount 是 StepCount 的统一口径表达式：会话内最近一次 dispatched 之后的
// reasoning 终态数，无推理步（后台任务/旧数据）回退探索图 action 节点数。
const summaryStepCount = `COALESCE(NULLIF((SELECT count(*) FROM message m
		WHERE m.conversation_id = c.id
		  AND m.metadata->>'kind' = 'reasoning'
		  AND m.seq > COALESCE((SELECT max(seq) FROM message
		        WHERE conversation_id = c.id
		          AND metadata->>'kind' = 'task_status'
		          AND metadata->>'status' = 'dispatched'), 0)), 0),
	(SELECT count(*) FROM exploration_node n WHERE n.task_id = t.id AND n.kind = 'action'))`

// summarySelect 是任务摘要（列表行与详情板）SELECT 的唯一事实源，与 scanSummary 一一对应。
// 批次内序号与总数按整个批次计（关联子查询，走 task_assignment_idx）：窗口函数只在筛选后的
// 结果里计数，按状态、搜索或范围筛掉兄弟任务后会错成 [1/1]。
const summarySelect = `
	t.id, t.goal, COALESCE(t.target_host,''), t.status::text, t.paused,
	t.assignment_id::text,
	a.title AS assignment_title,
	(SELECT count(*) FROM task t2
	  WHERE t2.assignment_id = t.assignment_id AND (t2.created_at, t2.id) <= (t.created_at, t.id)),
	(SELECT count(*) FROM task t3 WHERE t3.assignment_id = t.assignment_id),
	a.source,
	COALESCE(a.schedule_id::text, ''),
	COALESCE(cs.cron_expr, ''),
	COALESCE(c.id::text, ''),
	COALESCE(t.scenario, ''), COALESCE(t.scenario_name, ''),
	(t.scenario IS NOT NULL AND NOT EXISTS (SELECT 1 FROM scenario sc WHERE sc.code = t.scenario)),
	(SELECT count(*) FROM finding f WHERE f.task_id = t.id),
	` + summaryStepCount + `,
	(SELECT COALESCE(SUM(li.in_tokens), 0) FROM llm_invocation li WHERE li.task_id = t.id),
	(SELECT COALESCE(SUM(li.out_tokens), 0) FROM llm_invocation li WHERE li.task_id = t.id),
	t.created_at, t.ended_at, t.abort_reason`

// searchArg 把搜索框输入转成「包含」模式（元字符按字面匹配）；空输入保持空串，筛选子句据此不筛。
func searchArg(q string) string {
	if strings.TrimSpace(q) == "" {
		return ""
	}
	return db.ContainsPattern(q)
}

// summaryFrom 是任务摘要共用的 FROM 子句：任务带出批次、定时模板与对话（一个任务至多一个对话）。
const summaryFrom = `
	FROM task t
	JOIN assignment a ON a.id = t.assignment_id
	LEFT JOIN cron_schedule cs ON cs.id = a.schedule_id
	LEFT JOIN conversation c ON c.task_id = t.id`

// summaryWhere 是 List/Count 共用的筛选子句（$1 source / $2 status / $3 search /
// $4 since / $5 until / $6 任务 / $7 对话）。空串与 NULL 表示不筛该维度。
const summaryWhere = `
	WHERE ($1 = '' OR a.source = $1)
	  AND ($2 = '' OR t.status = $2)
	  AND ($3 = '' OR t.goal ILIKE $3 OR a.title ILIKE $3)
	  AND ($4::timestamptz IS NULL OR t.created_at >= $4)
	  AND ($5::timestamptz IS NULL OR t.created_at <= $5)
	  AND ($6 = '' OR t.id::text = $6)
	  AND ($7 = '' OR c.id::text = $7)`

// summaryArgs 是 summaryWhere 的参数（顺序与占位符一一对应）。
func summaryArgs(f ListFilter) []any {
	return []any{f.Source, f.Status, searchArg(f.Search), timeArg(f.Since), timeArg(f.Until), f.TaskID, f.ConversationID}
}

// timeArg 把零值时间折叠成 NULL（pgx 传零值 time.Time 会落成 year-1 而非 NULL）。
func timeArg(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func scanSummary(r db.RowScanner, s *Summary) error {
	var status, source string
	if err := r.Scan(&s.ID, &s.Goal, &s.TargetHost, &status, &s.Paused,
		&s.AssignmentID, &s.AssignmentTitle, &s.AssignmentIndex, &s.AssignmentTotal, &source, &s.ScheduleID,
		&s.CronExpr, &s.ConversationID, &s.ScenarioCode, &s.ScenarioName, &s.ScenarioDeleted, &s.FindingCount, &s.StepCount,
		&s.TokensIn, &s.TokensOut, &s.CreatedAt, &s.EndedAt, &s.AbortReason); err != nil {
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
	rows, err := s.pool.Query(ctx, `SELECT `+summarySelect+summaryFrom+summaryWhere+`
		ORDER BY t.created_at DESC, t.id LIMIT $8 OFFSET $9`,
		append(summaryArgs(f), f.Limit, f.Offset)...)
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
	err := s.pool.QueryRow(ctx, `SELECT count(*)`+summaryFrom+summaryWhere, summaryArgs(f)...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count tasks filtered: %w", err)
	}
	return n, nil
}

// GetSummary 取单个任务的摘要（详情板），与列表行同一 SELECT。pgx.ErrNoRows 由调用方映射 404。
func (s *Store) GetSummary(ctx context.Context, id string) (Summary, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+summarySelect+summaryFrom+` WHERE t.id = $1`, id)
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
