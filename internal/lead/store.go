package lead

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// maxEntries 是单 host LIST 淘汰保留条数（LPUSH 新条目在头部，LTRIM 只留最新 200 条）。
	maxEntries = 200
	// perKindLimit 是读时每 kind 注入 prompt 的截断条数（§7.4：新压旧，时序+数量截断）。
	perKindLimit = 5
)

// Store 是情报黑板的 Redis 实现。key = "<keyPrefix>:lead:<host>"，value = LIST（每条一个 JSON）。
type Store struct {
	rdb       *redis.Client
	keyPrefix string
}

// NewStore 用 pgxpool 无关——纯 Redis；keyPrefix 来自 cfg.Credential.RedisKeyPrefix（与 credential/hostsem 同租户命名空间）。
func NewStore(rdb *redis.Client, keyPrefix string) *Store {
	return &Store{rdb: rdb, keyPrefix: keyPrefix}
}

func (s *Store) key(host string) string {
	return s.keyPrefix + ":lead:" + host
}

// Append 写一条情报（纯 append，agent 零负担：不编 key 不判重）。
// LPUSH 后立即 LTRIM 保留最新 maxEntries 条，防单 host 无界增长。
func (s *Store) Append(ctx context.Context, host string, e Entry) error {
	switch e.Kind {
	case KindClue, KindFact, KindDeadend:
	default:
		return fmt.Errorf("lead: 非法 kind %q", e.Kind)
	}
	if e.Note == "" {
		return fmt.Errorf("lead: note 必填")
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	payload, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal lead entry: %w", err)
	}
	key := s.key(host)
	pipe := s.rdb.Pipeline()
	pipe.LPush(ctx, key, payload)
	pipe.LTrim(ctx, key, 0, maxEntries-1)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("append lead for host %s: %w", host, err)
	}
	return nil
}

// ExpireHost 给该 host 的情报黑板设冷却 TTL（active task 终态后调用，§7.2）。
// passive 不调此方法——LTRIM 已够界，无需额外过期。
func (s *Store) ExpireHost(ctx context.Context, host string, ttl time.Duration) error {
	if err := s.rdb.Expire(ctx, s.key(host), ttl).Err(); err != nil {
		return fmt.Errorf("expire lead for host %s: %w", host, err)
	}
	return nil
}

// ReadRecent 读时按 kind 分组，每组取最近 perKindLimit 条（LIST 本就新→旧，直接按序截断）。
// 反序列化失败的条目容错跳过（脏数据不阻塞读取，与 credential/lesson 一致）。
func (s *Store) ReadRecent(ctx context.Context, host string) (map[Kind][]Entry, error) {
	raw, err := s.rdb.LRange(ctx, s.key(host), 0, maxEntries-1).Result()
	if err != nil {
		return nil, fmt.Errorf("lrange lead for host %s: %w", host, err)
	}
	out := map[Kind][]Entry{}
	for _, item := range raw {
		var e Entry
		if err := json.Unmarshal([]byte(item), &e); err != nil {
			continue
		}
		if len(out[e.Kind]) >= perKindLimit {
			continue
		}
		out[e.Kind] = append(out[e.Kind], e)
	}
	return out, nil
}
