package runtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
)

// mockLLMProvider 是测试用的 LLM 提供者
type mockLLMProvider struct {
	responses []llm.Response
	callIndex int
}

func (m *mockLLMProvider) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	if m.callIndex >= len(m.responses) {
		return m.responses[len(m.responses)-1], nil
	}
	resp := m.responses[m.callIndex]
	m.callIndex++
	return resp, nil
}

func (m *mockLLMProvider) Stream(ctx context.Context, req llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, nil
}

func (m *mockLLMProvider) CountTokens(ctx context.Context, req llm.Request) (int, error) {
	return 0, nil
}

func (m *mockLLMProvider) ModelID() string {
	return "test-model"
}

func (m *mockLLMProvider) ProviderID() string {
	return "test-provider"
}

// mockTool 是测试用的工具
type mockTool struct {
	name        string
	description string
	handler     func(args json.RawMessage) (any, error)
}

func (t *mockTool) Name() string {
	return t.name
}

func (t *mockTool) Description() string {
	return t.description
}

func (t *mockTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

func (t *mockTool) Execute(ctx context.Context, input core.ToolInput) (core.ToolOutput, error) {
	result, err := t.handler(input.Arguments)
	if err != nil {
		return core.ToolOutput{Error: err.Error()}, err
	}
	resultJSON, _ := json.Marshal(result)
	return core.ToolOutput{Result: resultJSON}, nil
}

// TestReActRuntime_SimpleQuestion 测试简单问答（无工具调用）
func TestReActRuntime_SimpleQuestion(t *testing.T) {
	runtime := NewReActRuntime()

	mockProvider := &mockLLMProvider{
		responses: []llm.Response{
			{
				Content:      "2 + 2 = 4",
				FinishReason: "stop",
			},
		},
	}

	config := DefaultReActConfig()
	config.Objective = "计算 2 + 2"
	config.LLMProvider = mockProvider
	config.ModelID = "test-model"

	result, err := runtime.Run(context.Background(), config)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, ReActStatusSuccess, result.Status)
	assert.Equal(t, "2 + 2 = 4", result.FinalAnswer)
	assert.Equal(t, 1, result.Iterations)
	assert.Equal(t, 1, len(result.Trace))
}

// TestReActRuntime_ToolCalling 测试工具调用
func TestReActRuntime_ToolCalling(t *testing.T) {
	runtime := NewReActRuntime()

	// 注册计算器工具
	calculator := &mockTool{
		name:        "calculator",
		description: "执行数学计算",
		handler: func(args json.RawMessage) (any, error) {
			// 解析参数
			var params struct {
				Expression string `json:"expression"`
			}
			json.Unmarshal(args, &params)

			// 简单计算
			if params.Expression == "2+2" {
				return map[string]int{"result": 4}, nil
			}
			return nil, nil
		},
	}

	err := runtime.RegisterTool(calculator)
	require.NoError(t, err)

	// 模拟 LLM 响应：第一轮调用工具，第二轮给出答案
	mockProvider := &mockLLMProvider{
		responses: []llm.Response{
			// 第一轮：LLM 决定调用计算器
			{
				Content: "我需要使用计算器来计算 2+2",
				ToolCalls: []llm.ToolCall{
					{
						ID:        "call_1",
						Name:      "calculator",
						Arguments: json.RawMessage(`{"expression": "2+2"}`),
					},
				},
				FinishReason: "tool_use",
			},
			// 第二轮：LLM 根据工具结果给出答案
			{
				Content:      "根据计算器的结果，2+2=4",
				FinishReason: "stop",
			},
		},
	}

	config := DefaultReActConfig()
	config.Objective = "计算 2+2"
	config.LLMProvider = mockProvider
	config.ModelID = "test-model"

	result, err := runtime.Run(context.Background(), config)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, ReActStatusSuccess, result.Status)
	assert.Equal(t, "根据计算器的结果，2+2=4", result.FinalAnswer)
	assert.Equal(t, 2, result.Iterations)

	// 验证执行轨迹
	assert.Equal(t, 2, len(result.Trace))
	assert.Equal(t, 1, len(result.Trace[0].Actions))
	assert.Equal(t, "calculator", result.Trace[0].Actions[0].ToolCall.Name)
	assert.Equal(t, 1, len(result.Trace[0].Observations))
}

// TestReActRuntime_MaxIterations 测试最大迭代次数限制
func TestReActRuntime_MaxIterations(t *testing.T) {
	runtime := NewReActRuntime()

	// 模拟 LLM 一直调用工具（无限循环）
	mockProvider := &mockLLMProvider{
		responses: []llm.Response{
			{
				Content: "继续思考...",
				ToolCalls: []llm.ToolCall{
					{
						ID:        "call_1",
						Name:      "think",
						Arguments: json.RawMessage(`{}`),
					},
				},
				FinishReason: "tool_use",
			},
		},
	}

	// 注册一个无用的工具
	thinkTool := &mockTool{
		name:        "think",
		description: "思考",
		handler: func(args json.RawMessage) (any, error) {
			return "thinking...", nil
		},
	}
	runtime.RegisterTool(thinkTool)

	config := DefaultReActConfig()
	config.Objective = "无解问题"
	config.LLMProvider = mockProvider
	config.ModelID = "test-model"
	config.MaxIterations = 3

	result, err := runtime.Run(context.Background(), config)
	require.NoError(t, err)

	assert.Equal(t, ReActStatusMaxIterations, result.Status)
	assert.Equal(t, 3, result.Iterations)
	assert.Equal(t, 3, len(result.Trace))
}

