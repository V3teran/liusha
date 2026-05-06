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

// Publisher 把 TrafficSnapshot 以 XADD 投递到 FlowStream。
// 调用方：proxy.Server.onResponse 在过滤通过后调 Publish。
type Publisher struct {
	rdb *redis.Client
}

// NewPublisher 构造 Publisher。rdb 必填。
func NewPublisher(rdb *redis.Client) (*Publisher, error) {
	if rdb == nil {
		return nil, errors.New("proxy.NewPublisher: rdb 必填")
	}
	return &Publisher{rdb: rdb}, nil
}

// Publish 发布一条 snapshot；snap 为 nil 直接返回。redis 错误透传给上层处理。
func (p *Publisher) Publish(ctx context.Context, snap *TrafficSnapshot) error {
	if snap == nil {
		return nil
	}
	body, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	return p.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: FlowStream,
		MaxLen: streamMaxLen,
		Approx: true,
		Values: map[string]interface{}{"snap": body},
	}).Err()
}
