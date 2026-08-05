package configstore

import (
	"context"
	"encoding/json"
	"fmt"
)

// invalidateChannel 是跨进程失效总线的 redis pub/sub channel。
// api 写配置后 PUBLISH 到此，各进程（api/runner）的 Subscribe goroutine 收到即清本地 L1 + L2。
const invalidateChannel = "configstore:invalidate"

// invalidation 是一条失效消息：kind 定资源类型，id/code 定具体行。
//   - scenario：id 与 code **都必填**（它有 id-map 与 code-map 两张映射，缺一残留脏条目）
//   - hunter：仅 id（无 code-map），code 留空
//   - sentinels：true 表示改动了猎手，额外失效两个哨兵键（编排猎手 + enabled 领域池）
type invalidation struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Code      string `json:"code,omitempty"`
	Sentinels bool   `json:"sentinels,omitempty"`
}

// publish 把一条失效消息广播到总线。写路径在清完本地缓存后调用。
func (s *Store) publish(ctx context.Context, msg invalidation) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal invalidation: %w", err)
	}
	if err := s.rdb.Publish(ctx, invalidateChannel, body).Err(); err != nil {
		return fmt.Errorf("publish invalidation: %w", err)
	}
	return nil
}

// Subscribe 后台订阅失效总线，收到消息即清本进程 L1 + L2 对应键，直到 ctx 取消。
// api/runner 进程各自 `go store.Subscribe(ctx)`。阻塞运行，返回时表示订阅结束。
func (s *Store) Subscribe(ctx context.Context) error {
	sub := s.rdb.Subscribe(ctx, invalidateChannel)
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
			s.evict(ctx, msg)
		}
	}
}

// evict 按失效消息清 L1 + L2 的对应键（含派生键），让下次读回填。
func (s *Store) evict(ctx context.Context, msg invalidation) {
	keys := keysFor(msg)
	s.l1.del(keys...)
	if len(keys) > 0 {
		_ = s.rdb.Del(ctx, keys...).Err() // L2 尽力删；漏删由 TTL 兜底
	}
}

// keysFor 把一条失效消息展开成需要清除的全部缓存键（L1/L2 同键）。
func keysFor(msg invalidation) []string {
	var keys []string
	switch msg.Kind {
	case kindScenario:
		if msg.Code != "" {
			keys = append(keys, keyScenarioCode(msg.Code))
		}
		if msg.ID != "" {
			keys = append(keys, keyScenarioID(msg.ID))
		}
	case kindHunter:
		if msg.ID != "" {
			keys = append(keys, keyHunterID(msg.ID))
		}
	}
	if msg.Sentinels {
		keys = append(keys, keyOrchestrator, keyEnabledDomain)
	}
	return keys
}
