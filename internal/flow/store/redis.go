// Package store 实现 http_flow 的双层持久化：Redis 热层（1h TTL）和 Postgres 冷层。
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/flow"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	streamKeyFmt = "liusha:flow:hot:%s"
	streamMaxLen = 1000
	streamTTL    = time.Hour
)

// Redis 封装 go-redis 客户端，提供 flow 热层读写。
type Redis struct {
	rdb *redis.Client
}

// NewRedis 构造 Redis 存储；客户端生命周期由调用方管理。
func NewRedis(rdb *redis.Client) *Redis { return &Redis{rdb: rdb} }

// Push 将单条 flow 序列化后 XADD 到对应 engagement 的 Stream，并刷新 TTL。
func (s *Redis) Push(ctx context.Context, f flow.Flow) error {
	key := fmt.Sprintf(streamKeyFmt, f.EngagementID)
	data, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("marshal flow: %w", err)
	}
	pipe := s.rdb.Pipeline()
	pipe.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		MaxLen: streamMaxLen,
		Approx: true,
		Values: map[string]any{"data": string(data)},
	})
	pipe.Expire(ctx, key, streamTTL)
	_, err = pipe.Exec(ctx)
	return err
}

// ReadNew 从 lastID 之后读取最多 count 条消息，返回解码后的 Flow 列表。
// lastID="0" 从头读取；返回空切片而非 nil 表示没有新消息。
func (s *Redis) ReadNew(ctx context.Context, engID uuid.UUID, lastID string, count int64) ([]flow.Flow, error) {
	key := fmt.Sprintf(streamKeyFmt, engID)
	// Block: -1 表示不添加 BLOCK 参数，立即返回（非阻塞）。
	// Block: 0 会翻译为 XREAD BLOCK 0，永久阻塞直到有数据，不适合轮询场景。
	msgs, err := s.rdb.XRead(ctx, &redis.XReadArgs{
		Streams: []string{key, lastID},
		Count:   count,
		Block:   -1,
	}).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("xread: %w", err)
	}
	var out []flow.Flow
	for _, stream := range msgs {
		for _, msg := range stream.Messages {
			raw, ok := msg.Values["data"].(string)
			if !ok {
				continue
			}
			var f flow.Flow
			if err := json.Unmarshal([]byte(raw), &f); err != nil {
				continue
			}
			out = append(out, f)
		}
	}
	if out == nil {
		out = []flow.Flow{}
	}
	return out, nil
}
