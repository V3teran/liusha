package llmcall

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/logx"
)

// Store 封装 llm_call 表的所有持久化操作。
//
// 写入采用 channel + 后台 worker 批量 INSERT 模式（v1.1 性能改造）：
//   - Append 把 Call 推到 buffered channel 立即返回，Generate 路径 0 阻塞
//   - 后台 worker 累积 batchSize=100 行或 flushInterval=1s 触发一次批量 INSERT
//   - Close() 优雅关闭：停 worker + 清空剩余 buffer + 最后一次 flush
//
// 设计取舍：
//   - Append 返回的 id 现为 0（异步路径不再有 RETURNING id）；调用方 instrument.go
//     已忽略 id（仅记日志），故签名保留兼容。
//   - 进程崩溃可能丢 buffer 内未 flush 的行（最差 100 行 / 1s）；这是行为日志非交易，可接受。
//   - 一致性场景（如 SumCostByEngagement / CountByRole）调用方需先调 Flush() 同步等待。
type Store struct {
	pool   *pgxpool.Pool
	ch     chan Call
	closed chan struct{}
	wg     sync.WaitGroup
	log    zerolog.Logger
}

// 默认参数（本轮固定值；未来可加 NewStoreWithOptions 暴露）。
const (
	defaultBufferSize    = 1024
	defaultBatchSize     = 100
	defaultFlushInterval = 1 * time.Second
)

// NewStore 用 pgxpool 构造 Store 并启动后台 batch worker。
// 进程退出前必须调用 Close() 排空 buffer，否则丢失最近写入。
func NewStore(pool *pgxpool.Pool) *Store {
	s := &Store{
		pool:   pool,
		ch:     make(chan Call, defaultBufferSize),
		closed: make(chan struct{}),
		log:    logx.New("llmcall.store"),
	}
	s.wg.Add(1)
	go s.run()
	return s
}

// Append 把 Call 推到内部 channel；channel 满则丢一行并 warn（不阻塞 Generate）。
// 返回 id 永远为 0（异步路径无 RETURNING id），与同步版本保持签名兼容。
func (s *Store) Append(ctx context.Context, c Call) (int64, error) {
	if len(c.MessagesJSON) == 0 {
		c.MessagesJSON = []byte("[]")
	}
	if len(c.ResultJSON) == 0 {
		c.ResultJSON = []byte("{}")
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
			Msg("llm_call buffer 满，丢弃一条审计记录")
		return 0, nil
	}
}

// Flush 阻塞直到 channel 排空 + 当前 batch 已 commit；测试与 SumCost 前用。
func (s *Store) Flush(ctx context.Context) error {
	for {
		if len(s.ch) == 0 {
			// channel 空了；再等 flushInterval 让 worker 把最后一批 commit
			select {
			case <-time.After(defaultFlushInterval + 100*time.Millisecond):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
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
	ticker := time.NewTicker(defaultFlushInterval)
	defer ticker.Stop()

	batch := make([]Call, 0, defaultBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		// 用独立 ctx：caller 路径 ctx 取消不应阻断埋点。
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.copyFromBatch(ctx, batch); err != nil {
			s.log.Warn().Err(err).Int("rows", len(batch)).Msg("llm_call batch insert 失败")
		}
		batch = batch[:0]
	}

	for {
		select {
		case c := <-s.ch:
			batch = append(batch, c)
			if len(batch) >= defaultBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-s.closed:
			// drain 剩余 channel，最后 flush 一次
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
	}
}

// copyFromBatch 用 pgx CopyFrom 批量写入；比逐行 INSERT 快 10-100x。
func (s *Store) copyFromBatch(ctx context.Context, batch []Call) error {
	rows := make([][]any, len(batch))
	for i, c := range batch {
		rows[i] = []any{
			c.TaskID, c.EngagementID, c.Provider, c.Model,
			c.InTokens, c.OutTokens, c.CachedTokens,
			c.CostUSD, c.LatencyMs, c.FinishReason, c.Error, c.Role,
			c.MessagesJSON, c.ResultJSON,
		}
	}
	_, err := s.pool.CopyFrom(
		ctx,
		pgx.Identifier{"llm_call"},
		[]string{
			"task_id", "engagement_id", "provider", "model",
			"in_tokens", "out_tokens", "cached_tokens",
			"cost_usd", "latency_ms", "finish_reason", "error", "role",
			"messages_json", "result_json",
		},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("copyFrom llm_call: %w", err)
	}
	return nil
}

// SumCostByEngagement 返回指定 engagement 下所有 LLM 调用的成本总和（美元）。
//
// 注意：异步 buffer 内未 flush 的成本不算入 —— caller 若要严格一致需先 Flush()。
func (s *Store) SumCostByEngagement(ctx context.Context, engagementID string) (float64, error) {
	var v float64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(cost_usd), 0)::float8
		FROM llm_call
		WHERE engagement_id=$1`, engagementID).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("sum llm_call cost: %w", err)
	}
	return v, nil
}

// CountByRole 按 role 维度聚合 engagement 下的调用次数，便于验证多模型路由生效。
func (s *Store) CountByRole(ctx context.Context, engagementID string) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT role, count(*)
		FROM llm_call
		WHERE engagement_id=$1
		GROUP BY role`, engagementID)
	if err != nil {
		return nil, fmt.Errorf("count llm_call by role: %w", err)
	}
	defer rows.Close()

	out := make(map[string]int)
	for rows.Next() {
		var role string
		var n int
		if err := rows.Scan(&role, &n); err != nil {
			return nil, fmt.Errorf("scan role count: %w", err)
		}
		out[role] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate role counts: %w", err)
	}
	return out, nil
}
