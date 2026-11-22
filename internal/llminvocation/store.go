package llminvocation

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/logx"
)

// Store 封装 llm_call 表的所有持久化操作。
//
// 写入采用 channel + 后台 worker 批量 INSERT 模式（性能优化）：
//   - Append 把 Invocation 推到 buffered channel 立即返回，Generate 路径 0 阻塞
//   - 后台 worker 累积 batchSize=100 行或 flushInterval=1s 触发一次批量 INSERT
//   - Close() 优雅关闭：停 worker + 清空剩余 buffer + 最后一次 flush
//
// 设计取舍：
//   - Append 只回报入队结果，不回传 id（异步路径无 RETURNING）。
//   - 进程崩溃可能丢 buffer 内未 flush 的行（最差 100 行 / 1s）；这是行为日志非交易，可接受。
//   - 一致性场景（如 AggregateByTask / ListByTask）调用方需先调 Flush() 同步等待。
type Store struct {
	pool          *pgxpool.Pool
	ch            chan Invocation
	flushReq      chan chan struct{} // Flush() 请求 worker 立即落库；worker 处理完向回传的 chan 发信号
	closed        chan struct{}
	wg            sync.WaitGroup
	log           zerolog.Logger
	batchSize     int
	flushInterval time.Duration
	insertTimeout time.Duration
}

// 批量写入参数：缓冲满则丢一行并告警；累积 batchSize 行或每 flushInterval 写一次。
// 写入机制的内部参数，不是部署方需要决定的事。
const (
	bufferSize    = 1024
	batchSize     = 100
	flushInterval = constants.LogFlushInterval
	insertTimeout = constants.DBQueryTimeout
)

// NewStore 构造 Store 并启动后台批量写入。进程退出前必须调用 Close() 排空缓冲，否则丢失最近写入。
func NewStore(pool *pgxpool.Pool) *Store {
	s := &Store{
		pool:          pool,
		ch:            make(chan Invocation, bufferSize),
		flushReq:      make(chan chan struct{}),
		closed:        make(chan struct{}),
		log:           logx.New("llminvocation.store"),
		batchSize:     batchSize,
		flushInterval: flushInterval,
		insertTimeout: insertTimeout,
	}
	s.wg.Add(1)
	go s.run()
	return s
}

// Append 把 Invocation 推到内部 channel；channel 满则丢一行并 warn（不阻塞 Generate）。
// 异步落库，不回传 id。
func (s *Store) Append(ctx context.Context, c Invocation) error {
	if len(c.Messages) == 0 {
		c.Messages = []byte("[]")
	}
	if len(c.Result) == 0 {
		c.Result = []byte("{}")
	}
	select {
	case s.ch <- c:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		// channel 满（>1024 条）→ 丢这条，说明 LLM 调用速率超 worker batch 写盘吞吐。
		s.log.Warn().
			Str("provider", c.Provider).Str("model", c.Model).Str("role", c.Role).
			Msg("llm_invocation buffer 满，丢弃一条审计记录")
		return nil
	}
}

