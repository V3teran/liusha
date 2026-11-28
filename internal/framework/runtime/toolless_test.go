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

// 本次运行没有注册任何工具，模型却发起了工具调用：回一句「没有提供工具」之后结束循环、请它
// 收尾。把失败回给模型再等它重试换不来东西——实测规划者被追问线索交代时连着两次去读探索图。
func TestRun_ToolCallWithoutToolsEndsInWrapUp(t *testing.T) {
	toolCall := llm.Response{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "graph_read", Arguments: json.RawMessage(`{}`)}}}
	provider := &limitedProvider{recordingProvider: &recordingProvider{mockLLMProvider: mockLLMProvider{responses: []llm.Response{toolCall, {Content: planJSON}}}}, limit: 8192}
	config := structuredConfig(provider)

	result, err := NewReActRuntime().Run(context.Background(), config)
	require.NoError(t, err)
	require.Len(t, provider.requests, 2, "工具调用的回执之后就是收尾")
	wrapUp := provider.requests[1]
	messages := wrapUp.Messages
	assert.Equal(t, llm.RoleAssistant, messages[len(messages)-3].Role)
	assert.Equal(t, llm.RoleTool, messages[len(messages)-2].Role, "工具调用要有回执，历史才是合法的往来")
	assert.Equal(t, "工具 graph_read 未执行：本次运行没有提供工具。", messages[len(messages)-2].Content)
	assert.True(t, strings.HasPrefix(lastUserMessage(wrapUp), whyNoFinalAnswer))
	assert.Equal(t, 8192, wrapUp.MaxTokens, "这是一次收尾：按 provider 的上限请求预算，不是循环里的再一次调用")
	assert.Equal(t, planJSON, result.FinalAnswer)
	assert.Nil(t, result.InternalError)
}

// 注册了工具的运行里模型调了不存在的工具：失败照常回给模型，它可以改调真正的工具——这
// 种失败是可恢复的，与「本次运行根本没有工具」不同。
func TestRun_UnregisteredToolInTooledRunIsBouncedBack(t *testing.T) {
	ghost := llm.Response{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "ghost", Arguments: json.RawMessage(`{}`)}}}
	provider := &recordingProvider{mockLLMProvider: mockLLMProvider{responses: []llm.Response{ghost, {Content: planJSON}}}}
	config := structuredConfig(provider)
	react := NewReActRuntime()
	require.NoError(t, react.RegisterTool(&mockTool{name: "probe", description: "探测", handler: func(json.RawMessage) (any, error) { return "ok", nil }}))

	result, err := react.Run(context.Background(), config)
	require.NoError(t, err)
	require.Len(t, provider.requests, 2, "循环继续，模型的下一次回复就是终稿")
	messages := provider.requests[1].Messages
	assert.Equal(t, llm.RoleTool, messages[len(messages)-1].Role, "失败作为工具结果回给模型，循环继续")
	assert.Equal(t, "工具执行失败: 工具 ghost 未注册", messages[len(messages)-1].Content)
	assert.Equal(t, planJSON, result.FinalAnswer)
}

// 收尾调用自己的输出也被上限截断：验收取不出完整的输出，它是已知没写完的，不当终稿交付——
// 放大输出预算再要一次，并如实说明上一次是被截断的。
func TestRun_TruncatedRestatementIsAskedAgainWithBiggerBudget(t *testing.T) {
	provider := &limitedProvider{recordingProvider: &recordingProvider{mockLLMProvider: mockLLMProvider{responses: []llm.Response{
		{Content: truncatedPlan, FinishReason: "length", Usage: llm.Usage{OutTokens: 1500}},
		{Content: truncatedPlan, FinishReason: "length", Usage: llm.Usage{OutTokens: 1500}},
		{Content: planJSON, FinishReason: "stop", Usage: llm.Usage{OutTokens: 900}},
	}}}, limit: 16384}
	config := structuredConfig(provider)
	config.MaxTokens = 1500

	result, err := NewReActRuntime().Run(context.Background(), config)
	require.NoError(t, err)
	require.Len(t, provider.requests, 3)
	assert.Equal(t, 1500, provider.requests[1].MaxTokens, "收尾先按调用方的输出预算请求")
	assert.Equal(t, 3000, provider.requests[2].MaxTokens, "收尾的输出又被截断：预算放大一倍再要一次")
	assert.Contains(t, lastUserMessage(provider.requests[2]), "上一条输出达到输出上限被截断，没有写完", "重试如实说明上一次是被截断的")
	assert.Equal(t, planJSON, result.FinalAnswer)
	assert.Equal(t, 3900, result.Usage.OutTokens, "三次调用的用量都要计入")
	assert.Nil(t, result.InternalError)
}

// 收尾的输出虽被截断、但验收取得到完整的输出（砍掉的只是 JSON 之后的说明）：照常交付。
func TestRun_TruncatedRestatementThatPassesCheckStands(t *testing.T) {
	restated := planJSON + "\n\n补充：以上动作按先后执行，"
	provider := &recordingProvider{mockLLMProvider: mockLLMProvider{responses: []llm.Response{
		{Content: truncatedPlan, FinishReason: "length", Usage: llm.Usage{OutTokens: 4096}},
		{Content: restated, FinishReason: "length", Usage: llm.Usage{OutTokens: 4096}},
	}}}

	result, err := NewReActRuntime().Run(context.Background(), structuredConfig(provider))
	require.NoError(t, err)
	require.Len(t, provider.requests, 2, "验收通过就不再要")
	assert.Equal(t, restated, result.FinalAnswer)
}
