package convstream

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// subscribeBuffer 是订阅事件 channel 的缓冲，吸收 SSE 客户端短暂慢读，避免 redis 推送阻塞。
const subscribeBuffer = 64

// Subscription 是对一个会话 channel 的订阅（api SSE handler 侧）。
// 内部 goroutine 把 redis 消息泵成 []byte channel；用完必须 Close（停泵 + 释放 redis 连接）。
type Subscription struct {
	pubsub *redis.PubSub
	out    chan []byte
	done   chan struct{}
}

// Subscribe 订阅某会话的事件 channel。调用方用 Events() 取 payload，结束调 Close。
func Subscribe(ctx context.Context, rdb *redis.Client, conversationID string) *Subscription {
	ps := rdb.Subscribe(ctx, Channel(conversationID))
	s := &Subscription{pubsub: ps, out: make(chan []byte, subscribeBuffer), done: make(chan struct{})}
	go s.pump()
	return s
}

// pump 把 redis 消息转成 []byte 推到 out。Close 或 pubsub channel 关闭后退出并 close(out)。
// 消费方（SSE handler）可能经 ctx 放弃而不再读 out——发送必须监听 done，
// 否则 pump 卡在阻塞发送上，Close 也救不回来（goroutine 泄漏）。
func (s *Subscription) pump() {
	defer close(s.out)
	ch := s.pubsub.Channel()
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			select {
			case s.out <- []byte(msg.Payload):
			case <-s.done:
				return
			}
		case <-s.done:
			return
		}
	}
}

// Events 返回 payload channel（Close 后会被关闭，range 自然结束）。
func (s *Subscription) Events() <-chan []byte { return s.out }

// Close 停订阅、释放 redis 连接，并让 pump 退出 / Events channel 关闭。
func (s *Subscription) Close() error {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	return s.pubsub.Close()
}
