//go:build integration

package scanstream_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/V3teran/liusha/internal/scanstream"
)

// 用 dev redis 测 publish→subscribe 往返。需 LIUSHA_REDIS_ADDR（缺省 localhost:6379）。
//
//	LIUSHA_REDIS_ADDR=localhost:6379 go test -tags integration ./internal/scanstream/
func TestPubSub_RoundTrip(t *testing.T) {
	addr := os.Getenv("LIUSHA_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("redis 不可达（%s），跳过：%v", addr, err)
	}

	const convID = "test-conv-roundtrip"
	sub := scanstream.Subscribe(ctx, rdb, convID)
	defer sub.Close()
	// 等订阅就绪（go-redis 异步建立订阅，过早 publish 会丢）。
	time.Sleep(200 * time.Millisecond)

	pub := scanstream.NewPublisher(rdb)
	if err := pub.Publish(ctx, convID, []byte("事件1")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case got := <-sub.Events():
		if string(got) != "事件1" {
			t.Errorf("收到 %q，期望 事件1", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("3s 未收到事件")
	}

	// 空 conversationID 静默跳过（不报错）。
	if err := pub.Publish(ctx, "", []byte("x")); err != nil {
		t.Errorf("空 convID 应静默跳过，得错误 %v", err)
	}
}
