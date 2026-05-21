package credential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// fallbackHashKeyPrefix 在 caller 传入空 keyPrefix 时使用——保持与历史一致的
// "credentials:<host>" 数据布局，避免老 redis 数据 key 漂移。
//
// 数据布局：
//
//	HSET <prefix><host> <name1> <json1> <name2> <json2> ...
//	HGETALL <prefix><host>          // 一次往返拿全部 (name, json)
//	DEL     <prefix><host>          // 删整个 host 凭证
//	EXPIRE  <prefix><host> <ttl>    // 整个 host 一起过期
const fallbackHashKeyPrefix = "credentials:"

// RedisProvider 是 Provider 的 Redis 实现。
//
// keyPrefix 由 cmd 层从 config.Credential.RedisKeyPrefix 注入；
// 多项目共享 redis 时通过 yaml 改前缀即可隔离命名空间。
type RedisProvider struct {
	client    *redis.Client
	keyPrefix string
}

// NewRedis 构造 RedisProvider；keyPrefix 空时回退 fallbackHashKeyPrefix。
func NewRedis(client *redis.Client, keyPrefix string) *RedisProvider {
	if keyPrefix == "" {
		keyPrefix = fallbackHashKeyPrefix
	}
	return &RedisProvider{client: client, keyPrefix: keyPrefix}
}

// hashKey 拼接 host hash 的完整 Redis key。
func (r *RedisProvider) hashKey(host string) string {
	return r.keyPrefix + host
}

// BatchSave 用 Pipeline 批量写入；多 host 各自一次 HSET，再可选 EXPIRE。
// ttlSeconds=0 时不设过期，让 hash 永久存活。
func (r *RedisProvider) BatchSave(ctx context.Context, byHost map[string][]Identity, ttlSeconds int) error {
	pipe := r.client.Pipeline()

	for host, ids := range byHost {
		// 收集本 host 所有 (name, json) 对，一次 HSET 写入。
		fields := make(map[string]any, len(ids))
		for _, id := range ids {
			// 防御性跳过：anonymous 不是持久化身份（是 LLM 临时构造的测试概念），
			// caller 误传也不污染存储。
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
		pipe.HSet(ctx, r.hashKey(host), fields)
		if ttlSeconds > 0 {
			pipe.Expire(ctx, r.hashKey(host), time.Duration(ttlSeconds)*time.Second)
		}
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("batch save credentials: %w", err)
	}
	return nil
}

// GetIdentitiesByHost 返回 host 下所有预录入的真实身份。
//
// 不注入 anonymous：anonymous 是 LLM 在挖洞时临时构造的测试概念（从任一真实
// 身份的 credentials 数组拿模板、整段替换 value 为 lstoken），不是持久化对象。
// 反序列化失败的字段会被跳过（容错），避免脏数据阻塞读取。
func (r *RedisProvider) GetIdentitiesByHost(ctx context.Context, host string) ([]Identity, error) {
	out := []Identity{}

	res, err := r.client.HGetAll(ctx, r.hashKey(host)).Result()
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
	if err := r.client.Del(ctx, r.hashKey(host)).Err(); err != nil {
		return fmt.Errorf("del credentials for host %s: %w", host, err)
	}
	return nil
}

// 编译期保障：RedisProvider 满足 Provider 接口。
var _ Provider = (*RedisProvider)(nil)
