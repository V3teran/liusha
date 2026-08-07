package settingstore

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/V3teran/liusha/internal/cachestore"
)

// fakeDB 是带命中计数的 settingDB 假实现（读穿透 + 缺组兜底断言用）。
type fakeDB struct {
	react      *ReactSettings
	runtime    *RuntimeSettings
	proxy      *ProxyFilterSettings
	reactHit   int64
	runtimeHit int64
	proxyHit   int64
}

func (f *fakeDB) GetReact(_ context.Context) (ReactSettings, error) {
	atomic.AddInt64(&f.reactHit, 1)
	if f.react == nil {
		return ReactSettings{}, pgx.ErrNoRows
	}
	return *f.react, nil
}
func (f *fakeDB) SaveReact(_ context.Context, v ReactSettings) error {
	cp := v
	f.react = &cp
	return nil
}
func (f *fakeDB) GetRuntime(_ context.Context) (RuntimeSettings, error) {
	atomic.AddInt64(&f.runtimeHit, 1)
	if f.runtime == nil {
		return RuntimeSettings{}, pgx.ErrNoRows
	}
	return *f.runtime, nil
}
func (f *fakeDB) SaveRuntime(_ context.Context, v RuntimeSettings) error {
	cp := v
	f.runtime = &cp
	return nil
}
func (f *fakeDB) GetProxyFilter(_ context.Context) (ProxyFilterSettings, error) {
	atomic.AddInt64(&f.proxyHit, 1)
	if f.proxy == nil {
		return ProxyFilterSettings{}, pgx.ErrNoRows
	}
	return f.proxy.normalized(), nil
}
func (f *fakeDB) SaveProxyFilter(_ context.Context, v ProxyFilterSettings) error {
	cp := v.normalized()
	if f.proxy != nil {
		*f.proxy = cp // 原地改：跨进程测试里两 fakeDB 共享同一 *ProxyFilterSettings，A 写 B 可见
	} else {
		f.proxy = &cp
	}
	return nil
}

// newTestStore 用 miniredis + 假底层 store 构造 Store。
func newTestStore(t *testing.T) (*Store, *fakeDB, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	db := &fakeDB{}
	s := newWithStore(db, cachestore.New(rdb, 0))
	return s, db, mr
}

// TestReact_RefillHitsDBOnce 验证：首读打 DB，二读命中 L1（DB 计数不增）。
func TestReact_RefillHitsDBOnce(t *testing.T) {
	ctx := context.Background()
	s, db, _ := newTestStore(t)
	db.react = &ReactSettings{TriggerRatio: 0.75, TrailingBudgetRatio: 0.15, CompactorTimeoutSeconds: 30}

	for i := 0; i < 3; i++ {
		got, err := s.React(ctx)
		if err != nil {
			t.Fatalf("react read #%d: %v", i, err)
		}
		if got.TriggerRatio != 0.75 {
			t.Fatalf("期望 trigger_ratio 0.75，实际 %v", got.TriggerRatio)
		}
	}
	if got := atomic.LoadInt64(&db.reactHit); got != 1 {
		t.Fatalf("期望 DB 只打 1 次（二读命中 L1），实际 %d", got)
	}
}

// TestRuntime_L2HitSkipsDB 验证：清 L1 后读命中 redis L2，不再打 DB。
func TestRuntime_L2HitSkipsDB(t *testing.T) {
	ctx := context.Background()
	s, db, _ := newTestStore(t)
	db.runtime = &RuntimeSettings{StepToolTimeoutSeconds: 1800, RunTailBytes: 8192, FindingsLimitInPrompt: 1000}

	if _, err := s.Runtime(ctx); err != nil { // 回填 L1+L2
		t.Fatalf("warm: %v", err)
	}
	s.cache.DropL1(keyRuntime) // 仅清 L1，保留 L2
	if _, err := s.Runtime(ctx); err != nil {
		t.Fatalf("read after L1 evict: %v", err)
	}
	if got := atomic.LoadInt64(&db.runtimeHit); got != 1 {
		t.Fatalf("期望 DB 仍只 1 次（L2 命中），实际 %d", got)
	}
}

