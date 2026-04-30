package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// FlowStream 是 proxy → consumer 之间的 Redis Stream 名。
//
// 业界最佳实践：proxy 进程无状态，过滤后的快照一律 XADD 进入此流；
// 消费者（scanner ingestor）做切窗 / 落库 / Asynq 入队。
const FlowStream = "liusha:flow_events"

// streamMaxLen 是 XADD 自动裁剪老条目的上限（~ 近似），避免长期堆积撑满 Redis 内存。
// 1e5 条 × 平均 5 KiB ≈ 500 MiB；够 dev 环境用，prod 可调。
const streamMaxLen = 100_000

// Publisher 把 TrafficSnapshot 投递到 Redis Stream。
//
//	XADD liusha:flow_events MAXLEN ~ 100000 * snap <json>
//
// 调用方：proxy.Server.onResponse 在过滤通过后调 Publish。
type Publisher struct {
	rdb    *redis.Client
	stream string
	maxLen int64
}

// PublisherOption 函数式配置；不传 = 走默认（FlowStream / streamMaxLen）。
type PublisherOption func(*Publisher)

// WithStream 覆盖 stream 名（测试用）。
func WithStream(name string) PublisherOption { return func(p *Publisher) { p.stream = name } }

// WithMaxLen 覆盖 MAXLEN 阈值（测试用）。
func WithMaxLen(n int64) PublisherOption { return func(p *Publisher) { p.maxLen = n } }

// NewPublisher 构造 Publisher。rdb 必填。
func NewPublisher(rdb *redis.Client, opts ...PublisherOption) (*Publisher, error) {
	if rdb == nil {
		return nil, errors.New("proxy.NewPublisher: rdb 必填")
	}
	p := &Publisher{rdb: rdb, stream: FlowStream, maxLen: streamMaxLen}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

// Publish 发布一条 snapshot。错误由调用方决定（log warn / drop / 不影响代理转发）。
//
//	失败语义：snap 为 nil → 直接 nil；redis 不可达 → 透传 redis err，让上层 log。
func (p *Publisher) Publish(ctx context.Context, snap *TrafficSnapshot) error {
	if snap == nil {
		return nil
	}
	body, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	return p.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: p.stream,
		MaxLen: p.maxLen,
		Approx: true,
		Values: map[string]interface{}{"snap": body},
	}).Err()
}

// Stream 当前使用的 stream 名（测试用）。
func (p *Publisher) Stream() string { return p.stream }
