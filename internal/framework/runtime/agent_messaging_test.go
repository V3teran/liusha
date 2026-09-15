package runtime

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────
//  测试用例
// ─────────────────────────────────────────────

// TestMessageBroker_RegisterAndUnregister 测试 Agent 注册和注销
func TestMessageBroker_RegisterAndUnregister(t *testing.T) {
	broker := NewMessageBroker()

	// 注册 Agent
	agent1, err := broker.Register("agent1", 10)
	require.NoError(t, err)
	assert.Equal(t, "agent1", agent1.AgentID())

	// 列出 Agent
	agents := broker.ListAgents()
	assert.Contains(t, agents, "agent1")

	// 重复注册应失败
	_, err = broker.Register("agent1", 10)
	assert.Error(t, err)

	// 注销 Agent
	err = broker.Unregister("agent1")
	require.NoError(t, err)

	// 再次列出应为空
	agents = broker.ListAgents()
	assert.NotContains(t, agents, "agent1")
}

// TestAgentMessaging_PointToPoint 测试点对点消息传递
func TestAgentMessaging_PointToPoint(t *testing.T) {
	broker := NewMessageBroker()
	ctx := context.Background()

	// 注册两个 Agent
	sender, err := broker.Register("sender", 10)
	require.NoError(t, err)
	defer sender.Close()

	receiver, err := broker.Register("receiver", 10)
	require.NoError(t, err)
	defer receiver.Close()

	// 发送消息
	payload := map[string]string{"action": "ping"}
	err = sender.Send(ctx, "receiver", MessageTypeEvent, payload)
	require.NoError(t, err)

	// 接收消息
	msg, err := receiver.Receive(ctx)
	require.NoError(t, err)

	assert.Equal(t, "sender", msg.From)
	assert.Equal(t, "receiver", msg.To)
	assert.Equal(t, MessageTypeEvent, msg.Type)

	var receivedPayload map[string]string
	err = json.Unmarshal(msg.Payload, &receivedPayload)
	require.NoError(t, err)
	assert.Equal(t, "ping", receivedPayload["action"])
}

// TestAgentMessaging_RequestResponse 测试请求-响应模式
func TestAgentMessaging_RequestResponse(t *testing.T) {
	broker := NewMessageBroker()
	ctx := context.Background()

	// 注册两个 Agent
	client, err := broker.Register("client", 10)
	require.NoError(t, err)
	defer client.Close()

	server, err := broker.Register("server", 10)
	require.NoError(t, err)
	defer server.Close()

	// 服务端处理请求
	go func() {
		msg, err := server.Receive(ctx)
		if err != nil {
			return
		}

		// 解析请求
		var req map[string]string
		_ = json.Unmarshal(msg.Payload, &req)

		// 回复响应
		response := map[string]string{"result": "pong"}
		_ = server.Reply(ctx, msg, response)
	}()

	// 客户端发送请求并等待响应
	request := map[string]string{"action": "ping"}
	response, err := client.Request(ctx, "server", request, 2*time.Second)
	require.NoError(t, err)

	assert.Equal(t, "server", response.From)
	assert.Equal(t, "client", response.To)
	assert.Equal(t, MessageTypeResponse, response.Type)

	var responsePayload map[string]string
	err = json.Unmarshal(response.Payload, &responsePayload)
	require.NoError(t, err)
	assert.Equal(t, "pong", responsePayload["result"])
}

// TestAgentMessaging_Broadcast 测试广播消息
func TestAgentMessaging_Broadcast(t *testing.T) {
	broker := NewMessageBroker()
	ctx := context.Background()

	// 注册 3 个 Agent
	broadcaster, err := broker.Register("broadcaster", 10)
	require.NoError(t, err)
	defer broadcaster.Close()

	listener1, err := broker.Register("listener1", 10)
	require.NoError(t, err)
	defer listener1.Close()

	listener2, err := broker.Register("listener2", 10)
	require.NoError(t, err)
	defer listener2.Close()

	// 广播消息
	payload := map[string]string{"event": "system_shutdown"}
	err = broadcaster.Broadcast(ctx, "system.shutdown", payload)
	require.NoError(t, err)

	// 两个监听者都应收到消息
	var wg sync.WaitGroup
	wg.Add(2)

	checkListener := func(listener AgentMessaging, name string) {
		defer wg.Done()

		ctx, cancel := context.WithTimeout(ctx, 1*time.Second)
		defer cancel()

		msg, err := listener.Receive(ctx)
		require.NoError(t, err, "listener %s 应收到消息", name)

		assert.Equal(t, "broadcaster", msg.From)
		assert.Equal(t, MessageTypeBroadcast, msg.Type)
		assert.Equal(t, "system.shutdown", msg.Subject)
	}

	go checkListener(listener1, "listener1")
	go checkListener(listener2, "listener2")

	wg.Wait()

	// 广播者自己不应收到消息（inbox 应为空）
	ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	_, err = broadcaster.Receive(ctx)
	assert.Error(t, err) // 应超时
}

