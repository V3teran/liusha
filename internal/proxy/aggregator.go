// Package proxy hosts liusha's in-process MITM 流量切片、去重、聚合。
// 与 liusha2 的差异：无 RuntimeConfig 热更新；所有参数静态注入；持久化由 sink 承担。
package proxy

import (
	"context"
	"errors"
	"sync"
	"time"
)

// TrafficSnapshot 一条 HTTP 流量在内存中的可序列化快照。
//
// 字段构成（11 个）：
//
//	ID              全局唯一 id（uuid 等）
//	Host            host header（去端口）
//	Method          GET/POST/...
//	Scheme          http / https
//	URI             含 query 的完整 path
//	StatusCode      响应状态码
//	RequestHeaders  请求头（key 小写化）
//	ResponseHeaders 响应头
//	RequestBody     请求体（已截断到 MaxRequestBodySize）
//	ResponseBody    响应体（已截断到 MaxResponseBodySize）
//	Timestamp       捕获时间（host 本地时钟，UTC）
type TrafficSnapshot struct {
	ID              string
	Host            string
	Method          string
	Scheme          string
	URI             string
	StatusCode      int
	RequestHeaders  map[string]string
	ResponseHeaders map[string]string
	RequestBody     []byte
	ResponseBody    []byte
	Timestamp       time.Time
}

// AggregatorSink 接收聚合输出。Aggregator 不直接知道存储/队列实现。
//
//	典型实现：写 Postgres + 入 Asynq 队列；ctx 超时由调用方控制。
type AggregatorSink interface {
	Flush(ctx context.Context, snapshots []*TrafficSnapshot) error
}

// Aggregator 把单条 snapshot 攒成批，按计数阈值或时间窗口触发 sink.Flush；
// 同时维护 deduplicator 的 hash 过期清理。
type Aggregator struct {
	mu sync.Mutex

	// 缓冲与依赖
	snapshots []*TrafficSnapshot
	dedup     *TrafficDeduplicator
	sink      AggregatorSink

	// 触发参数（构造时定，运行时不变）
	timeWindow     time.Duration
	countThreshold int
	dedupeWindow   time.Duration

	// 生命周期
	ticker        *time.Ticker
	cleanupTicker *time.Ticker
	stopCh        chan struct{}
	wg            sync.WaitGroup
	startedOnce   sync.Once
	stoppedOnce   sync.Once

	// 统计（测试与监控用）
	totalDuplicated int
}

// NewAggregator 构造聚合器。
//
//	timeWindow:     周期 flush 间隔（如 10s）
//	dedupeWindow:   去重窗口（如 60s），同时是 hash 清理周期
//	countThreshold: 缓冲达到此数量立即 flush（如 100）
//	dedup / sink:   依赖；dedup nil → 不去重；sink nil → flush 不外发
func NewAggregator(
	timeWindow, dedupeWindow time.Duration,
	countThreshold int,
	dedup *TrafficDeduplicator,
	sink AggregatorSink,
) *Aggregator {
	if countThreshold <= 0 {
		countThreshold = 100
	}
	if timeWindow <= 0 {
		timeWindow = 10 * time.Second
	}
	if dedupeWindow <= 0 {
		dedupeWindow = 60 * time.Second
	}
	return &Aggregator{
		snapshots:      make([]*TrafficSnapshot, 0, countThreshold),
		dedup:          dedup,
		sink:           sink,
		timeWindow:     timeWindow,
		countThreshold: countThreshold,
		dedupeWindow:   dedupeWindow,
		stopCh:         make(chan struct{}),
	}
}

// Start 启动两个后台 goroutine：周期 flush + 周期 cleanup hash。
// 重复调用安全（仅首次生效）。
func (a *Aggregator) Start(ctx context.Context) {
	a.startedOnce.Do(func() {
		a.ticker = time.NewTicker(a.timeWindow)
		// hash 清理频率与去重窗口对齐即可
		a.cleanupTicker = time.NewTicker(a.dedupeWindow)

		a.wg.Add(2)

		go func() {
			defer a.wg.Done()
			defer a.ticker.Stop()
			for {
				select {
				case <-a.ticker.C:
					a.flush(ctx)
				case <-a.stopCh:
					return
				case <-ctx.Done():
					return
				}
			}
		}()

		go func() {
			defer a.wg.Done()
			defer a.cleanupTicker.Stop()
			for {
				select {
				case <-a.cleanupTicker.C:
					if a.dedup != nil {
						a.dedup.Cleanup(time.Now().Unix(), int64(a.dedupeWindow.Seconds()))
					}
				case <-a.stopCh:
					return
				case <-ctx.Done():
					return
				}
			}
		}()
	})
}

// Stop 优雅退出：通知后台 goroutine 收工，并 flush pending。
// 可重复调用。
func (a *Aggregator) Stop() {
	a.stoppedOnce.Do(func() {
		close(a.stopCh)
		a.wg.Wait()
		// 兜底 flush（外部 ctx 可能已取消，因此用 background）
		a.flush(context.Background())
	})
}

// Add 接收一条 snapshot：去重后入缓冲；满 countThreshold 立即触发 flush。
//
// 满时同步 flush（非异步），保证 Stop 后无遗留 goroutine 写 sink。
func (a *Aggregator) Add(snap *TrafficSnapshot) {
	if snap == nil {
		return
	}

	a.mu.Lock()
	if a.dedup != nil {
		hash := a.dedup.CalculateHash(snap)
		ts := snap.Timestamp.Unix()
		if ts == 0 {
			ts = time.Now().Unix()
		}
		if a.dedup.IsDuplicate(hash, ts, int64(a.dedupeWindow.Seconds())) {
			a.totalDuplicated++
			a.mu.Unlock()
			return
		}
	}
	a.snapshots = append(a.snapshots, snap)
	full := len(a.snapshots) >= a.countThreshold
	a.mu.Unlock()

	if full {
		a.flush(context.Background())
	}
}

// flush 把 buffer 整个交给 sink；buffer 在锁内置空再解锁，避免与 Add 竞争。
func (a *Aggregator) flush(ctx context.Context) {
	a.mu.Lock()
	if len(a.snapshots) == 0 {
		a.mu.Unlock()
		return
	}
	batch := a.snapshots
	a.snapshots = make([]*TrafficSnapshot, 0, a.countThreshold)
	a.mu.Unlock()

	if a.sink == nil {
		return
	}
	// 错误吞掉是有意的：聚合层不感知存储语义；sink 内部自行做日志/重试
	_ = a.sink.Flush(ctx, batch)
}

// Pending 当前缓冲的 snapshot 数（测试与监控用）。
func (a *Aggregator) Pending() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.snapshots)
}

// TotalDuplicated 累计被去重的条数（测试与监控用）。
func (a *Aggregator) TotalDuplicated() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.totalDuplicated
}

// ErrSinkClosed sink 实现侧的约定错误（聚合器本身不抛；放此处便于上层引用）。
var ErrSinkClosed = errors.New("aggregator sink closed")