// TestProxyFilter_NilSlicesNormalized 验证：DB 存 null 数组读回归一为空切片（非 nil）。
func TestProxyFilter_NilSlicesNormalized(t *testing.T) {
	ctx := context.Background()
	s, db, _ := newTestStore(t)
	db.proxy = &ProxyFilterSettings{MaxRequestBodySize: 2097152, MaxResponseBodySize: 8388608} // 数组全 nil

	got, err := s.ProxyFilter(ctx)
	if err != nil {
		t.Fatalf("proxy read: %v", err)
	}
	if got.ExcludeMethods == nil || got.ExcludeSuffixes == nil || got.ExcludeStatusCodes == nil {
		t.Fatal("nil 数组应归一为空切片")
	}
	if got.MaxRequestBodySize != 2097152 {
		t.Fatalf("标量应保留，实际 %d", got.MaxRequestBodySize)
	}
}

// TestSaveReact_InvalidatesSnapshot 验证：SaveReact 后哨兵键被清、下次读打 DB 拿新值。
func TestSaveReact_InvalidatesSnapshot(t *testing.T) {
	ctx := context.Background()
	s, db, _ := newTestStore(t)
	db.react = &ReactSettings{TriggerRatio: 0.75}

	if _, err := s.React(ctx); err != nil { // 暖 L1
		t.Fatal(err)
	}
	if !s.cache.L1Has(keyReact) {
		t.Fatal("首读后哨兵键应在 L1")
	}
	if err := s.SaveReact(ctx, ReactSettings{TriggerRatio: 0.90, TrailingBudgetRatio: 0.20, CompactorTimeoutSeconds: 45}); err != nil {
		t.Fatalf("save react: %v", err)
	}
	if s.cache.L1Has(keyReact) {
		t.Fatal("写后哨兵键应被失效")
	}
	got, err := s.React(ctx)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if got.TriggerRatio != 0.90 {
		t.Fatalf("应读到新值 0.90，实际 %v", got.TriggerRatio)
	}
}

// TestSaveProxyFilter_CrossProcessInvalidation 验证：进程 A 改过滤规则→PUBLISH→
// 进程 B 的 L1 被清、下次读拿新值（proxy 热改的跨进程一致性）。
func TestSaveProxyFilter_CrossProcessInvalidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	shared := &ProxyFilterSettings{ExcludeMethods: []string{"OPTIONS"}, MaxRequestBodySize: 1000}
	dbA := &fakeDB{proxy: shared}
	dbB := &fakeDB{proxy: shared} // 共享底层数据（同一 DB）
	a := newWithStore(dbA, cachestore.New(rdb, 0))
	bCache := cachestore.New(rdb, 0)
	b := newWithStore(dbB, bCache)

	go func() { _ = bCache.Subscribe(ctx) }()
	waitSubscribed(t, mr, 1)

	if _, err := b.ProxyFilter(ctx); err != nil { // 暖 B 的 L1
		t.Fatalf("B warm: %v", err)
	}

	// A 改上限（两 fakeDB 共享同一 *ProxyFilterSettings 指针，A 的 Save 覆写它）。
	if err := a.SaveProxyFilter(ctx, ProxyFilterSettings{ExcludeMethods: []string{"OPTIONS", "HEAD"}, MaxRequestBodySize: 2000}); err != nil {
		t.Fatalf("A save: %v", err)
	}

	waitForCondition(t, func() bool { return !b.cache.L1Has(keyProxyFilter) })
	got, err := b.ProxyFilter(ctx)
	if err != nil {
		t.Fatalf("B reread: %v", err)
	}
	if got.MaxRequestBodySize != 2000 {
		t.Fatalf("B 应读到新上限 2000，实际 %d", got.MaxRequestBodySize)
	}
}

// TestReact_NotFoundPropagates 验证：缺组时读返回 IsNotFound 可识别的错误（种子据此判存与否）。
func TestReact_NotFoundPropagates(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newTestStore(t)
	_, err := s.GetReactRaw(ctx)
	if !IsNotFound(err) {
		t.Fatalf("缺组应返回 IsNotFound 为真的错误，实际 %v", err)
	}
}

// waitSubscribed 轮询 miniredis 直到 channel 订阅者数达到 want。
func waitSubscribed(t *testing.T, mr *miniredis.Miniredis, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(mr.PubSubChannels("*")) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("等待订阅者超时")
}

// waitForCondition 轮询 cond 直到为真或超时。
func waitForCondition(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}
