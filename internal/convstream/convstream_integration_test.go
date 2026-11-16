//go:build integration

package convstream_test

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/V3teran/liusha/internal/containertest"
	"github.com/V3teran/liusha/internal/convstream"
)

// 用真实 Redis（testcontainers 拉起）测 publish→subscribe 往返：Pub/Sub 的订阅就绪、
// 投递顺序是 Redis 的行为，内存替身不足以覆盖。
func TestPubSub_RoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	c, err := containertest.Start(ctx, func(ctx context.Context) (*tcredis.RedisContainer, error) { return tcredis.Run(ctx, "redis:8") })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	url, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	const conversationID = "test-conv-roundtrip"
	sub := convstream.Subscribe(ctx, rdb, conversationID)
	defer sub.Close()
	// 等订阅就绪（go-redis 异步建立订阅，过早 publish 会丢）。
	time.Sleep(200 * time.Millisecond)

	pub := convstream.NewPublisher(rdb)
	if err := pub.Publish(ctx, conversationID, []byte("事件1")); err != nil {
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
		t.Errorf("空 conversationID 应静默跳过，得错误 %v", err)
	}
}
