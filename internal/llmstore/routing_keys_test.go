package llmstore

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/llmconfig"
)

// 模型配置变更的失效广播到达后 Router 丢掉旧 Provider：provider 与角色路由的键会触发，
// 检索模型的密钥不会（它只管知识库的检索方式，不影响对话模型的调用）。
func TestOnRoutingChange(t *testing.T) {
	t.Run("影响对话模型调用的键", func(t *testing.T) {
		for _, keys := range [][]string{
			{"llmstore:provider:code:some-uuid"},
			{"llmstore:providers:list:all"},
			{"llmstore:providers:list:enabled"},
			{"llmstore:routing"},
			{"llmstore:routes:list"},
			{"llmstore:retrieval", "llmstore:routing"}, // 一批里只要有一个就算
		} {
			called := 0
			OnRoutingChange(func() { called++ })(nil, keys)
			if called != 1 {
				t.Errorf("%v 应让 Router 清一次缓存，实际 %d 次", keys, called)
			}
		}
	})
	t.Run("不影响对话模型调用的键", func(t *testing.T) {
		for _, keys := range [][]string{
			nil,
			{},
			{"llmstore:retrieval"},
			{"llmstore:retriev"},        // 前缀相近但不是 provider 的键
			{"llmstore:provider-codes"}, // 以 llmstore:prov 开头，但不是 provider 的单条键
			{"llmstore:providers"},      // 列表键之外的同前缀键
			{"settingstore:override:runtime"},
		} {
			called := 0
			OnRoutingChange(func() { called++ })(nil, keys)
			if called != 0 {
				t.Errorf("%v 不该让 Router 清缓存，实际 %d 次", keys, called)
			}
		}
	})
}

// 端到端：在页面上保存模型配置（走 Store 的真实写方法）→ 失效经真实的 Redis 广播 → 另一个
// 进程里挂在总线上的回调被触发、Router 丢掉旧 Provider。检索模型密钥的保存不触发它。
func TestOnRoutingChange_RoutedThroughRealInvalidationBus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	// 写方：保存模型配置的进程（api）。
	writer := newWithStore(&fakeLLM{}, cachestore.New(rdb, 0))
	// 读方：另一个进程（runner）——自己的 Cache 实例，订阅同一条总线。
	reader := cachestore.New(rdb, 0)
	var dropped atomic.Int32
	probes := make(chan string, 16)
	reader.OnInvalidate(func(_ context.Context, keys []string) {
		for _, key := range keys {
			if strings.HasPrefix(key, "test:probe:") {
				probes <- key
			}
		}
	})
	reader.OnInvalidate(OnRoutingChange(func() { dropped.Add(1) }))
	go func() { _ = reader.Subscribe(ctx) }()

	// flush 发一枚探针并等它到达。同一频道上的消息按序送达：它到了，排在它前面的失效就都
	// 已被处理，回调的计数随之定格。订阅在后台建立，而 pub/sub 不补发错过的消息：没收到
	// 就重发；上一次 flush 重发的探针若迟到，序号更小，跳过。
	probe := cachestore.New(rdb, 0)
	seq := 0
	flush := func() {
		t.Helper()
		seq++
		key := fmt.Sprintf("test:probe:%d", seq)
		deadline := time.Now().Add(3 * time.Second)
		resend := time.NewTimer(50 * time.Millisecond)
		defer resend.Stop()
		if err := probe.Invalidate(ctx, key); err != nil {
			t.Fatal(err)
		}
		for {
			select {
			case got := <-probes:
				if got == key {
					return
				}
			case <-resend.C:
				if time.Now().After(deadline) {
					t.Fatal("失效广播 3 秒内没有到达另一个进程")
				}
				if err := probe.Invalidate(ctx, key); err != nil {
					t.Fatal(err)
				}
				resend.Reset(50 * time.Millisecond)
			}
		}
	}
	expectDropped := func(want int32, what string) {
		t.Helper()
		if got := dropped.Load(); got != want {
			t.Fatalf("%s：Router 应已丢掉 %d 次旧 Provider，实际 %d 次", what, want, got)
		}
	}
	flush() // 订阅已建立

	// 保存检索模型的密钥：不影响对话模型调用，Router 不动。
	if _, err := writer.SetRetrievalModel(ctx, llmconfig.RetrievalParams{BaseURL: "http://r.test", EmbeddingModel: "e"}); err != nil {
		t.Fatal(err)
	}
	flush()
	expectDropped(0, "保存检索模型密钥后")

	// 保存 provider：另一个进程里的 Router 立即丢掉旧 Provider。
	if _, err := writer.CreateProvider(ctx, llmconfig.ProviderParams{Code: "p1"}); err != nil {
		t.Fatal(err)
	}
	flush()
	expectDropped(1, "保存 provider 后")

	// 改角色路由：同样。
	if _, err := writer.UpsertRoleRoute(ctx, "simple", "p1"); err != nil {
		t.Fatal(err)
	}
	flush()
	expectDropped(2, "改角色路由后")
}
