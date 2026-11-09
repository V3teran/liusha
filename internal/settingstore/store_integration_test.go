//go:build integration

package settingstore

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/override"
)

func testDefaults() Defaults {
	cfg := config.AppConfig{}
	cfg.ApplyDefaults()
	cfg.Proxy.ExcludeHosts = []string{"cdn.example"}
	return DefaultsFrom(cfg)
}

func newStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	mr := miniredis.RunT(t)
	return New(pool, cachestore.New(redis.NewClient(&redis.Options{Addr: mr.Addr()}), 0), testDefaults()), context.Background()
}

func TestPatchResetAndDefaults(t *testing.T) {
	s, ctx := newStore(t)
	rt, err := s.Runtime(ctx)
	if err != nil || rt.StepLLMTimeoutSeconds != 300 || rt.RunTailBytes != 8192 {
		t.Fatalf("未自定义时应为出厂默认: %+v %v", rt, err)
	}
	if err := s.Patch(ctx, GroupRuntime, override.Document{"run_tail_bytes": json.RawMessage(`16384`)}); err != nil {
		t.Fatal(err)
	}
	d, _ := s.Detail(ctx, GroupRuntime)
	if string(d.Values["run_tail_bytes"]) != "16384" || len(d.Overrides) != 1 {
		t.Fatalf("应只自定义 run_tail_bytes: values=%s overrides=%v", d.Values["run_tail_bytes"], d.Overrides)
	}
	// 写回与出厂相同的值即不算自定义
	if err := s.Patch(ctx, GroupRuntime, override.Document{"run_tail_bytes": json.RawMessage(`8192`)}); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.Detail(ctx, GroupRuntime); len(d.Overrides) != 0 {
		t.Fatalf("与出厂相同不应留自定义: %v", d.Overrides)
	}
	if err := s.Patch(ctx, GroupCompaction, override.Document{"planner_context_ratio": json.RawMessage(`0.8`)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Reset(ctx, GroupCompaction, "planner_context_ratio"); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Compaction(ctx); c.PlannerContextRatio != 0.6 {
		t.Fatalf("恢复后应为出厂 0.6: %+v", c)
	}
}

func TestPatchValidation(t *testing.T) {
	s, ctx := newStore(t)
	bad := []struct {
		g Group
		d override.Document
	}{
		{GroupRuntime, override.Document{"run_tail_bytes": json.RawMessage(`0`)}},
		{GroupCompaction, override.Document{"monitor_context_ratio": json.RawMessage(`0.95`)}},
		{GroupProxyFilter, override.Document{"max_request_body_size": json.RawMessage(`-1`)}},
		{GroupRuntime, override.Document{"no_such_field": json.RawMessage(`1`)}},
		{Group("nope"), override.Document{"x": json.RawMessage(`1`)}},
	}
	for _, b := range bad {
		if err := s.Patch(ctx, b.g, b.d); !errors.Is(err, override.ErrInvalid) {
			t.Errorf("%s %v 应被拒（ErrInvalid），得 %v", b.g, b.d, err)
		}
	}
	for _, g := range Groups {
		if d, _ := s.Detail(ctx, g); len(d.Overrides) != 0 {
			t.Errorf("被拒的提交不应留下自定义: %s %v", g, d.Overrides)
		}
	}
}

// 名单字段：增删按操作记录，出厂名单（来自 config.yaml）变化时用户的增删仍生效。
func TestProxyFilterListChanges(t *testing.T) {
	s, ctx := newStore(t)
	change, _ := json.Marshal(override.SetChange{On: override.Strings([]string{"ads.example"}), Off: override.Strings([]string{"cdn.example"})})
	if err := s.Patch(ctx, GroupProxyFilter, override.Document{"exclude_hosts": change}); err != nil {
		t.Fatal(err)
	}
	pf, _ := s.ProxyFilter(ctx)
	if !slices.Equal(pf.ExcludeHosts, []string{"ads.example"}) {
		t.Fatalf("生效排除主机: %v", pf.ExcludeHosts)
	}
	// 部署者在 yaml 里新增出厂排除主机：用户删掉的保持删除，新增的出厂项生效。
	cfg := config.AppConfig{}
	cfg.ApplyDefaults()
	cfg.Proxy.ExcludeHosts = []string{"cdn.example", "tracker.example"}
	s2 := New(s.table.Pool, s.cache, DefaultsFrom(cfg))
	pf2, _ := s2.ProxyFilter(ctx)
	if !slices.Equal(pf2.ExcludeHosts, []string{"tracker.example", "ads.example"}) {
		t.Fatalf("出厂名单变化后生效: %v", pf2.ExcludeHosts)
	}
}

// 进程 A 修改后广播失效，订阅了失效总线的进程 B 下次读到新值（runner/proxy 热生效的前提）。
func TestCrossProcessInvalidation(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	mr := miniredis.RunT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	newCache := func() *cachestore.Cache {
		c := cachestore.New(redis.NewClient(&redis.Options{Addr: mr.Addr()}), 0)
		go func() { _ = c.Subscribe(ctx) }()
		return c
	}
	a, b := New(pool, newCache(), testDefaults()), New(pool, newCache(), testDefaults())

	if rt, _ := b.Runtime(ctx); rt.StepLLMTimeoutSeconds != 300 {
		t.Fatalf("B 预热读: %+v", rt)
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(mr.PubSubChannels("*")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := a.Patch(ctx, GroupRuntime, override.Document{"step_llm_timeout_seconds": json.RawMessage(`120`)}); err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) {
		if rt, _ := b.Runtime(ctx); rt.StepLLMTimeoutSeconds == 120 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("B 未收到失效广播，仍读到旧值")
}
