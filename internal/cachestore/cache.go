// Package cachestore 是资源无关的多级缓存内核：内存 L1（本进程）→ redis L2
// （跨进程共享 + 失效总线）→ 事实源（由调用方的 load 回调打 DB）。
//
// 为何抽这一层：api 与 runner 是**多进程**。任一进程改了配置，其它进程的本地 L1
// 必须被动失效，否则读到旧值。写路径写完事实源后经 redis PUBLISH 广播「要清哪些键」，
// 各进程的 Subscribe goroutine 收到即清本地 L1 + L2，下次读回填最新值。
//
// 与旧 configstore 内嵌实现的区别：失效消息只携带**缓存键列表**（写方本就要算 fillKeys），
// 订阅方无脑清这批键，不再按资源 kind 做 switch。于是一个进程共享**一个 Cache + 一条订阅
// 循环**，configstore / llmstore / settingstore 等所有资源复用同一实例（键各带全局前缀，不撞）。
//
// 缓存粒度由调用方决定：单条热读走 ReadThrough；列表读等低频、失效成本高的读直穿事实源不缓存。
package cachestore

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultL2TTL 是 redis L2 条目的保守兜底过期（防订阅漏消息导致的永久脏读）。
// 正常失效靠 PUBLISH 驱逐，TTL 只是安全网。
const DefaultL2TTL = 10 * time.Minute

// l1Cache 是进程内第一级缓存：RWMutex 保护的 key→json 字节表。
// 无 TTL——条目靠失效消息（本进程写 / redis 订阅）驱逐，不靠过期。
// 值统一存 json 字节（与 L2 同形），读路径按需 Unmarshal。
type l1Cache struct {
	mu sync.RWMutex
	m  map[string][]byte
}

func newL1() *l1Cache { return &l1Cache{m: make(map[string][]byte)} }

// get 返回键对应的 json 字节与命中标志。
func (c *l1Cache) get(key string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	b, ok := c.m[key]
	return b, ok
}

// set 存入 val 的副本，避免调用方复用底层数组污染缓存。
func (c *l1Cache) set(key string, val []byte) {
	cp := make([]byte, len(val))
	copy(cp, val)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = cp
}

// del 删除一批键（失效驱逐）。
func (c *l1Cache) del(keys ...string) {
	if len(keys) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range keys {
		delete(c.m, k)
	}
}

// Cache 编排 L1（本进程内存）+ L2（redis，跨进程共享 + 失效总线）。
// 一个进程构造一个共享实例，供所有资源仓储复用。
type Cache struct {
	rdb     *redis.Client
	l1      *l1Cache
	l2TTL   time.Duration
	channel string // 失效总线 redis pub/sub channel

	hooksMu sync.RWMutex
	hooks   []func(ctx context.Context, keys []string) // 跨进程失效后置回调（见 OnInvalidate）
}

// OnInvalidate 注册跨进程失效的后置回调：Subscribe 收到失效消息、清完本进程 L1+L2 后，
// 按注册序同步触发每个回调，并把本次失效的键列表传入。
//
// 用于「清缓存不够、还要主动重建运行期状态」的场景——典型是 proxy 进程收到 proxy_filter
// 失效后，立即重读 settingstore 重建过滤链并原子换入（Server.SwapFilter）。回调应快速返回
// （重活自行起 goroutine），且自行按键前缀判断是否与己相关（无脑触发，回调侧过滤）。
func (c *Cache) OnInvalidate(fn func(ctx context.Context, keys []string)) {
	c.hooksMu.Lock()
	c.hooks = append(c.hooks, fn)
	c.hooksMu.Unlock()
}

// fireHooks 按注册序同步调用所有失效回调。
func (c *Cache) fireHooks(ctx context.Context, keys []string) {
	c.hooksMu.RLock()
	hooks := c.hooks
	c.hooksMu.RUnlock()
	for _, fn := range hooks {
		fn(ctx, keys)
	}
}

// New 构造共享多级缓存。l2TTL<=0 时取 DefaultL2TTL。
func New(rdb *redis.Client, l2TTL time.Duration) *Cache {
	if l2TTL <= 0 {
		l2TTL = DefaultL2TTL
	}
	return &Cache{rdb: rdb, l1: newL1(), l2TTL: l2TTL, channel: invalidateChannel}
}

// ReadThrough 是多级读的统一泛型骨架：L1 命中即返 → L2（同键）命中回填 L1 → 事实源
// 经 load 加载后回填 L1+L2。fillKeys 返回该值应写入的全部缓存键（如一条场景的 code/id 双键）。
func ReadThrough[T any](
	ctx context.Context, c *Cache, primaryKey string,
	fillKeys func(T) []string, load func(context.Context) (T, error),
) (T, error) {
	var zero T
	// L1
	if b, ok := c.l1.get(primaryKey); ok {
		return decode[T](b, zero)
	}
	// L2
	if b, err := c.rdb.Get(ctx, primaryKey).Bytes(); err == nil {
		c.l1.set(primaryKey, b) // 回填 L1
		return decode[T](b, zero)
	}
	// 事实源
	v, err := load(ctx)
	if err != nil {
		return zero, err
	}
	c.Fill(ctx, v, fillKeys(v))
	return v, nil
}

// decode 把缓存字节反序列化为 T；失败返回 zero + err。
func decode[T any](b []byte, zero T) (T, error) {
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return zero, err
	}
	return v, nil
}

// Fill 把值 json 编码后写入给定的全部 L1+L2 键（L2 带兜底 TTL）。缓存失败不致命（下次读重试）。
func (c *Cache) Fill(ctx context.Context, v any, keys []string) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	for _, k := range keys {
		c.l1.set(k, b)
		_ = c.rdb.Set(ctx, k, b, c.l2TTL).Err()
	}
}

// DropL1 只清本地 L1 层（保留共享 L2）——供诊断 / 测试模拟「本进程冷启但 L2 仍热」。
func (c *Cache) DropL1(keys ...string) { c.l1.del(keys...) }

// L1Has 报告键是否命中本地 L1 层。诊断 / 测试用（断言失效是否生效）。
func (c *Cache) L1Has(key string) bool {
	_, ok := c.l1.get(key)
	return ok
}
