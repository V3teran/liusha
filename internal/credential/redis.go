package credential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// hashKeyPrefix 是 host 维度凭证 hash 的 key 前缀。
//
// 数据布局（v1.1 改造，原版 SCAN+多 GET → HASH 单次 HGETALL）：
//
//	HSET liusha:credentials:<host> <name1> <json1> <name2> <json2> ...
//	HGETALL liusha:credentials:<host>          // 一次往返拿全部 (name, json)
//	HDEL    liusha:credentials:<host> <name>   // 删单个身份
//	DEL     liusha:credentials:<host>          // 删整个 host 凭证
//	EXPIRE  liusha:credentials:<host> <ttl>    // 整个 host 一起过期
//
// 优势：
//   - 一次 HGETALL = O(1) 网络 + O(N) Redis 内部扫，不再是 SCAN 多 GET
//   - 多次 BatchSave 同 host 自动累积到同一 hash，无需 KEYS 查全量
//   - 整个 host 共享 TTL（同寿命语义）
const hashKeyPrefix = "liusha:credentials:"

// hashKey 拼接 host hash 的完整 Redis key。
func hashKey(host string) string {
	return hashKeyPrefix + host
}

// RedisProvider 是 Provider 的 Redis 实现。
type RedisProvider struct {
	client *redis.Client
}

// NewRedis 构造 RedisProvider。
func NewRedis(client *redis.Client) *RedisProvider {
	return &RedisProvider{client: client}
}

// BatchSave 用 Pipeline 批量写入；多 host 各自一次 HSET，再可选 EXPIRE。
// ttlSeconds=0 时不设过期，让 hash 永久存活。
func (r *RedisProvider) BatchSave(ctx context.Context, byHost map[string][]Identity, ttlSeconds int) error {
	pipe := r.client.Pipeline()

	for host, ids := range byHost {
		// 收集本 host 所有 (name, json) 对，一次 HSET 写入。
		fields := make(map[string]any, len(ids))
		for _, id := range ids {
			// anonymous 不持久化，由 GetIdentitiesByHost 注入。
			if id.Name == AnonymousName {
				continue
			}
			payload, err := json.Marshal(id)
			if err != nil {
				return fmt.Errorf("marshal identity %s/%s: %w", host, id.Name, err)
			}
			fields[id.Name] = payload
		}
		if len(fields) == 0 {
			continue
		}
		pipe.HSet(ctx, hashKey(host), fields)
		if ttlSeconds > 0 {
			pipe.Expire(ctx, hashKey(host), time.Duration(ttlSeconds)*time.Second)
		}
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("batch save credentials: %w", err)
	}
	return nil
}

// GetIdentitiesByHost 返回 host 下全部身份；总是包含 anonymous。
// 反序列化失败的字段会被跳过（容错），避免脏数据阻塞读取。
func (r *RedisProvider) GetIdentitiesByHost(ctx context.Context, host string) ([]Identity, error) {
	out := []Identity{{Name: AnonymousName, Role: AnonymousName}}

	res, err := r.client.HGetAll(ctx, hashKey(host)).Result()
	if err != nil {
		// hash 不存在不算错（首次访问 host）；其他错误向上传播。
		if errors.Is(err, redis.Nil) {
			return out, nil
		}
		return nil, fmt.Errorf("hgetall credentials for host %s: %w", host, err)
	}
	for _, payload := range res {
		var id Identity
		// 反序列化失败容错跳过：脏数据不应阻塞业务。
		if err := json.Unmarshal([]byte(payload), &id); err != nil {
			continue
		}
		out = append(out, id)
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

// Delete 清空 host 下所有持久化身份；anonymous 不受影响（不存于 Redis）。
// 一次 DEL 即可，不再需要 SCAN 游标循环。
func (r *RedisProvider) Delete(ctx context.Context, host string) error {
	if err := r.client.Del(ctx, hashKey(host)).Err(); err != nil {
		return fmt.Errorf("del credentials for host %s: %w", host, err)
	}
	return nil
}

// 编译期保障：RedisProvider 满足 Provider 接口。
var _ Provider = (*RedisProvider)(nil)
