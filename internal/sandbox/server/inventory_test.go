package server

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
)

// blockingProbe 是一次可控的探测：每次调用登记、等 release 放行后返回 inv，并记下放行时
// 探测自己的 ctx 是否已被取消。
type blockingProbe struct {
	calls     atomic.Int32
	started   chan struct{}
	release   chan sandbox.Inventory
	cancelled atomic.Bool
}

func newBlockingProbe() *blockingProbe {
	return &blockingProbe{started: make(chan struct{}, 8), release: make(chan sandbox.Inventory)}
}

func (p *blockingProbe) probe(ctx context.Context) sandbox.Inventory {
	p.calls.Add(1)
	p.started <- struct{}{}
	inv := <-p.release
	if ctx.Err() != nil {
		p.cancelled.Store(true)
	}
	return inv
}

func installed(names ...string) sandbox.Inventory {
	inv := sandbox.Inventory{Tools: map[string]bool{}}
	for _, n := range names {
		inv.Tools[n] = true
	}
	return inv
}

func TestInventoryCache_AbandonedRequestDoesNotPoisonCache(t *testing.T) {
	p := newBlockingProbe()
	c := newInventoryCache(p.probe)

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := c.get(ctx)
		errc <- err
	}()
	<-p.started
	cancel() // 请求方在首次探测结束前放弃
	if err := <-errc; err == nil {
		t.Fatal("请求方放弃后 get 应返回它的 ctx 错误")
	}

	p.release <- installed("nmap")
	got, err := c.get(context.Background())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Tools["nmap"] {
		t.Fatalf("缓存应为完整探测的结果，得到 %+v", got)
	}
	if p.cancelled.Load() {
		t.Fatal("探测不应随请求方取消")
	}
	if n := p.calls.Load(); n != 1 {
		t.Fatalf("探测次数 = %d，期望 1", n)
	}
}

func TestInventoryCache_ServesStaleWhileRefreshing(t *testing.T) {
	p := newBlockingProbe()
	c := newInventoryCache(p.probe)

	go func() { <-p.started; p.release <- installed("nmap") }()
	if _, err := c.get(context.Background()); err != nil {
		t.Fatalf("首次 get: %v", err)
	}

	c.mu.Lock()
	c.expires = time.Now().Add(-time.Second) // 过期
	c.mu.Unlock()

	got, err := c.get(context.Background()) // 不等后台刷新
	if err != nil || !got.Tools["nmap"] {
		t.Fatalf("过期后应立即返回旧值，得到 %+v, %v", got, err)
	}
	<-p.started
	p.release <- installed("nmap", "sqlmap")

	deadline := time.Now().Add(5 * time.Second)
	for {
		got, _ = c.get(context.Background())
		if got.Tools["sqlmap"] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("后台刷新的结果没有生效")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := p.calls.Load(); n != 2 {
		t.Fatalf("探测次数 = %d，期望 2", n)
	}
}

func TestInventoryCache_ConcurrentRequestsShareOneProbe(t *testing.T) {
	p := newBlockingProbe()
	c := newInventoryCache(p.probe)

	const n = 8
	var wg sync.WaitGroup
	results := make(chan sandbox.Inventory, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			inv, err := c.get(context.Background())
			if err != nil {
				t.Errorf("get: %v", err)
			}
			results <- inv
		}()
	}
	<-p.started
	p.release <- installed("nmap")
	wg.Wait()
	close(results)
	for inv := range results {
		if !inv.Tools["nmap"] {
			t.Fatalf("所有请求方应拿到同一次探测的结果，得到 %+v", inv)
		}
	}
	if got := p.calls.Load(); got != 1 {
		t.Fatalf("探测次数 = %d，期望 1", got)
	}
}
