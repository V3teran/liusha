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

	"github.com/V3teran/liusha/internal/config"
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
//   - Append 返回的 id 现为 0（异步路径不再有 RETURNING id）；调用方 instrument.go
//     已忽略 id（仅记日志），故签名保留兼容。
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

// fallback 参数（caller 用 NewStore(pool) 不带 config 路径时使用）。
const (
	fallbackBufferSize    = 1024
	fallbackBatchSize     = 100
	fallbackFlushInterval = constants.LogFlushInterval
	fallbackInsertTimeout = constants.DBQueryTimeout
)

// NewStoreWithConfig 用 yaml 配置构造 Store 并启动后台 batch worker；任一字段为 0 时回退到 fallback 常量。
// 进程退出前必须调用 Close() 排空 buffer，否则丢失最近写入。
func NewStoreWithConfig(pool *pgxpool.Pool, c config.InvocationConfig) *Store {
	bufSize := c.BufferSize
	if bufSize <= 0 {
		bufSize = fallbackBufferSize
	}
	batchSize := c.BatchSize
	if batchSize <= 0 {
		batchSize = fallbackBatchSize
	}
	flush := time.Duration(c.FlushIntervalMs) * time.Millisecond
	if flush <= 0 {
		flush = fallbackFlushInterval
	}
	insertTO := time.Duration(c.InsertTimeoutSec) * time.Second
	if insertTO <= 0 {
		insertTO = fallbackInsertTimeout
	}
	s := &Store{
		pool:          pool,
		ch:            make(chan Invocation, bufSize),
		flushReq:      make(chan chan struct{}),
		closed:        make(chan struct{}),
		log:           logx.New("llmcall.store"),
		batchSize:     batchSize,
		flushInterval: flush,
		insertTimeout: insertTO,
	}
	s.wg.Add(1)
	go s.run()
	return s
}

// Append 把 Invocation 推到内部 channel；channel 满则丢一行并 warn（不阻塞 Generate）。
// 返回 id 永远为 0（异步路径无 RETURNING id），与同步版本保持签名兼容。
func (s *Store) Append(ctx context.Context, c Invocation) (int64, error) {
	if len(c.Messages) == 0 {
		c.Messages = []byte("[]")
	}
	if len(c.Result) == 0 {
		c.Result = []byte("{}")
	}
	select {
	case s.ch <- c:
		return 0, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
		// channel 满（>1024 条）→ 丢这条，说明 LLM 调用速率超 worker batch 写盘吞吐。
		s.log.Warn().
			Str("provider", c.Provider).Str("model", c.Model).Str("role", c.Role).
			Msg("llm_invocation buffer 满，丢弃一条审计记录")
		return 0, nil
	}
}

