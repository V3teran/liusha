package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// recordingProvider 在 mockLLMProvider 之上记下每次请求，供断言回传给 provider 的历史。
type recordingProvider struct {
	mockLLMProvider
	requests []llm.Request
}

func (p *recordingProvider) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	p.requests = append(p.requests, req)
	return p.mockLLMProvider.Complete(ctx, req)
}

// runWithToolCall 让模型先发起一次给定参数的工具调用、再给出答案，返回运行结果、
// 工具实际收到的参数（未执行为 nil）与 provider 收到的请求。
func runWithToolCall(t *testing.T, args string) (*ReActResult, json.RawMessage, []llm.Request) {
	t.Helper()
	rt := NewReActRuntime()
	var received json.RawMessage
	require.NoError(t, rt.RegisterTool(&mockTool{
		name: "calculator",
		handler: func(a json.RawMessage) (any, error) {
			received = a
			return map[string]int{"result": 4}, nil
		},
	}))
	provider := &recordingProvider{mockLLMProvider: mockLLMProvider{responses: []llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "calculator", Arguments: json.RawMessage(args)}}, FinishReason: "tool_use"},
		{Content: "完成", FinishReason: "stop"},
	}}}
	cfg := defaultTestConfig()
	cfg.Objective = "计算"
	cfg.LLMProvider = provider
	result, err := rt.Run(context.Background(), cfg)
	require.NoError(t, err)
	return result, received, provider.requests
}

// assertHistoryArgsValid 断言回传给 provider 的每个工具调用参数都是合法 JSON——
// 非法参数原样回传会让下一轮请求体本身不是合法 JSON（provider 以 400 拒绝整次调用）。
func assertHistoryArgsValid(t *testing.T, reqs []llm.Request) {
	t.Helper()
	for i, req := range reqs {
		for _, m := range req.Messages {
			for _, tc := range m.ToolCalls {
				assert.Truef(t, json.Valid(tc.Arguments), "第 %d 次请求回传了非法工具参数 %q", i+1, tc.Arguments)
			}
		}
	}
}

func TestReActRuntime_RepairableToolArgs(t *testing.T) {
	result, received, reqs := runWithToolCall(t, `{"expression": "2+2",}`)

	assert.JSONEq(t, `{"expression": "2+2"}`, string(received), "可修复的参数应修复后执行")
	require.GreaterOrEqual(t, len(reqs), 2)
	assertHistoryArgsValid(t, reqs)
	assert.Equal(t, ReActStatusSuccess, result.Status)
}

func TestReActRuntime_MalformedToolArgs(t *testing.T) {
	result, received, reqs := runWithToolCall(t, `{"expression": "2+`)

	assert.Nil(t, received, "无法修复的参数不应执行工具")
	require.GreaterOrEqual(t, len(reqs), 2)
	assertHistoryArgsValid(t, reqs)
	require.Len(t, result.Trace[0].Observations, 1)
	assert.True(t, strings.Contains(result.Trace[0].Observations[0], "不是合法 JSON"),
		"应把参数错误回给模型重试，得 %q", result.Trace[0].Observations[0])
}
