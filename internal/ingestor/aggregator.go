package ingestor

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// aggregator.go：passive 流量按 host 攒批的分布式窗口（见 spec §6.2 + §13.1）。
//
// ingestor 跑在水平扩展的 scanner 内，同 host 流量经 Redis 消费组分给不同实例——进程内内存
// 窗口是分片非副本，会把一批拆成 N 个残批。故窗口状态放 Redis：任何实例消费到某 host 流量都
// INCR 同一个计数键，达阈值/超时的实例抢锁（SET NX）建 task，跨实例只有一个赢家（§13.2 幂等）。
//
// 触发有两条路径，共用 claimAndReset 抢锁（跨实例幂等，只一个赢家建 task）：
//   ① 阈值触发：observe 时 count 达 batchSize → 立即触发（流量密集场景）。
//   ② 超时补偿：observe 的超时判定只在有新流量到达时才被求值，静默 host 的窗口无后续
//      流量则永不触发。故用后台 sweepExpired（独立时钟，见 traffic.sweepLoop）遍历活跃 host
//      集，对已超 window 的窗口主动建 task——兜住"打一批就停"的尾批（e2e 场景 + 生产静默 host）。
//
// 键（tenant 前缀由 caller 拼）：
//   {p}:agg:count:{host}   int   本窗口累计条数（INCR）
//   {p}:agg:first:{host}   int64 本窗口首条 unix ms（SET NX，判超时用）
//   {p}:agg:lock:{host}    lock  建 task 抢占锁（SET NX EX，防并发重复建）
//   {p}:agg:hosts          set   有未触发窗口的活跃 host 集（供 sweepExpired 遍历）

// hostWindow 是聚合器对单 host 窗口的判定结果。
type hostWindow struct {
	Count     int64
	FirstMs   int64
	Triggered bool // 达 batch 阈值或超窗口时长
}

// aggregator 用 Redis 维护 per-host 攒批窗口。
type aggregator struct {
	rdb       *redis.Client
	keyPrefix string
	batchSize int64
	window    time.Duration
}

func newAggregator(rdb *redis.Client, keyPrefix string, batchSize int, window time.Duration) *aggregator {
	return &aggregator{rdb: rdb, keyPrefix: keyPrefix, batchSize: int64(batchSize), window: window}
}

// batchN 返回配置的批大小（领取 proxy_traffic 时的上限）。
func (a *aggregator) batchN() int { return int(a.batchSize) }

func (a *aggregator) countKey(host string) string { return a.keyPrefix + ":agg:count:" + host }
func (a *aggregator) firstKey(host string) string { return a.keyPrefix + ":agg:first:" + host }
func (a *aggregator) lockKey(host string) string  { return a.keyPrefix + ":agg:lock:" + host }
func (a *aggregator) hostsKey() string            { return a.keyPrefix + ":agg:hosts" }

// observe 记一条 host 流量进窗口，返回是否应触发建 task。
//
// INCR 计数 + SET NX 首条时间戳（带 2×window 兜底 TTL 防泄漏）。达 batchSize 或距首条超
// window 即触发。触发判定只标记，不清窗口——清零在 reset（抢锁成功者调）。
func (a *aggregator) observe(ctx context.Context, host string) (hostWindow, error) {
	nowMs := time.Now().UnixMilli()
	ttl := 2 * a.window

	count, err := a.rdb.Incr(ctx, a.countKey(host)).Result()
	if err != nil {
		return hostWindow{}, fmt.Errorf("agg incr %s: %w", host, err)
	}
	if count == 1 {
		// 本窗口第一条：记首条时间戳 + 给两个键都设兜底 TTL（防某 host 再无流量时键泄漏）。
		a.rdb.Set(ctx, a.firstKey(host), nowMs, ttl)
		a.rdb.Expire(ctx, a.countKey(host), ttl)
		// 登记进活跃 host 集，供 sweepExpired 遍历（超时补偿路径）。claimAndReset 成功时 SREM。
		a.rdb.SAdd(ctx, a.hostsKey(), host)
	}
	firstMs, err := a.rdb.Get(ctx, a.firstKey(host)).Int64()
	if err != nil {
		// 首条键已过期但计数还在（边界）——用当前时间兜底，避免误判超时。
		firstMs = nowMs
	}

	w := hostWindow{Count: count, FirstMs: firstMs}
	w.Triggered = count >= a.batchSize || (nowMs-firstMs) >= a.window.Milliseconds()
	return w, nil
}

// claimAndReset 尝试抢锁并清窗口：抢到锁（本实例负责建 task）返回 true 并清零计数/首条键；
// 抢不到（别的实例先占）返回 false。锁 TTL 短（建 task 是快操作），到期自动释放防死锁。
func (a *aggregator) claimAndReset(ctx context.Context, host string) (bool, error) {
	ok, err := a.rdb.SetNX(ctx, a.lockKey(host), "1", 10*time.Second).Result()
	if err != nil {
		return false, fmt.Errorf("agg lock %s: %w", host, err)
	}
	if !ok {
		return false, nil
	}
	// 抢到锁：清窗口计数 + 首条键 + 从活跃集移除，让后续流量开新窗口。
	// 锁不立即删——留 TTL 内挡住并发重复触发。
	a.rdb.Del(ctx, a.countKey(host), a.firstKey(host))
	a.rdb.SRem(ctx, a.hostsKey(), host)
	return true, nil
}

// sweepExpired 遍历活跃 host 集，返回窗口已超 window 时长、应超时触发建 task 的 host 列表。
//
// 这是"超时补偿"的时钟源（observe 的超时判定只在有新流量时才被求值，静默 host 需靠它兜底）。
// 只做判定不抢锁——调用方对返回的每个 host 走 claimAndReset（跨实例幂等）后建 task。
// 顺带清理孤儿：host 在集里但 firstKey 已过期/被清（别的实例已处理）→ SREM 剔除。
func (a *aggregator) sweepExpired(ctx context.Context) ([]string, error) {
	hosts, err := a.rdb.SMembers(ctx, a.hostsKey()).Result()
	if err != nil {
		return nil, fmt.Errorf("agg sweep smembers: %w", err)
	}
	nowMs := time.Now().UnixMilli()
	var expired []string
	for _, host := range hosts {
		firstMs, err := a.rdb.Get(ctx, a.firstKey(host)).Int64()
		if err != nil {
			// firstKey 不存在（已被 claimAndReset 清 / TTL 过期）→ 该 host 是孤儿，剔除。
			a.rdb.SRem(ctx, a.hostsKey(), host)
			continue
		}
		if (nowMs - firstMs) >= a.window.Milliseconds() {
			expired = append(expired, host)
		}
	}
	return expired, nil
}