// Flush 请求 worker 立即落库当前 batch 并等其确认；测试与 AggregateByTask/ListByTask 前用。
//
// 请求-确认语义：向 worker 发一个「立即 flush」信号 + 回传 chan，worker 处理到这条信号
// 时立即落库当前 batch 再关闭回传 chan，Flush 一收到就返回。不盲等 flushInterval：查询
// 前调用时 channel 通常为空，几乎零延迟（一次 channel round-trip）。
func (s *Store) Flush(ctx context.Context) error {
	// 先判 ctx：已取消时「发出请求」与「ctx.Done」两个 select 分支可能同时就绪，
	// Go 随机选择，会时而照常 flush 返回 nil，违背「取消即返回 ctx.Err()」的契约。
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan struct{})
	select {
	case s.flushReq <- done:
	case <-s.closed:
		return nil // worker 已停（Close 中），无需再等
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// recoverPanic 捕获批写循环 panic：审计是旁路，不应因单条脏数据拖垮进程。
// 循环体本身由 flush 驱动，panic 后仅记录日志并退出当前循环（Close 兜底）。
func (s *Store) recoverPanic() {
	if r := recover(); r != nil {
		s.log.Error().Interface("panic", r).Msg("llm_invocation 批写循环 panic")
	}
}

// Close 停止 worker 并 flush 剩余 buffer。幂等。
func (s *Store) Close() error {
	select {
	case <-s.closed:
		return nil
	default:
		close(s.closed)
	}
	s.wg.Wait()
	return nil
}

// run 是后台 worker：累积 batch 满 / 定时 flush；收到 closed 信号则 drain 后退出。
func (s *Store) run() {
	defer s.recoverPanic()
	defer s.wg.Done()
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()

	batch := make([]Invocation, 0, s.batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		// 用独立 ctx：caller 路径 ctx 取消不应阻断埋点。
		ctx, cancel := context.WithTimeout(context.Background(), s.insertTimeout)
		defer cancel()
		if err := s.copyFromBatch(ctx, batch); err != nil {
			s.log.Warn().Err(err).Int("rows", len(batch)).Msg("llm_call batch insert 失败")
		}
		batch = batch[:0]
	}

	// drainAndFlush 非阻塞收走 channel 里已入队的行（Append 是 fire-and-forget，
	// 此刻 channel 里的都是「已提交」的行）后落库一次；Flush()/closed 分支共用。
	drainAndFlush := func() {
		for {
			select {
			case c := <-s.ch:
				batch = append(batch, c)
			default:
				flush()
				return
			}
		}
	}

	for {
		select {
		case c := <-s.ch:
			batch = append(batch, c)
			if len(batch) >= s.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case done := <-s.flushReq:
			// Flush() 请求：立即落库当前 batch，再关 done 通知调用方——
			// 不再靠盲等 flushInterval（那曾让每次查询前白等 1.1s）。
			drainAndFlush()
			close(done)
		case <-s.closed:
			drainAndFlush()
			return
		}
	}
}

// copyFromBatch 用 pgx CopyFrom 批量写入；比逐行 INSERT 快 10-100x。
func (s *Store) copyFromBatch(ctx context.Context, batch []Invocation) error {
	rows := make([][]any, len(batch))
	for i, c := range batch {
		rows[i] = []any{
			c.AgentRunID, c.TaskID, c.ConversationID,
			c.Provider, c.Model,
			c.InTokens, c.OutTokens, c.CachedTokens,
			c.LatencyMs,
			c.FinishReason, c.Error, c.ErrorKind, c.Role,
			c.Messages, c.Result,
		}
	}
	_, err := s.pool.CopyFrom(
		ctx,
		pgx.Identifier{"llm_invocation"},
		[]string{
			"agent_run_id", "task_id", "conversation_id",
			"provider", "model",
			"in_tokens", "out_tokens", "cached_tokens",
			"latency_ms",
			"finish_reason", "error_message", "error_kind", "role",
			"messages", "result",
		},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("copyFrom llm_invocation: %w", err)
	}
	return nil
}

// maxListLimit 是 ListByTask 单页硬上限——防无界查询（无论调用方传多大 limit，都不超它）。
const maxListLimit = 1000

// defaultListLimit 对齐 httpapi defaultInvocationPageSize=200——≤0 兜底用缺省而非上限。
const defaultListLimit = 200

// ListFilter 是列表 / 统计共用的筛选条件（零值 = 不筛，等价于全量）。
//
// 关键：列表与统计吃同一份筛选，否则筛选后"明细 3 条、合计仍是全量"会互相打脸。
type ListFilter struct {
	Role    string     // 精确匹配调用者角色；空 = 不筛
	Model   string     // 精确匹配模型名；空 = 不筛
	OnlyErr bool       // true = 只看失败调用（error_message 非空）
	Start   *time.Time // created_at >= Start；nil = 不限
	End     *time.Time // created_at <= End；nil = 不限
	Search  string     // 全局搜索：匹配 task_id/conversation_id/assignment_id；空 = 不筛
	Limit   int        // 本页上限；<=0 或超 maxListLimit 收敛到 maxListLimit
	Offset  int        // 本页起点（offset 分页——对齐流量/漏洞模块的总数+跳页体验）
}

// where 把筛选拼成 SQL 条件与参数（task_id 恒为 $1，故从 $2 起编号）。
// 返回的 conds 已含 task_id 条件，调用方直接 strings.Join(conds, " AND ")。
// 列名加 li. 前缀以支持 JOIN 查询。
func (f ListFilter) where(taskID string) (conds []string, args []any) {
	args = []any{taskID}
	conds = []string{"li.task_id=$1::uuid"}
	add := func(cond string, val any) {
		args = append(args, val)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	if f.Role != "" {
		add("li.role = $%d", f.Role)
	}
	if f.Model != "" {
		add("li.model = $%d", f.Model)
	}
	if f.OnlyErr {
		conds = append(conds, "li.error_message <> ''")
	}
	if f.Start != nil {
		add("li.created_at >= $%d", *f.Start)
	}
	if f.End != nil {
		add("li.created_at <= $%d", *f.End)
	}
	return conds, args
}

// whereAll 把筛选拼成 SQL 条件（全局搜索，不限定 task_id）。
// Search 为空时返回所有数据，否则匹配 task_id/conversation_id/assignment_id 之一。
func (f ListFilter) whereAll() (conds []string, args []any) {
	args = []any{}
	conds = []string{}
	add := func(cond string, val any) {
		args = append(args, val)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	if f.Search != "" {
		// 搜索：匹配 task_id/conversation_id/assignment_id（对话与批次按 invocationJoins 的归属口径）
		add("(li.task_id::text = $%d OR "+invocationConversation+"::text = $%[1]d OR t.assignment_id::text = $%[1]d)", f.Search)
	}
	if f.Role != "" {
		add("li.role = $%d", f.Role)
	}
	if f.Model != "" {
		add("li.model = $%d", f.Model)
	}
	if f.OnlyErr {
		conds = append(conds, "li.error_message <> ''")
	}
	if f.Start != nil {
		add("li.created_at >= $%d", *f.Start)
	}
	if f.End != nil {
		add("li.created_at <= $%d", *f.End)
	}
	if len(conds) == 0 {
		conds = append(conds, "TRUE") // 无条件时返回所有数据
	}
	return conds, args
}

// limitOrDefault 收敛分页上限，防无界查询。
func (f ListFilter) limitOrDefault() int {
	if f.Limit <= 0 {
		return defaultListLimit
	}
	if f.Limit > maxListLimit {
		return maxListLimit
	}
	return f.Limit
}

// invocationJoins 是全部查询共用的归属联表：
//   - tc：任务的对话（智能体运行的调用经 task_id 找对话；一个任务至多一个对话）
//   - cc：调用自己记下的对话（对话里直接发起的调用：问答、意图分类、标题生成）
//   - t / a：归属任务与批次——调用自己的任务，没有时取所在对话的任务（来源据此定标签页）
const invocationJoins = `
	LEFT JOIN conversation tc ON tc.task_id = li.task_id
	LEFT JOIN conversation cc ON cc.id = li.conversation_id
	LEFT JOIN task t ON t.id = COALESCE(li.task_id, cc.task_id)
	LEFT JOIN assignment a ON a.id = t.assignment_id`

// invocationConversation 是调用所属对话的表达式：自己记下的对话优先，否则是任务的对话。
const invocationConversation = "COALESCE(cc.id, tc.id)"

// listSelect 是列表查询共享的列序与 JOIN（ListByTask / ListAll 单一口径）：业务键
// （conversation/assignment）经 invocationJoins 派生。新增列时同步改 scanListRow。
const listSelect = `
	SELECT li.id, li.agent_run_id, li.task_id::text,
	       li.provider, li.model,
	       li.in_tokens, li.out_tokens, li.cached_tokens,
	       li.latency_ms,
	       li.finish_reason, li.error_message, li.error_kind, li.role,
	       li.created_at,
	       ` + invocationConversation + `::text AS conversation_id,
	       t.assignment_id::text AS assignment_id
	FROM llm_invocation li` + invocationJoins

// scanListRow 是 listSelect 列序的统一反序列化点。
func scanListRow(rows pgx.Rows, v *Invocation) error {
	var tid, cid, aid *string
	if err := rows.Scan(
		&v.ID, &v.AgentRunID, &tid,
		&v.Provider, &v.Model,
		&v.InTokens, &v.OutTokens, &v.CachedTokens,
		&v.LatencyMs,
		&v.FinishReason, &v.Error, &v.ErrorKind, &v.Role,
		&v.CreatedAt,
		&cid, &aid,
	); err != nil {
		return err
	}
	v.TaskID = tid
	v.ConversationID = cid
	v.AssignmentID = aid
	return nil
}

// ListByTask 列出 task 下的 LLM invocation，按 id ASC + offset 翻页
// （对齐流量/漏洞模块的「共 X 条 + N/M 页」体验，配合 CountByTask 算总页数）。
//
// 筛选条件见 ListFilter（role/model/仅错误/时间范围 + limit/offset）；
// 与 AggregateByTask 吃同一份 filter，保证「明细」与「合计」口径一致。
//
// 用 id 而非 created_at 排序：批量 worker 同一 flush 内的行 created_at 几乎相同（写入时刻，
// 非调用时刻），排序不稳定；bigserial id 在同一批 CopyFrom 内严格单调，才是可靠的排序键。
// 调用方有责任先 Flush() 等异步 buffer commit，否则可能缺最近 0-1s 的记录。
//
// 列表不选 messages/result（大字段，详情另走 GetByID 按需拉，见 docs 中"列表/详情分离"设计），
// 审计表要能一眼看出一次调用属于哪个任务、用了什么模型、成败如何
// 「这次调用干了什么」（派活 / 跑命令 / 写漏洞），否则得逐行点开详情才知道。
// 工具清单在库内算完再出网，仍不传大字段。
func (s *Store) ListByTask(ctx context.Context, taskID string, f ListFilter) ([]Invocation, error) {
	conds, args := f.where(taskID)
	args = append(args, f.limitOrDefault(), f.Offset)
	q := fmt.Sprintf(listSelect+`
		WHERE %[1]s
		ORDER BY li.id DESC
		LIMIT $%[2]d OFFSET $%[3]d`,
		strings.Join(conds, " AND "), len(args)-1, len(args))
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list llm_invocation by task: %w", err)
	}
	defer rows.Close()

	var out []Invocation
	for rows.Next() {
		var v Invocation
		if err := scanListRow(rows, &v); err != nil {
			return nil, fmt.Errorf("scan llm_invocation: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate llm_invocation: %w", err)
	}
	return out, nil
}

// GetDetailByID 通过 ID 直接获取详情（不需要 task_id），用于全局搜索后点击详情。
// 包含 messages/result 大字段，以及 JOIN 获取的 conversation_id/assignment_id。
func (s *Store) GetDetailByID(ctx context.Context, id int64) (Invocation, error) {
	var v Invocation
	var tid, cid, aid *string
	err := s.pool.QueryRow(ctx, `
		SELECT li.id, li.agent_run_id, li.task_id::text,
		       li.provider, li.model,
		       li.in_tokens, li.out_tokens, li.cached_tokens,
		       li.latency_ms,
		       li.finish_reason, li.error_message, li.error_kind, li.role,
		       li.messages, li.result, li.created_at,
		       `+invocationConversation+`::text AS conversation_id,
		       t.assignment_id::text AS assignment_id,
		       COALESCE(a.source, '')
		FROM llm_invocation li`+invocationJoins+`
		WHERE li.id=$1`, id).Scan(
		&v.ID, &v.AgentRunID, &tid,
		&v.Provider, &v.Model,
		&v.InTokens, &v.OutTokens, &v.CachedTokens,
		&v.LatencyMs,
		&v.FinishReason, &v.Error, &v.ErrorKind, &v.Role,
		&v.Messages, &v.Result, &v.CreatedAt,
		&cid, &aid, &v.Source,
	)
	if err != nil {
		return Invocation{}, fmt.Errorf("get llm_invocation detail %d: %w", id, err)
	}
	v.TaskID = tid
	v.ConversationID = cid
	v.AssignmentID = aid
	return v, nil
}

// Aggregate 是某 task 下所有 LLM 调用的合计（会话用量总览）。
// 权威口径：框架层埋点（framework/llm.InstrumentProvider）对每次 Provider 调用（含纯
// tool_call、含失败）都落一行，
// 故此合计覆盖全部调用，不受"无文字推理不发 SSE 事件"影响。
type Aggregate struct {
	Calls        int   // 调用次数
	InTokens     int64 // 输入 token 合计
	OutTokens    int64 // 输出 token 合计
	CachedTokens int64 // 命中缓存的 token 合计
	LatencyMs    int64 // LLM 调用耗时合计（ms）
}

// AggregateByTask 合计某 task 的 llm_invocation 用量，按 filter 筛选（零值 filter = 全量）。
// 无匹配记录时返回零值。调用前应先 Flush() 确保异步 buffer 已落库。
//
// 与 ListByTask 共用 ListFilter：审计页筛选后，合计跟着筛选变（对齐 NewAPI GetLogsStat 的做法），
// 不会出现「明细只剩 3 条、合计仍是全量」的自相矛盾。Limit/Offset 不参与统计——
// 统计是整个筛选结果集的合计，与翻到第几页无关。
func (s *Store) AggregateByTask(ctx context.Context, taskID string, f ListFilter) (Aggregate, error) {
	conds, args := f.where(taskID)
	q := fmt.Sprintf(`
		SELECT COUNT(*),
		       COALESCE(SUM(in_tokens),0), COALESCE(SUM(out_tokens),0),
		       COALESCE(SUM(cached_tokens),0),
		       COALESCE(SUM(latency_ms),0)
		FROM llm_invocation li WHERE %s`, strings.Join(conds, " AND "))
	var a Aggregate
	err := s.pool.QueryRow(ctx, q, args...).
		Scan(&a.Calls, &a.InTokens, &a.OutTokens, &a.CachedTokens, &a.LatencyMs)
	if err != nil {
		return Aggregate{}, fmt.Errorf("aggregate llm_invocation by task %s: %w", taskID, err)
	}
	return a, nil
}

// CountByTask 返回该 task 下匹配 filter 的总行数（不含 limit/offset），供前端算总页数。
// 与 ListByTask/AggregateByTask 共用同一份筛选口径。
func (s *Store) CountByTask(ctx context.Context, taskID string, f ListFilter) (int, error) {
	conds, args := f.where(taskID)
	q := fmt.Sprintf(`SELECT COUNT(*) FROM llm_invocation li WHERE %s`, strings.Join(conds, " AND "))
	var total int
	if err := s.pool.QueryRow(ctx, q, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count llm_invocation by task %s: %w", taskID, err)
	}
	return total, nil
}

// ListAll 全局搜索 LLM 调用（不限定 task_id）。
// Search 字段匹配 task_id/conversation_id/assignment_id 之一。
func (s *Store) ListAll(ctx context.Context, f ListFilter) ([]Invocation, error) {
	conds, args := f.whereAll()
	// 追加 LIMIT 和 OFFSET 参数
	limitIdx := len(args) + 1
	offsetIdx := len(args) + 2
	args = append(args, f.limitOrDefault(), f.Offset)

	q := fmt.Sprintf(listSelect+`
		WHERE %[1]s
		ORDER BY li.id DESC
		LIMIT $%[2]d OFFSET $%[3]d`,
		strings.Join(conds, " AND "), limitIdx, offsetIdx)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list all llm_invocation: %w", err)
	}
	defer rows.Close()

	var out []Invocation
	for rows.Next() {
		var v Invocation
		if err := scanListRow(rows, &v); err != nil {
			return nil, fmt.Errorf("scan llm_invocation: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CountAll 全局搜索的总数统计。
func (s *Store) CountAll(ctx context.Context, f ListFilter) (int, error) {
	conds, args := f.whereAll()
	q := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM llm_invocation li`+invocationJoins+`
		WHERE %s`, strings.Join(conds, " AND "))
	var total int
	if err := s.pool.QueryRow(ctx, q, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count all llm_invocation: %w", err)
	}
	return total, nil
}

// AggregateAll 全局搜索口径的用量合计（calls/tokens/耗时），与 ListAll/CountAll
// 共用同一份筛选（含 JOIN——Search 条件引用 task/conversation 列）。
// 全局审计页头部统计 chip 的数据源：统计是整个筛选结果集的合计，与翻页无关。
func (s *Store) AggregateAll(ctx context.Context, f ListFilter) (Aggregate, error) {
	conds, args := f.whereAll()
	q := fmt.Sprintf(`
		SELECT COUNT(*),
		       COALESCE(SUM(in_tokens),0), COALESCE(SUM(out_tokens),0),
		       COALESCE(SUM(cached_tokens),0),
		       COALESCE(SUM(latency_ms),0)
		FROM llm_invocation li`+invocationJoins+`
		WHERE %s`, strings.Join(conds, " AND "))
	var a Aggregate
	err := s.pool.QueryRow(ctx, q, args...).
		Scan(&a.Calls, &a.InTokens, &a.OutTokens, &a.CachedTokens, &a.LatencyMs)
	if err != nil {
		return Aggregate{}, fmt.Errorf("aggregate all llm_invocation: %w", err)
	}
	return a, nil
}

// Facets 是角色 / 模型筛选的选项。
type Facets struct {
	Roles  []string
	Models []string
}

// Facets 取角色与模型的可选值：来自整个筛选结果集（不是当前页），每个维度不受自身筛选
// 影响——选中一个模型后其余模型仍可选；其它筛选（搜索、仅错误、时间）照常收窄。
func (s *Store) Facets(ctx context.Context, f ListFilter) (Facets, error) {
	roleFilter, modelFilter := f, f
	roleFilter.Role, modelFilter.Model = "", ""
	roles, err := s.distinct(ctx, "li.role", roleFilter)
	if err != nil {
		return Facets{}, err
	}
	models, err := s.distinct(ctx, "li.model", modelFilter)
	if err != nil {
		return Facets{}, err
	}
	return Facets{Roles: roles, Models: models}, nil
}

// distinct 取筛选结果集里某列的非空去重值（升序）。col 只取自本包常量，不来自输入。
func (s *Store) distinct(ctx context.Context, col string, f ListFilter) ([]string, error) {
	conds, args := f.whereAll()
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT DISTINCT %[1]s FROM llm_invocation li`+invocationJoins+`
		WHERE %[2]s AND %[1]s <> ''
		ORDER BY 1`, col, strings.Join(conds, " AND ")), args...)
	if err != nil {
		return nil, fmt.Errorf("distinct %s: %w", col, err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan %s: %w", col, err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// RetentionBatch 是留存清理的单批行数（DeleteOlderThan 每次至多删这么多）。
const RetentionBatch = 5000

// DeleteOlderThan 删除 created_at 早于 cutoff 的调用记录，单批至多 RetentionBatch 行，返回
// 删除行数。批删而非一次删完：记录含完整 prompt/response（单行 ~26KB），首次开启留存时
// 可能几十万行，一条 DELETE 会长时间持锁；调用方循环调到返回 0。删除前调用方应先
// Flush()，保证 buffer 内的新记录不被误删窗口漏算。
func (s *Store) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM llm_invocation
		WHERE id IN (SELECT id FROM llm_invocation WHERE created_at < $1 LIMIT $2)`,
		cutoff, RetentionBatch)
	if err != nil {
		return 0, fmt.Errorf("delete llm_invocation older than %s: %w", cutoff.Format(time.RFC3339), err)
	}
	return tag.RowsAffected(), nil
}