// Flush 请求 worker 立即落库当前 batch 并等其确认；测试与 AggregateByTask/ListByTask 前用。
//
// 曾经的实现是「channel 空了就 sleep(flushInterval+100ms)」——盲等固定时长，
// 不管有没有数据要落库都要付这个代价：GET 请求场景 channel 几乎总是空的，
// 结果每次查询前都白等 1.1s（flushInterval 默认 1000ms）。
// 现在改成请求-确认：向 worker 发一个「立即 flush」信号 + 回传 chan，
// worker 处理到这条信号时立即落库当前 batch 再关闭回传 chan，Flush 一收到就返回，
// 没有待落库数据时几乎零延迟（一次 channel round-trip）。
func (s *Store) Flush(ctx context.Context) error {
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
			c.AgentRunID, c.TaskID,
			c.Provider, c.Model,
			c.InTokens, c.OutTokens, c.CachedTokens,
			c.LatencyMs,
			c.FinishReason, c.Error, c.Role,
			c.Messages, c.Result,
		}
	}
	_, err := s.pool.CopyFrom(
		ctx,
		pgx.Identifier{"llm_invocation"},
		[]string{
			"agent_run_id", "task_id",
			"provider", "model",
			"in_tokens", "out_tokens", "cached_tokens",
			"latency_ms",
			"finish_reason", "error_message", "role",
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

// textPreviewLimit 是列表摘要里文字预览的截断长度：够表格一行展示，又不至于把正文搬出库。
const textPreviewLimit = 200

// resultToolCallsExpr / resultContentExpr / toolNameExpr：result jsonb 的键读取表达式。
// 历史行是 Go 结构体无 tag 直出的大写键（Content/ToolCalls），加 tag 后的新行是小写键——
// 派生摘要两套都认（COALESCE 先小写后大写），新旧数据在列表页同等可读。
// 工具名同有两种形态：OpenAI 原始线格式包在 function.name 下，内部 ToolCall 是扁平 name。
const (
	resultToolCallsExpr = `COALESCE(li.result->'tool_calls', li.result->'ToolCalls')`
	resultContentExpr   = `COALESCE(li.result->>'content', li.result->>'Content', '')`
	toolNameExpr        = `COALESCE(tc->'function'->>'name', tc->>'name')`
)

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
		// 搜索：匹配 task_id/conversation_id/assignment_id
		add("(li.task_id::text = $%d OR c.id::text = $%[1]d OR t.assignment_id::text = $%[1]d)", f.Search)
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

// listSelect / listJoin 是列表查询共享的列序与 JOIN（ListByTask / ListAll
// 单一口径）：派生摘要（tool_names/text_preview）在库内算完再出网，业务键
// （conversation/assignment）经 JOIN 派生。新增列时两处同步改 scanListRow。
const listSelect = `
	SELECT li.id, li.request_id, li.agent_run_id, li.task_id::text,
	       li.provider, li.model,
	       li.in_tokens, li.out_tokens, li.cached_tokens,
	       li.latency_ms,
	       li.finish_reason, li.error_message, li.role,
	       li.created_at,
	       COALESCE((SELECT array_agg(COALESCE(%[1]s, '') ORDER BY ord)
	                 FROM jsonb_array_elements(%[2]s) WITH ORDINALITY AS t(tc, ord)
	                 WHERE jsonb_typeof(%[2]s) = 'array'), '{}') AS tool_names,
	       left(%[3]s, %[4]d) AS text_preview,
	       c.id::text AS conversation_id,
	       t.assignment_id::text AS assignment_id
	FROM llm_invocation li
	LEFT JOIN task t ON t.id = li.task_id
	LEFT JOIN conversation c ON c.task_id = li.task_id`

// scanListRow 是 listSelect 列序的统一反序列化点。
func scanListRow(rows pgx.Rows, v *Invocation) error {
	var tid, cid, aid *string
	if err := rows.Scan(
		&v.ID, &v.RequestID, &v.AgentRunID, &tid,
		&v.Provider, &v.Model,
		&v.InTokens, &v.OutTokens, &v.CachedTokens,
		&v.LatencyMs,
		&v.FinishReason, &v.Error, &v.Role,
		&v.CreatedAt,
		&v.ToolNames, &v.TextPreview,
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
// 但在 SQL 里从 result 派生出 tool_names / text_preview 两个短摘要——审计表要能一眼看出
// 「这次调用干了什么」（派活 / 跑命令 / 写漏洞），否则得逐行点开详情才知道。
// 摘要在库内算完再出网，仍不传大字段。
func (s *Store) ListByTask(ctx context.Context, taskID string, f ListFilter) ([]Invocation, error) {
	conds, args := f.where(taskID)
	args = append(args, f.limitOrDefault(), f.Offset)
	q := fmt.Sprintf(listSelect+`
		WHERE %[5]s
		ORDER BY li.id ASC
		LIMIT $%[6]d OFFSET $%[7]d`,
		toolNameExpr, resultToolCallsExpr, resultContentExpr, textPreviewLimit,
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
		SELECT li.id, li.request_id, li.agent_run_id, li.task_id::text,
		       li.provider, li.model,
		       li.in_tokens, li.out_tokens, li.cached_tokens,
		       li.latency_ms,
		       li.finish_reason, li.error_message, li.role,
		       li.messages, li.result, li.created_at,
		       c.id::text AS conversation_id,
		       t.assignment_id::text AS assignment_id,
		       COALESCE(a.source, '')
		FROM llm_invocation li
		LEFT JOIN task t ON t.id = li.task_id
		LEFT JOIN assignment a ON a.id = t.assignment_id
		LEFT JOIN conversation c ON c.task_id = li.task_id
		WHERE li.id=$1`, id).Scan(
		&v.ID, &v.RequestID, &v.AgentRunID, &tid,
		&v.Provider, &v.Model,
		&v.InTokens, &v.OutTokens, &v.CachedTokens,
		&v.LatencyMs,
		&v.FinishReason, &v.Error, &v.Role,
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
// 权威口径：UsageRecorder 对每次 ChatModel 调用（含纯 tool_call、含失败）都落一行，
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
		WHERE %[5]s
		ORDER BY li.id DESC
		LIMIT $%[6]d OFFSET $%[7]d`,
		toolNameExpr, resultToolCallsExpr, resultContentExpr, textPreviewLimit,
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
		FROM llm_invocation li
		LEFT JOIN task t ON t.id = li.task_id
		LEFT JOIN conversation c ON c.task_id = li.task_id
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
		FROM llm_invocation li
		LEFT JOIN task t ON t.id = li.task_id
		LEFT JOIN conversation c ON c.task_id = li.task_id
		WHERE %s`, strings.Join(conds, " AND "))
	var a Aggregate
	err := s.pool.QueryRow(ctx, q, args...).
		Scan(&a.Calls, &a.InTokens, &a.OutTokens, &a.CachedTokens, &a.LatencyMs)
	if err != nil {
		return Aggregate{}, fmt.Errorf("aggregate all llm_invocation: %w", err)
	}
	return a, nil
}

// DeleteOlderThan 删除 created_at 早于 cutoff 的调用记录，返回删除行数。
// 记录含完整 prompt/response 敏感载荷，留存策略由调用方周期执行（如每日）。
// 删除前调用方应先 Flush()，保证 buffer 内的新记录不被误删窗口漏算。
func (s *Store) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM llm_invocation WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete llm_invocation older than %s: %w", cutoff.Format(time.RFC3339), err)
	}
	return tag.RowsAffected(), nil
}
