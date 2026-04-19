package proxy

import (
	"context"
	"time"

	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/scope"
)

// FlowPusher 是 Redis 热层的最小接口。
type FlowPusher interface {
	Push(ctx context.Context, f flow.Flow) error
}

// FlowBatcher 是 Postgres 冷层的最小接口。
type FlowBatcher interface {
	BatchInsert(ctx context.Context, flows []flow.Flow) error
}

// IngestConfig 控制批量刷写行为。
type IngestConfig struct {
	BatchSize     int           // 达到此条数立即 flush
	FlushInterval time.Duration // 达到此时间间隔强制 flush
}

// Ingestor 读取 ParsedFlow channel，经 ScopePolicy 过滤后推 Redis 热层并攒批写 Postgres。
type Ingestor struct {
	policy *scope.Policy
	rdb    FlowPusher
	pg     FlowBatcher
	cfg    IngestConfig
}

// NewIngestor 构造 Ingestor。
func NewIngestor(policy *scope.Policy, rdb FlowPusher, pg FlowBatcher, cfg IngestConfig) *Ingestor {
	return &Ingestor{policy: policy, rdb: rdb, pg: pg, cfg: cfg}
}

// Run 阻塞消费 ch 直到 ctx 取消或 ch 关闭。
func (ing *Ingestor) Run(ctx context.Context, ch <-chan ParsedFlow) {
	bootCtx := logx.WithTraceID(ctx, logx.NewTraceID())
	ticker := time.NewTicker(ing.cfg.FlushInterval)
	defer ticker.Stop()

	var buf []flow.Flow

	flush := func() {
		if len(buf) == 0 {
			return
		}
		if err := ing.pg.BatchInsert(ctx, buf); err != nil {
			logx.From(bootCtx).Error().Err(err).Int("count", len(buf)).Msg("flow batch insert 失败")
		}
		buf = buf[:0]
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return

		case <-ticker.C:
			flush()

		case pf, ok := <-ch:
			if !ok {
				flush()
				return
			}
			if !ing.policy.InScope(pf.Method, pf.URL) {
				continue
			}
			f := flow.New(flow.RawInput{
				EngagementID: pf.EngagementID,
				Timestamp:    pf.Timestamp,
				Method:       pf.Method,
				URL:          pf.URL,
				ReqHeaders:   pf.ReqHeaders,
				ReqBody:      pf.ReqBody,
				RespStatus:   pf.RespStatus,
				RespHeaders:  pf.RespHeaders,
				RespBody:     pf.RespBody,
			})
			if err := ing.rdb.Push(ctx, f); err != nil {
				logx.From(bootCtx).Error().Err(err).Str("url", f.URL).Msg("redis push 失败")
			}
			buf = append(buf, f)
			if len(buf) >= ing.cfg.BatchSize {
				flush()
			}
		}
	}
}
