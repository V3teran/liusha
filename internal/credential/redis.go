package credential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// keyPrefix 是所有活凭证的 key 前缀，集中常量便于 ops 排查。
const keyPrefix = "liusha:credential:"

// scanCount 控制 SCAN 单次返回 key 数量，平衡吞吐与单次往返延迟。
const scanCount = 100

// RedisProvider 是 Provider 的 Redis 实现：每条 identity 一个 key，
// 用 SCAN 列举，避免 KEYS 阻塞主线程。
type RedisProvider struct {
	client *redis.Client
}

// NewRedis 构造 RedisProvider。
func NewRedis(client *redis.Client) *RedisProvider {
	return &RedisProvider{client: client}
}

// key 拼接 host + name 的 Redis key。
// 形如 liusha:credential:vulnapp:admin。
func key(host, name string) string {
	return keyPrefix + host + ":" + name
}

// scanPattern 匹配指定 host 下所有 identity key。
func scanPattern(host string) string {
	return keyPrefix + host + ":*"
}

// BatchSave 用 Pipeline 批量写入，减少 RTT。
// ttlSeconds=0 时 SET 不带 EX，让 key 永久存活。
func (r *RedisProvider) BatchSave(ctx context.Context, byHost map[string][]Identity, ttlSeconds int) error {
	pipe := r.client.Pipeline()
	ttl := time.Duration(ttlSeconds) * time.Second

	for host, ids := range byHost {
		for _, id := range ids {
			// anonymous 不持久化，由 GetIdentitiesByHost 注入。
			if id.Name == AnonymousName {
				continue
			}
			payload, err := json.Marshal(id)
			if err != nil {
				return fmt.Errorf("marshal identity %s/%s: %w", host, id.Name, err)
			}
			// ttl=0 → SET 无过期；>0 → SET EX。
			pipe.Set(ctx, key(host, id.Name), payload, ttl)
		}
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("batch save credentials: %w", err)
	}
	return nil
}

// GetIdentitiesByHost 返回 host 下全部身份；总是包含 anonymous。
// 反序列化失败的 key 会被跳过（容错），避免一条脏数据阻塞整体读取。
func (r *RedisProvider) GetIdentitiesByHost(ctx context.Context, host string) ([]Identity, error) {
	out := []Identity{{Name: AnonymousName, Role: AnonymousName}}

	var cursor uint64
	pattern := scanPattern(host)
	for {
		keys, next, err := r.client.Scan(ctx, cursor, pattern, scanCount).Result()
		if err != nil {
			return nil, fmt.Errorf("scan credentials for host %s: %w", host, err)
		}
		for _, k := range keys {
			val, err := r.client.Get(ctx, k).Bytes()
			if err != nil {
				// key 可能在 SCAN 与 GET 之间过期，redis.Nil 视为正常跳过。
				if errors.Is(err, redis.Nil) {
					continue
				}
				return nil, fmt.Errorf("get credential %s: %w", k, err)
			}
			var id Identity
			// 反序列化失败容错跳过：脏数据不应阻塞业务。
			if err := json.Unmarshal(val, &id); err != nil {
				continue
			}
			out = append(out, id)
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	return out, nil
}

// List 是 GetIdentitiesByHost 的 map 形式封装，方便批量 API 直接消费。
func (r *RedisProvider) List(ctx context.Context, host string) (map[string][]Identity, error) {
	ids, err := r.GetIdentitiesByHost(ctx, host)
	if err != nil {
		return nil, err
	}
	return map[string][]Identity{host: ids}, nil
}

// Delete 用 SCAN+DEL 清空 host 下所有持久化身份；anonymous 不受影响（不存于 Redis）。
func (r *RedisProvider) Delete(ctx context.Context, host string) error {
	var cursor uint64
	pattern := scanPattern(host)
	for {
		keys, next, err := r.client.Scan(ctx, cursor, pattern, scanCount).Result()
		if err != nil {
			return fmt.Errorf("scan credentials for delete %s: %w", host, err)
		}
		if len(keys) > 0 {
			if err := r.client.Del(ctx, keys...).Err(); err != nil {
				return fmt.Errorf("del credentials for host %s: %w", host, err)
			}
		}
		if next == 0 {
			return nil
		}
		cursor = next
	}
}

// 编译期保障：RedisProvider 满足 Provider 接口。
var _ Provider = (*RedisProvider)(nil)
