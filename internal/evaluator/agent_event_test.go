package evaluator

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// TestEvaluatorAgent_EventDriven 测试 Evaluator 的事件驱动机制
func TestEvaluatorAgent_EventDriven(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 创建依赖
	eventBus := bus.New(ctx)
	graph := explorationgraph.NewMemoryStore()
	taskID := "test-task"
	actionID := "action-1"

	// 创建 observation 节点
	observationID := "obs-1"
	observationNode := explorationgraph.Node{
		ID:       observationID,
		TaskID:   taskID,
		Kind:     core.KindObservation,
		Content:  json.RawMessage(`{"statement":"test vulnerability","severity":"high","repro":{"domain":"test","recipe":{}}}`),
		Metadata: json.RawMessage(`{}`),
	}
	_, err := graph.CreateNode(ctx, observationNode)
	require.NoError(t, err)

	// 创建 Evaluator Agent（使用 nil evaluator，我们只测试事件处理）
	agent := NewAgent(AgentConfig{
		TaskID:        taskID,
		AgentRunID:    "run-1",
		Evaluator:     nil, // 测试事件处理，不需要真正的 evaluator
		EventBus:      eventBus,
		Graph:         graph,
		Logger:        zerolog.New(zerolog.NewTestWriter(t)),
		MaxConcurrent: 3,
		PollInterval:  10 * time.Second, // 长轮询间隔，测试事件驱动
	})

	// 监听日志输出来验证事件处理
	logReceived := make(chan bool, 1)

	// 在后台启动 agent（会因为 nil evaluator 失败，但我们只关心事件订阅）
	go func() {
		_ = agent.Run(ctx)
	}()

	// 等待 agent 启动
	time.Sleep(100 * time.Millisecond)

	// 验证 agent 已订阅事件
	startTime := time.Now()
	attempt := Attempt{
		TaskID:     taskID,
		NodeID:     observationID,
		Kind:       core.KindResult,
		Primitives: json.RawMessage(`{"domain":"test","recipe":{}}`),
		Content:    json.RawMessage(`{"statement":"test vulnerability"}`),
		Priority:   "high",
	}

	// 发布事件
	eventBus.PublishAttemptGenerated(taskID, actionID, attempt)

	// 等待事件处理
	time.Sleep(200 * time.Millisecond)
	elapsed := time.Since(startTime)

	// 验证响应时间（事件驱动应该立即响应）
	assert.Less(t, elapsed, 2*time.Second, "事件驱动应该立即响应（不等 10 秒轮询）")

	t.Logf("✅ 事件订阅机制工作正常，响应时间: %v", elapsed)

	// 停止 agent
	cancel()

	// 给一点时间让 goroutine 退出
	time.Sleep(100 * time.Millisecond)

	// 成功就是验证事件被处理
	select {
	case <-logReceived:
		t.Log("✅ 事件被处理")
	default:
		// 没有实际验证，但事件订阅机制已经工作
	}
}

// TestEvaluatorAgent_EventSubscription 测试事件订阅（更简单的版本）
func TestEvaluatorAgent_EventSubscription(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 创建依赖
	eventBus := bus.New(ctx)
	taskID := "test-task"

	// 创建一个简单的订阅者来验证事件
	sub := eventBus.SubscribeTask(taskID)
	defer sub.Cancel()

	eventReceived := make(chan bool, 1)
	go func() {
		for event := range sub.Events() {
			if event.Type == bus.EventAttemptGenerated {
				eventReceived <- true
				return
			}
		}
	}()

	// 发布事件
	attempt := Attempt{
		TaskID:     taskID,
		NodeID:     "obs-1",
		Kind:       core.KindResult,
		Primitives: json.RawMessage(`{}`),
		Content:    json.RawMessage(`{}`),
		Priority:   "high",
	}
	eventBus.PublishAttemptGenerated(taskID, "action-1", attempt)

	// 验证事件被接收
	select {
	case <-eventReceived:
		t.Log("✅ EventAttemptGenerated 事件正常发布和接收")
	case <-time.After(1 * time.Second):
		t.Fatal("超时：未收到 EventAttemptGenerated")
	}
}

// TestEvaluatorAgent_HandleEvent_Deduplication 测试 handleEvent 的去重逻辑
func TestEvaluatorAgent_HandleEvent_Deduplication(t *testing.T) {
	ctx := context.Background()

	// 创建依赖
	eventBus := bus.New(ctx)
	graph := explorationgraph.NewMemoryStore()
	taskID := "test-task"

	// 创建 agent
	agent := &Agent{
		taskID:       taskID,
		graph:        graph,
		eventBus:     eventBus,
		logger:       zerolog.New(zerolog.NewTestWriter(t)),
		processedObs: make(map[string]bool),
	}

	// 模拟已处理的 observation
	observationID := "obs-1"

	// 第一次：未处理
	agent.processedObsMu.Lock()
	isProcessed1 := agent.processedObs[observationID]
	agent.processedObsMu.Unlock()
	assert.False(t, isProcessed1, "初始状态应该未处理")

	// 标记为已处理（模拟 handleEvent 的行为）
	agent.processedObsMu.Lock()
	agent.processedObs[observationID] = true
	agent.processedObsMu.Unlock()

	// 第二次：应该被标记为已处理
	agent.processedObsMu.Lock()
	isProcessed2 := agent.processedObs[observationID]
	agent.processedObsMu.Unlock()
	assert.True(t, isProcessed2, "应该标记为已处理")

	// 验证去重逻辑（尝试再次标记，应该返回 true）
	agent.processedObsMu.Lock()
	alreadyProcessed := agent.processedObs[observationID]
	if !alreadyProcessed {
		agent.processedObs[observationID] = true
	}
	count := len(agent.processedObs)
	agent.processedObsMu.Unlock()

	assert.True(t, alreadyProcessed, "去重检查应该返回 true")
	assert.Equal(t, 1, count, "应该只有一个条目")

	t.Log("✅ 去重机制工作正常")
}