// TestReActRuntime_EarlyStopCondition 测试提前终止条件
func TestReActRuntime_EarlyStopCondition(t *testing.T) {
	runtime := NewReActRuntime()

	mockProvider := &mockLLMProvider{
		responses: []llm.Response{
			{
				Content:      "最终答案：42",
				FinishReason: "stop",
			},
		},
	}

	config := DefaultReActConfig()
	config.Objective = "终极问题的答案"
	config.LLMProvider = mockProvider
	config.ModelID = "test-model"
	config.EarlyStopCondition = func(thought string) bool {
		// 包含"最终答案"时提前终止
		return len(thought) > 0 && thought[:4] == "最终答案"
	}

	result, err := runtime.Run(context.Background(), config)
	require.NoError(t, err)

	assert.Equal(t, ReActStatusSuccess, result.Status)
	assert.Equal(t, "最终答案：42", result.FinalAnswer)
	assert.Equal(t, 1, result.Iterations)
}

// TestReActRuntime_MessageHistory 测试消息历史保留
func TestReActRuntime_MessageHistory(t *testing.T) {
	runtime := NewReActRuntime()

	mockProvider := &mockLLMProvider{
		responses: []llm.Response{
			{
				Content:      "回答1",
				FinishReason: "stop",
			},
		},
	}

	config := DefaultReActConfig()
	config.LLMProvider = mockProvider
	config.ModelID = "test-model"

	// 第一轮对话
	config.Objective = "问题1"
	result1, err := runtime.Run(context.Background(), config)
	require.NoError(t, err)

	// 验证历史被保留
	history := runtime.GetMessageHistory()
	assert.Greater(t, len(history), 0)

	// 第二轮对话（应该包含第一轮的历史）
	mockProvider.callIndex = 0
	mockProvider.responses[0].Content = "回答2"
	config.Objective = "问题2"

	result2, err := runtime.Run(context.Background(), config)
	require.NoError(t, err)

	// 验证第二轮包含第一轮的历史
	assert.Greater(t, len(result2.MessageHistory), len(result1.MessageHistory))
}

// TestReActRuntime_ContextCancellation 测试上下文取消
func TestReActRuntime_ContextCancellation(t *testing.T) {
	runtime := NewReActRuntime()

	mockProvider := &mockLLMProvider{
		responses: []llm.Response{
			{
				Content: "思考中...",
				ToolCalls: []llm.ToolCall{
					{
						ID:        "call_1",
						Name:      "slow_tool",
						Arguments: json.RawMessage(`{}`),
					},
				},
				FinishReason: "tool_use",
			},
		},
	}

	// 注册一个慢工具
	slowTool := &mockTool{
		name:        "slow_tool",
		description: "慢工具",
		handler: func(args json.RawMessage) (any, error) {
			time.Sleep(1 * time.Second)
			return "done", nil
		},
	}
	runtime.RegisterTool(slowTool)

	config := DefaultReActConfig()
	config.Objective = "测试取消"
	config.LLMProvider = mockProvider
	config.ModelID = "test-model"

	// 创建可取消的上下文
	ctx, cancel := context.WithCancel(context.Background())

	// 100ms 后取消
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	result, err := runtime.Run(ctx, config)
	assert.Error(t, err)
	assert.Equal(t, ReActStatusCancelled, result.Status)
}

// TestReActRuntime_Callbacks 测试回调函数
func TestReActRuntime_Callbacks(t *testing.T) {
	runtime := NewReActRuntime()

	mockProvider := &mockLLMProvider{
		responses: []llm.Response{
			{
				Content: "思考：需要调用工具",
				ToolCalls: []llm.ToolCall{
					{
						ID:        "call_1",
						Name:      "test_tool",
						Arguments: json.RawMessage(`{}`),
					},
				},
				FinishReason: "tool_use",
			},
			{
				Content:      "最终答案",
				FinishReason: "stop",
			},
		},
	}

	testTool := &mockTool{
		name:        "test_tool",
		description: "测试工具",
		handler: func(args json.RawMessage) (any, error) {
			return "tool_result", nil
		},
	}
	runtime.RegisterTool(testTool)

	// 记录回调
	var thoughts []string
	var actions []*Action
	var observations []string
	var iterations []int

	config := DefaultReActConfig()
	config.Objective = "测试回调"
	config.LLMProvider = mockProvider
	config.ModelID = "test-model"
	config.OnThought = func(thought string) {
		thoughts = append(thoughts, thought)
	}
	config.OnAction = func(action *Action) {
		actions = append(actions, action)
	}
	config.OnObservation = func(observation string) {
		observations = append(observations, observation)
	}
	config.OnIteration = func(iteration int, status IterationStatus) {
		iterations = append(iterations, iteration)
	}

	result, err := runtime.Run(context.Background(), config)
	require.NoError(t, err)

	// 验证回调被调用
	assert.Equal(t, 2, len(thoughts))
	assert.Equal(t, 1, len(actions))
	assert.Equal(t, 1, len(observations))
	assert.Equal(t, 2, len(iterations))
	assert.Equal(t, ReActStatusSuccess, result.Status)
}