// TestAgentMessaging_Subscribe 测试事件订阅
func TestAgentMessaging_Subscribe(t *testing.T) {
	broker := NewMessageBroker()
	ctx := context.Background()

	// 注册两个 Agent
	publisher, err := broker.Register("publisher", 10)
	require.NoError(t, err)
	defer publisher.Close()

	subscriber, err := broker.Register("subscriber", 10)
	require.NoError(t, err)
	defer subscriber.Close()

	// 订阅事件
	var receivedEvents []string
	var mu sync.Mutex

	handler := func(ctx context.Context, msg *AgentMessage) error {
		var payload map[string]string
		_ = json.Unmarshal(msg.Payload, &payload)

		mu.Lock()
		receivedEvents = append(receivedEvents, payload["data"])
		mu.Unlock()

		return nil
	}

	err = subscriber.Subscribe("data.update", handler)
	require.NoError(t, err)

	// 发布事件
	for i := 1; i <= 3; i++ {
		msg, _ := NewAgentMessage("publisher", "subscriber", MessageTypeEvent, map[string]string{
			"data": string(rune('A' + i - 1)),
		})
		msg.Subject = "data.update"

		err = publisher.SendMessage(ctx, msg)
		require.NoError(t, err)
	}

	// 触发接收（订阅处理器会异步执行）
	for i := 0; i < 3; i++ {
		_, err = subscriber.Receive(ctx)
		require.NoError(t, err)
	}

	// 等待异步处理完成
	time.Sleep(100 * time.Millisecond)

	// 验证接收到的事件
	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, receivedEvents, 3)
	assert.Contains(t, receivedEvents, "A")
	assert.Contains(t, receivedEvents, "B")
	assert.Contains(t, receivedEvents, "C")
}

// TestAgentMessaging_Timeout 测试超时场景
func TestAgentMessaging_Timeout(t *testing.T) {
	broker := NewMessageBroker()
	ctx := context.Background()

	// 注册两个 Agent
	client, err := broker.Register("client", 10)
	require.NoError(t, err)
	defer client.Close()

	server, err := broker.Register("server", 10)
	require.NoError(t, err)
	defer server.Close()

	// 服务端不响应（故意不处理请求）

	// 客户端请求应超时
	request := map[string]string{"action": "ping"}
	_, err = client.Request(ctx, "server", request, 100*time.Millisecond)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "超时")
}

