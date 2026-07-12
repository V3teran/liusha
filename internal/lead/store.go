package lead

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// maxEntries 是单 host LIST 淘汰保留条数（LPUSH 新条目在头部，LTRIM 只留最新 200 条），
// 防单 host 无界增长。读时不再按 kind 截断——全量注入（每条一句话，200 条量级 prompt 装得下），
// 由读时精确去重收敛重复条目。
const maxEntries = 200

// Store 是情报黑板的 Redis 实现。key = "<keyPrefix>lead:<host>"，value = LIST（每条一个 JSON）。
//
// ttl = 滚动过期时长：每次 Append 刷新该 host 的 key TTL（持续写则一直续命，停写 ttl 后自净）。
// active/passive 一视同仁，不再分模式清理——同一 host 双模式不会互相抢过期时间。ttl≤0 关过期（仅 LTRIM 兜底）。
type Store struct {
	rdb       *redis.Client
	keyPrefix string
	ttl       time.Duration
}

// NewStore 用 pgxpool 无关——纯 Redis；keyPrefix 来自 cfg.Credential.RedisKeyPrefix（与 credential/hostsem 同租户命名空间）。
func NewStore(rdb *redis.Client, keyPrefix string, ttl time.Duration) *Store {
	return &Store{rdb: rdb, keyPrefix: keyPrefix, ttl: ttl}
}

// key 拼接：keyPrefix 约定自带尾部冒号（如 "credentials:"，见 credential.RedisProvider 同约定），
// 直接紧跟 "lead:" 拼接，不再额外插入冒号（否则会出现 "credentials::lead:host" 双冒号）。
func (s *Store) key(host string) string {
	return s.keyPrefix + "lead:" + host
}

// Append 写一条情报（纯 append，agent 零负担：不编 key 不判重，重复留给读时收敛）。
// LPUSH 后 LTRIM 保留最新 maxEntries 条，再滚动刷新 key TTL（持续写则一直续命，停写后自净）。
func (s *Store) Append(ctx context.Context, host string, e Entry) error {
	switch e.Kind {
	case KindClue, KindFact, KindDeadend:
	default:
		return fmt.Errorf("lead: 非法 kind %q", e.Kind)
	}
	if e.Detail == "" {
		return fmt.Errorf("lead: detail 必填")
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
	if s.ttl > 0 {
		pipe.Expire(ctx, key, s.ttl) // 滚动刷新：每次写都把过期时间拨回 ttl
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("append lead for host %s: %w", host, err)
	}
	return nil
}

// ReadRecent 读时按 kind 分组，全量返回（不再按 kind 截断），并做精确去重：detail 文本完全相同
// 的条目只保留最新一条（LIST 新→旧，首次见到即保留）。收敛 agent 复写同一句造成的重复注入，
// 不做语义合并（那需 LLM，属过度设计）。反序列化失败的条目容错跳过（脏数据不阻塞读取）。
func (s *Store) ReadRecent(ctx context.Context, host string) (map[Kind][]Entry, error) {
	raw, err := s.rdb.LRange(ctx, s.key(host), 0, maxEntries-1).Result()
	if err != nil {
		return nil, fmt.Errorf("lrange lead for host %s: %w", host, err)
	}
	out := map[Kind][]Entry{}
	seen := map[string]struct{}{} // (kind, detail) 精确去重键
	for _, item := range raw {
		var e Entry
		if err := json.Unmarshal([]byte(item), &e); err != nil {
			continue
		}
		dedupKey := string(e.Kind) + "\x00" + e.Detail
		if _, dup := seen[dedupKey]; dup {
			continue
		}
		seen[dedupKey] = struct{}{}
		out[e.Kind] = append(out[e.Kind], e)
	}
	return out, nil
}
