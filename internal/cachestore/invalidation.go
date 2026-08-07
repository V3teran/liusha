package cachestore

import (
	"context"
	"encoding/json"
	"fmt"
)

// invalidateChannel 是跨进程失效总线的 redis pub/sub channel。所有资源共用一条——
// 消息只携带键列表，订阅方无脑清键，无需按资源分 channel。
const invalidateChannel = "cachestore:invalidate"

// invalidation 是一条失效消息：直接携带要清除的全部缓存键。
// 写方本就为 Fill 算过 fillKeys，广播同一批键即可，订阅方无脑清，无 per-resource 逻辑。
type invalidation struct {
	Keys []string `json:"keys"`
}

// Invalidate 清本进程 L1+L2 对应键，并广播失效消息（本地即时 + 跨进程最终一致）。
// 写路径在写完事实源后调用：本进程立即清而非等自己的订阅回环，避免写后瞬时读到脏值。
func (c *Cache) Invalidate(ctx context.Context, keys ...string) error {
	c.evict(ctx, keys)
	return c.publish(ctx, keys)
}

// publish 把一批要清的键广播到总线。
func (c *Cache) publish(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	body, err := json.Marshal(invalidation{Keys: keys})
	if err != nil {
		return fmt.Errorf("marshal invalidation: %w", err)
	}
	if err := c.rdb.Publish(ctx, c.channel, body).Err(); err != nil {
		return fmt.Errorf("publish invalidation: %w", err)
	}
	return nil
}

// evict 清 L1 + L2 的给定键，让下次读回填。
func (c *Cache) evict(ctx context.Context, keys []string) {
	if len(keys) == 0 {
		return
	}
	c.l1.del(keys...)
	_ = c.rdb.Del(ctx, keys...).Err() // L2 尽力删；漏删由 TTL 兜底
}

// Subscribe 后台订阅失效总线，收到消息即清本进程 L1 + L2 对应键，直到 ctx 取消。
// 每个进程（api/runner）起**一条** `go cache.Subscribe(ctx)` 即覆盖所有资源。
// 阻塞运行，返回时表示订阅结束。
func (c *Cache) Subscribe(ctx context.Context) error {
	sub := c.rdb.Subscribe(ctx, c.channel)
	defer func() { _ = sub.Close() }()

	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case m, ok := <-ch:
			if !ok {
				return nil
			}
			var msg invalidation
			if err := json.Unmarshal([]byte(m.Payload), &msg); err != nil {
				continue // 脏消息跳过，不拖垮订阅循环
			}
			c.evict(ctx, msg.Keys)
			c.fireHooks(ctx, msg.Keys) // 清完键后触发后置回调（如 proxy 重建过滤链）
		}
	}
}