// TestAgentMessaging_OfflineAgent 测试向离线 Agent 发送消息
func TestAgentMessaging_OfflineAgent(t *testing.T) {
	broker := NewMessageBroker()
	ctx := context.Background()

	// 注册并立即注销 Agent
	agent, err := broker.Register("agent1", 10)
	require.NoError(t, err)

	err = broker.Unregister("agent1")
	require.NoError(t, err)

	// 向离线 Agent 发送消息应失败
	sender, err := broker.Register("sender", 10)
	require.NoError(t, err)
	defer sender.Close()

	err = sender.Send(ctx, "agent1", MessageTypeEvent, map[string]string{"test": "data"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "不存在或已离线")

	// 原 agent 通道已关闭，Receive 应失败
	_, err = agent.Receive(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已关闭")
}

// TestAgentMessaging_FullQueue 测试消息队列满
func TestAgentMessaging_FullQueue(t *testing.T) {
	broker := NewMessageBroker()
	ctx := context.Background()

	// 注册 Agent，队列大小为 1
	sender, err := broker.Register("sender", 10)
	require.NoError(t, err)
	defer sender.Close()

	receiver, err := broker.Register("receiver", 1) // 只能缓冲 1 条消息
	require.NoError(t, err)
	defer receiver.Close()

	// 发送第一条消息（应成功）
	err = sender.Send(ctx, "receiver", MessageTypeEvent, map[string]string{"msg": "1"})
	require.NoError(t, err)

	// 发送第二条消息（队列满，应失败）
	err = sender.Send(ctx, "receiver", MessageTypeEvent, map[string]string{"msg": "2"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "队列已满")

	// 接收第一条消息后，队列应有空位
	_, err = receiver.Receive(ctx)
	require.NoError(t, err)

	// 再次发送应成功
	err = sender.Send(ctx, "receiver", MessageTypeEvent, map[string]string{"msg": "3"})
	require.NoError(t, err)
}

// TestAgentMessaging_ContextCancellation 测试上下文取消
func TestAgentMessaging_ContextCancellation(t *testing.T) {
	broker := NewMessageBroker()

	receiver, err := broker.Register("receiver", 10)
	require.NoError(t, err)
	defer receiver.Close()

	// 创建可取消的上下文
	ctx, cancel := context.WithCancel(context.Background())

	// 启动接收 goroutine
	done := make(chan error, 1)
	go func() {
		_, err := receiver.Receive(ctx)
		done <- err
	}()

	// 取消上下文
	time.Sleep(50 * time.Millisecond)
	cancel()

	// 应返回取消错误
	err = <-done
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

// TestMessageBroker_Shutdown 测试代理关闭
func TestMessageBroker_Shutdown(t *testing.T) {
	broker := NewMessageBroker()
	ctx := context.Background()

	// 注册多个 Agent
	for i := 1; i <= 3; i++ {
		_, err := broker.Register(string(rune('A'+i-1)), 10)
		require.NoError(t, err)
	}

	// 关闭代理
	err := broker.Shutdown(ctx)
	require.NoError(t, err)

	// 所有 Agent 应被清空
	agents := broker.ListAgents()
	assert.Empty(t, agents)

	// 无法注册新 Agent
	_, err = broker.Register("new", 10)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已关闭")
}

// TestAgentMessaging_ConcurrentSend 测试并发发送
func TestAgentMessaging_ConcurrentSend(t *testing.T) {
	broker := NewMessageBroker()
	ctx := context.Background()

	sender, err := broker.Register("sender", 10)
	require.NoError(t, err)
	defer sender.Close()

	receiver, err := broker.Register("receiver", 100) // 大队列
	require.NoError(t, err)
	defer receiver.Close()

	// 并发发送 100 条消息
	const numMessages = 100
	var wg sync.WaitGroup
	wg.Add(numMessages)

	for i := 0; i < numMessages; i++ {
		go func(id int) {
			defer wg.Done()
			_ = sender.Send(ctx, "receiver", MessageTypeEvent, map[string]int{"id": id})
		}(i)
	}

	wg.Wait()

	// 接收所有消息
	receivedIDs := make(map[int]bool)
	for i := 0; i < numMessages; i++ {
		msg, err := receiver.Receive(ctx)
		require.NoError(t, err)

		var payload map[string]int
		_ = json.Unmarshal(msg.Payload, &payload)
		receivedIDs[payload["id"]] = true
	}

	// 验证所有消息都收到了
	assert.Len(t, receivedIDs, numMessages)
}

// TestAgentMessaging_MultipleRequestResponse 测试多个并发请求-响应
func TestAgentMessaging_MultipleRequestResponse(t *testing.T) {
	broker := NewMessageBroker()
	ctx := context.Background()

	client, err := broker.Register("client", 100)
	require.NoError(t, err)
	defer client.Close()

	server, err := broker.Register("server", 100)
	require.NoError(t, err)
	defer server.Close()

	// 服务端处理请求
	go func() {
		for {
			msg, err := server.Receive(ctx)
			if err != nil {
				return
			}

			var req map[string]int
			_ = json.Unmarshal(msg.Payload, &req)

			// 回复：返回 ID 的两倍
			response := map[string]int{"result": req["id"] * 2}
			_ = server.Reply(ctx, msg, response)
		}
	}()

	// 并发发送多个请求
	const numRequests = 10
	var wg sync.WaitGroup
	wg.Add(numRequests)

	results := make([]int, numRequests)
	var mu sync.Mutex

	for i := 0; i < numRequests; i++ {
		go func(id int) {
			defer wg.Done()

			request := map[string]int{"id": id}
			response, err := client.Request(ctx, "server", request, 1*time.Second)
			require.NoError(t, err)

			var respPayload map[string]int
			_ = json.Unmarshal(response.Payload, &respPayload)

			mu.Lock()
			results[id] = respPayload["result"]
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	// 验证所有响应
	for i := 0; i < numRequests; i++ {
		assert.Equal(t, i*2, results[i])
	}
}
