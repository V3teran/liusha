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
// Stream 名 / MaxLen 由 config.ProxyConfig 注入（cmd/proxy 装配时传入），
// 不再硬编码——多项目共享 redis 时通过 yaml 改名即可隔离。
type Publisher struct {
	rdb    *redis.Client
	stream string
	maxLen int64
}

// NewPublisher 构造 Publisher。
//
//	rdb:    必填
//	stream: 空时报错（必须由 caller 从 config.ProxyConfig.StreamName 显式传入）
//	maxLen: <=0 时不裁剪（XADD 不带 MAXLEN）；>0 走近似裁剪（Approx=true）
func NewPublisher(rdb *redis.Client, stream string, maxLen int64) (*Publisher, error) {
	if rdb == nil {
		return nil, errors.New("proxy.NewPublisher: rdb 必填")
	}
	if stream == "" {
		return nil, errors.New("proxy.NewPublisher: stream 必填")
	}
	return &Publisher{rdb: rdb, stream: stream, maxLen: maxLen}, nil
}

// Stream 返回 publisher 当前使用的 stream 名（供 ingestor 等外部一致引用）。
func (p *Publisher) Stream() string { return p.stream }

// Publish 发布一条 snapshot；snap 为 nil 直接返回。redis 错误透传给上层处理。
func (p *Publisher) Publish(ctx context.Context, snap *TrafficSnapshot) error {
	if snap == nil {
		return nil
	}
	body, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	args := &redis.XAddArgs{
		Stream: p.stream,
		Values: map[string]interface{}{"snap": body},
	}
	if p.maxLen > 0 {
		args.MaxLen = p.maxLen
		args.Approx = true
	}
	return p.rdb.XAdd(ctx, args).Err()
}
