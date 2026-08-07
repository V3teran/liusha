package cachestore

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestCache 起一个 miniredis 支撑的共享缓存（不依赖真 Redis）。
func newTestCache(t *testing.T) *Cache {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return New(rdb, 0)
}

// OnInvalidate 注册的回调应在 Subscribe 收到失效消息、清完键后被触发，并拿到失效键列表。
func TestOnInvalidate_HookFiredWithKeys(t *testing.T) {
	c := newTestCache(t)

	var (
		mu      sync.Mutex
		gotKeys []string
		fired   = make(chan struct{}, 1)
	)
	c.OnInvalidate(func(_ context.Context, keys []string) {
		mu.Lock()
		gotKeys = append([]string(nil), keys...)
		mu.Unlock()
		select {
		case fired <- struct{}{}:
		default:
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Subscribe(ctx) }()

	// 预热 L1，验证失效后被清。
	c.Fill(ctx, "v", []string{"settingstore:proxy_filter"})
	if !c.L1Has("settingstore:proxy_filter") {
		t.Fatal("Fill 后 L1 应命中")
	}

	// 等订阅就绪后广播失效。
	waitSubscribed(t, c)
	if err := c.Invalidate(ctx, "settingstore:proxy_filter"); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}

	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("2s 内未触发 OnInvalidate 回调")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(gotKeys) != 1 || gotKeys[0] != "settingstore:proxy_filter" {
		t.Fatalf("回调收到的键列表错: %#v", gotKeys)
	}
	if c.L1Has("settingstore:proxy_filter") {
		t.Fatal("失效后 L1 仍命中——evict 未生效")
	}
}

// 多个回调按注册序全部触发。
func TestOnInvalidate_MultipleHooks(t *testing.T) {
	c := newTestCache(t)

	var wg sync.WaitGroup
	wg.Add(2)
	c.OnInvalidate(func(_ context.Context, _ []string) { wg.Done() })
	c.OnInvalidate(func(_ context.Context, _ []string) { wg.Done() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Subscribe(ctx) }()
	waitSubscribed(t, c)

	if err := c.Invalidate(ctx, "settingstore:react"); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("2s 内两个回调未全部触发")
	}
}

// waitSubscribed 轮询直到 miniredis 记录到订阅者，避免广播先于 Subscribe 就绪而丢失。
func waitSubscribed(t *testing.T, c *Cache) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n, err := c.rdb.PubSubNumSub(context.Background(), c.channel).Result(); err == nil {
			if cnt, ok := n[c.channel]; ok && cnt >= 1 {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("订阅者未在 2s 内就绪")
}
