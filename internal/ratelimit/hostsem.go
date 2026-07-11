// Package ratelimit 实现 per-host 并发信号量（见 spec §4.3）。
//
// 渗透场景硬需求：全局 asynq 并发挡不住"多个 task 恰好打同一 host"——同目标叠打会触发
// 目标 WAF 封 IP / 目标过载。业界扫描器都有 per-target 并发上限。
//
// 这是**并发数**限制（同 host 同时运行的 task ≤ limit），非频率限制。用 Redis INCR/DECR
// 计数信号量：Acquire INCR，超上限则 DECR 回滚返回 false；Release DECR。key 带 TTL 兜底——
// 持有者进程崩溃未 Release 时，计数不会永久泄漏（TTL 到期自动清）。
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// HostSemaphore 是按 host 的并发信号量。
type HostSemaphore struct {
	rdb       *redis.Client
	keyPrefix string
	limit     int64
	ttl       time.Duration // 计数键兜底 TTL（防持有者崩溃泄漏）
}

// NewHostSemaphore 构造 per-host 信号量。limit ≤ 0 时视为不限制（Acquire 恒成功）。
// ttl 应 ≥ 单 task 最长运行时长 + 缓冲（防长 task 运行期被 TTL 误清导致计数漂移）。
func NewHostSemaphore(rdb *redis.Client, keyPrefix string, limit int, ttl time.Duration) *HostSemaphore {
	return &HostSemaphore{rdb: rdb, keyPrefix: keyPrefix, limit: int64(limit), ttl: ttl}
}

func (s *HostSemaphore) key(host string) string { return s.keyPrefix + ":hostsem:" + host }

// Acquire 尝试为 host 占一个并发额度。
//   - limit ≤ 0（不限）或 host 为空 → 返回 (noopRelease, true, nil)。
//   - 占用成功 → 返回 (release, true, nil)，调用方 defer release。
//   - 已达上限 → 回滚计数，返回 (nil, false, nil)，调用方据此退避重试。
func (s *HostSemaphore) Acquire(ctx context.Context, host string) (release func(), ok bool, err error) {
	if s.limit <= 0 || host == "" {
		return func() {}, true, nil
	}
	key := s.key(host)
	n, err := s.rdb.Incr(ctx, key).Result()
	if err != nil {
		return nil, false, fmt.Errorf("hostsem incr %s: %w", host, err)
	}
	// 每次 INCR 都刷新 TTL：只要 host 还有活跃 task，计数键就不过期；全部释放后靠 TTL 兜底清零。
	s.rdb.Expire(ctx, key, s.ttl)
	if n > s.limit {
		// 超上限：回滚本次 INCR，返回未占用。不 DECR 到负数（DECR 幂等安全，但仅回滚自己这一次）。
		s.rdb.Decr(ctx, key)
		return nil, false, nil
	}
	return func() { s.release(host) }, true, nil
}

// release 释放一个并发额度（DECR）。用独立 context——即便请求 ctx 已取消也要归还额度，
// 否则计数只增不减会把 host 永久锁死。
func (s *HostSemaphore) release(host string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s.rdb.Decr(ctx, s.key(host))
}
