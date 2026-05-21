package llminvocation

import (
	"context"
	"fmt"
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
//   - 一致性场景（如 SumCostByOwner / CountByRole）调用方需先调 Flush() 同步等待。
type Store struct {
	pool          *pgxpool.Pool
	ch            chan Invocation
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

// Flush 阻塞直到 channel 排空 + 当前 batch 已 commit；测试与 SumCost 前用。
func (s *Store) Flush(ctx context.Context) error {
	for {
		if len(s.ch) == 0 {
			// channel 空了；再等 flushInterval 让 worker 把最后一批 commit
			select {
			case <-time.After(s.flushInterval + 100*time.Millisecond):
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

	for {
		select {
		case c := <-s.ch:
			batch = append(batch, c)
			if len(batch) >= s.batchSize {
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
func (s *Store) copyFromBatch(ctx context.Context, batch []Invocation) error {
	rows := make([][]any, len(batch))
	for i, c := range batch {
		rows[i] = []any{
			c.TaskID, c.OwnerType, c.OwnerID,
			c.Provider, c.Model,
			c.InTokens, c.OutTokens, c.CachedTokens,
			c.CostUSD, c.LatencyMs, c.FinishReason, c.Error, c.Role,
			c.Messages, c.Result,
		}
	}
	_, err := s.pool.CopyFrom(
		ctx,
		pgx.Identifier{"llm_invocation"},
		[]string{
			"agent_run_id", "owner_type", "owner_id",
			"provider", "model",
			"in_tokens", "out_tokens", "cached_tokens",
			"cost_usd", "latency_ms", "finish_reason", "error_message", "role",
			"messages", "result",
		},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("copyFrom llm_invocation: %w", err)
	}
	return nil
}

// ListByOwner 列出 owner 下所有 LLM invocation（按 created_at ASC）。
// ownerType 为 "" 时退化为仅按 owner_id 过滤（caller 仅持有 ID 时用，如 HTTP URL :owner_id）。
// 调用方有责任先 Flush() 等异步 buffer commit，否则可能缺最近 0-1s 的记录。
func (s *Store) ListByOwner(ctx context.Context, ownerType, ownerID string) ([]Invocation, error) {
	q := `SELECT id, agent_run_id, owner_type, owner_id::text,
	             provider, model,
	             in_tokens, out_tokens, cached_tokens,
	             cost_usd, latency_ms, finish_reason, error_message, role,
	             messages, result, created_at
	      FROM llm_invocation
	      WHERE owner_id=$1::uuid`
	args := []any{ownerID}
	if ownerType != "" {
		q += ` AND owner_type=$2`
		args = append(args, ownerType)
	}
	q += ` ORDER BY created_at ASC`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list llm_invocation by owner: %w", err)
	}
	defer rows.Close()

	var out []Invocation
	for rows.Next() {
		var v Invocation
		var taskID, ot, oid *string
		if err := rows.Scan(
			&v.ID, &taskID, &ot, &oid,
			&v.Provider, &v.Model,
			&v.InTokens, &v.OutTokens, &v.CachedTokens,
			&v.CostUSD, &v.LatencyMs, &v.FinishReason, &v.Error, &v.Role,
			&v.Messages, &v.Result, &v.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan llm_invocation: %w", err)
		}
		v.TaskID = taskID
		v.OwnerType = ot
		v.OwnerID = oid
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate llm_invocation: %w", err)
	}
	return out, nil
}

