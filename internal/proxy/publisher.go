package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Publisher 把 TrafficSnapshot 以 XADD 投递到指定 Redis Stream。
//
// 调用方：proxy.Server.onResponse 在过滤通过后调 Publish。
//
// Stream 名 / MaxLen 由 config.ProxyConfig 注入（proxy 组件装配时传入），
// 不再硬编码——多项目共享 redis 时通过 yaml 改名即可隔离。
type Publisher struct {
	rdb    *redis.Client
	stream string
	maxLen int64
}

// TrafficStream 是代理流量快照的 Redis Stream：proxy 发布、runner 的流量摄入器消费，
// 两端共用的内部约定。
const TrafficStream = "liusha:traffic_snapshots"

// NewPublisher 构造 Publisher。maxLen 是 Stream 的近似裁剪上限（proxy.stream_max_len，> 0）。
func NewPublisher(rdb *redis.Client, maxLen int64) (*Publisher, error) {
	if rdb == nil {
		return nil, errors.New("proxy.NewPublisher: rdb 必填")
	}
	if maxLen <= 0 {
		return nil, errors.New("proxy.NewPublisher: maxLen 须大于 0")
	}
	return &Publisher{rdb: rdb, stream: TrafficStream, maxLen: maxLen}, nil
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
		Stream: p.stream,
		Values: map[string]interface{}{"snap": body},
		MaxLen: p.maxLen,
		Approx: true, // 近似裁剪：按宏节点整块删除，代价远低于精确裁剪
	}).Err()
}
