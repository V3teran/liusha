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
	fallbackFlushInterval = 1 * time.Second
	fallbackInsertTimeout = 5 * time.Second
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
			c.HunterID, c.TaskID,
			c.Provider, c.Model,
			c.InTokens, c.OutTokens, c.CachedTokens,
			c.LatencyMs, c.TTFTMs, c.IsStream,
			c.FinishReason, c.Error, c.Role,
			c.Messages, c.Result,
		}
	}
	_, err := s.pool.CopyFrom(
		ctx,
		pgx.Identifier{"llm_invocation"},
		[]string{
			"hunter_id", "task_id",
			"provider", "model",
			"in_tokens", "out_tokens", "cached_tokens",
			"latency_ms", "ttft_ms", "is_stream",
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

// textPreviewLimit 是列表摘要里文字预览的截断长度：够表格一行展示，又不至于把正文搬出库。
const textPreviewLimit = 200

// ListFilter 是列表 / 统计共用的筛选条件（零值 = 不筛，等价于全量）。
//
// 关键：列表与统计吃同一份筛选，否则筛选后"明细 3 条、合计仍是全量"会互相打脸。
type ListFilter struct {
	Role    string     // 精确匹配调用者角色；空 = 不筛
	Model   string     // 精确匹配模型名；空 = 不筛
	OnlyErr bool       // true = 只看失败调用（error_message 非空）
	Start   *time.Time // created_at >= Start；nil = 不限
	End     *time.Time // created_at <= End；nil = 不限
	AfterID int64      // keyset 游标：只取 id > AfterID；0 = 从头
	Limit   int        // 本页上限；<=0 或超 maxListLimit 收敛到 maxListLimit
}

// where 把筛选拼成 SQL 条件与参数（task_id 恒为 $1，故从 $2 起编号）。
// 返回的 conds 已含 task_id 条件，调用方直接 strings.Join(conds, " AND ")。
func (f ListFilter) where(taskID string, withCursor bool) (conds []string, args []any) {
	args = []any{taskID}
	conds = []string{"task_id=$1::uuid"}
	add := func(cond string, val any) {
		args = append(args, val)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	if withCursor && f.AfterID > 0 {
		add("id > $%d", f.AfterID)
	}
	if f.Role != "" {
		add("role = $%d", f.Role)
	}
	if f.Model != "" {
		add("model = $%d", f.Model)
	}
	if f.OnlyErr {
		conds = append(conds, "error_message <> ''")
	}
	if f.Start != nil {
		add("created_at >= $%d", *f.Start)
	}
	if f.End != nil {
		add("created_at <= $%d", *f.End)
	}
	return conds, args
}

// limitOrDefault 收敛分页上限，防无界查询。
func (f ListFilter) limitOrDefault() int {
	if f.Limit <= 0 || f.Limit > maxListLimit {
		return maxListLimit
	}
	return f.Limit
}

// ListByTask 列出 task 下的 LLM invocation，按 id ASC 游标翻页（keyset pagination，非 offset）。
//
// 筛选条件见 ListFilter（role/model/仅错误/时间范围 + 游标 + 上限）；
// 与 AggregateByTask 吃同一份 filter，保证「明细」与「合计」口径一致。
//
// 用 id 而非 created_at 排序：批量 worker 同一 flush 内的行 created_at 几乎相同（写入时刻，
// 非调用时刻），排序不稳定；bigserial id 在同一批 CopyFrom 内严格单调，才是可靠的时序游标。
// 调用方有责任先 Flush() 等异步 buffer commit，否则可能缺最近 0-1s 的记录。
//
// 列表不选 messages/result（大字段，详情另走 GetByID 按需拉，见 docs 中"列表/详情分离"设计），
// 但在 SQL 里从 result 派生出 tool_names / text_preview 两个短摘要——审计表要能一眼看出
// 「这次调用干了什么」（派活 / 跑命令 / 写漏洞），否则得逐行点开详情才知道。
// 摘要在库内算完再出网，仍不传大字段。
func (s *Store) ListByTask(ctx context.Context, taskID string, f ListFilter) ([]Invocation, error) {
	conds, args := f.where(taskID, true)
	args = append(args, f.limitOrDefault())
	q := fmt.Sprintf(`
		SELECT id, request_id, hunter_id, task_id::text,
		       provider, model,
		       in_tokens, out_tokens, cached_tokens,
		       latency_ms, ttft_ms, is_stream,
		       finish_reason, error_message, role,
		       created_at,
		       COALESCE((SELECT array_agg(tc->'function'->>'name' ORDER BY ord)
		                 FROM jsonb_array_elements(result->'tool_calls') WITH ORDINALITY AS t(tc, ord)
		                 WHERE jsonb_typeof(result->'tool_calls') = 'array'), '{}') AS tool_names,
		       left(COALESCE(result->>'content', ''), %d) AS text_preview
		FROM llm_invocation
		WHERE %s
		ORDER BY id ASC
		LIMIT $%d`, textPreviewLimit, strings.Join(conds, " AND "), len(args))
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list llm_invocation by task: %w", err)
	}
	defer rows.Close()

	var out []Invocation
	for rows.Next() {
		var v Invocation
		var hunterID, tid *string
		if err := rows.Scan(
			&v.ID, &v.RequestID, &hunterID, &tid,
			&v.Provider, &v.Model,
			&v.InTokens, &v.OutTokens, &v.CachedTokens,
			&v.LatencyMs, &v.TTFTMs, &v.IsStream,
			&v.FinishReason, &v.Error, &v.Role,
			&v.CreatedAt,
			&v.ToolNames, &v.TextPreview,
		); err != nil {
			return nil, fmt.Errorf("scan llm_invocation: %w", err)
		}
		v.HunterID = hunterID
		v.TaskID = tid
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate llm_invocation: %w", err)
	}
	return out, nil
}

// GetByID 取单条 invocation 的完整行（含 messages/result 大字段），供列表页点击钻取详情用。
// 用 taskID+id 联合定位（而非裸 id）：防止跨 task 猜 id 越权读取审计原文。
func (s *Store) GetByID(ctx context.Context, taskID string, id int64) (Invocation, error) {
	var v Invocation
	var hunterID, tid *string
	err := s.pool.QueryRow(ctx, `
		SELECT id, request_id, hunter_id, task_id::text,
		       provider, model,
		       in_tokens, out_tokens, cached_tokens,
		       latency_ms, ttft_ms, is_stream,
		       finish_reason, error_message, role,
		       messages, result, created_at
		FROM llm_invocation
		WHERE id=$1 AND task_id=$2::uuid`, id, taskID).Scan(
		&v.ID, &v.RequestID, &hunterID, &tid,
		&v.Provider, &v.Model,
		&v.InTokens, &v.OutTokens, &v.CachedTokens,
		&v.LatencyMs, &v.TTFTMs, &v.IsStream,
		&v.FinishReason, &v.Error, &v.Role,
		&v.Messages, &v.Result, &v.CreatedAt,
	)
	if err != nil {
		return Invocation{}, fmt.Errorf("get llm_invocation %d: %w", id, err)
	}
	v.HunterID = hunterID
	v.TaskID = tid
	return v, nil
}

// Aggregate 是某 owner 下所有 LLM 调用的合计（会话用量总览）。
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
// 不会出现「明细只剩 3 条、合计仍是全量」的自相矛盾。游标（AfterID）不参与统计——
// 统计是整个筛选结果集的合计，与翻到第几页无关。
func (s *Store) AggregateByTask(ctx context.Context, taskID string, f ListFilter) (Aggregate, error) {
	conds, args := f.where(taskID, false) // withCursor=false：统计不受翻页游标影响
	q := fmt.Sprintf(`
		SELECT COUNT(*),
		       COALESCE(SUM(in_tokens),0), COALESCE(SUM(out_tokens),0),
		       COALESCE(SUM(cached_tokens),0),
		       COALESCE(SUM(latency_ms),0)
		FROM llm_invocation WHERE %s`, strings.Join(conds, " AND "))
	var a Aggregate
	err := s.pool.QueryRow(ctx, q, args...).
		Scan(&a.Calls, &a.InTokens, &a.OutTokens, &a.CachedTokens, &a.LatencyMs)
	if err != nil {
		return Aggregate{}, fmt.Errorf("aggregate llm_invocation by task %s: %w", taskID, err)
	}
	return a, nil
}

// Facets 是筛选下拉的候选值（该 task 下实际出现过的 role / model 去重集合）。
type Facets struct {
	Roles  []string
	Models []string
}

// FacetsByTask 返回该 task 下 role / model 的 distinct 值，供前端筛选下拉。
//
// 必须服务端算：分页下前端只见当前页，从已加载行推候选会漏掉后续页里的 role/model。
// 不吃 ListFilter——候选集合应始终是该 task 的全集，否则筛了 role 之后 role 下拉就只剩自己，
// 用户无法切换到别的值（自锁死）。
func (s *Store) FacetsByTask(ctx context.Context, taskID string) (Facets, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT 'role' AS kind, role AS val FROM llm_invocation
		WHERE task_id=$1::uuid AND role <> ''
		UNION
		SELECT DISTINCT 'model' AS kind, model AS val FROM llm_invocation
		WHERE task_id=$1::uuid AND model <> ''
		ORDER BY kind, val`, taskID)
	if err != nil {
		return Facets{}, fmt.Errorf("facets llm_invocation by task %s: %w", taskID, err)
	}
	defer rows.Close()

	var f Facets
	for rows.Next() {
		var kind, val string
		if err := rows.Scan(&kind, &val); err != nil {
			return Facets{}, fmt.Errorf("scan facets: %w", err)
		}
		if kind == "role" {
			f.Roles = append(f.Roles, val)
		} else {
			f.Models = append(f.Models, val)
		}
	}
	if err := rows.Err(); err != nil {
		return Facets{}, fmt.Errorf("iterate facets: %w", err)
	}
	return f, nil
}
