package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// truncatedPlan 是被输出上限砍断的规划终稿：JSON 停在半截。
const truncatedPlan = `{"should_continue":true,"reasoning":"还没写完`

// 终稿被输出上限截断（结束原因写明）：JSON 在半截断掉。收尾请求如实说明是截断，而不是只说
// 「取不出 JSON」——那会让模型以同样的篇幅再写一遍、再被截断一次。
func TestRun_TruncatedFinalDraftIsRestatedAsTruncated(t *testing.T) {
	provider := &recordingProvider{mockLLMProvider: mockLLMProvider{responses: []llm.Response{
		{Content: truncatedPlan, FinishReason: "max_tokens", Usage: llm.Usage{OutTokens: 4096}},
		{Content: planJSON, FinishReason: "end_turn"},
	}}}
	config := structuredConfig(provider)

	result, err := NewReActRuntime().Run(context.Background(), config)
	require.NoError(t, err)
	require.Len(t, provider.requests, 2)
	ask := lastUserMessage(provider.requests[1])
	assert.True(t, strings.HasPrefix(ask, "上一版输出达到输出上限（4096 token）被截断，没有写完。"), ask)
	assert.NotContains(t, ask, "没通过验收", "原因是截断，取不出 JSON 只是它的结果")
	messages := provider.requests[1].Messages
	assert.Equal(t, truncatedPlan, messages[len(messages)-2].Content, "模型看得到被截断的那一版")
	assert.Contains(t, ask, "契约", "收尾请求照常重申输出契约")
	assert.Equal(t, planJSON, result.FinalAnswer)
	assert.Nil(t, result.InternalError)
}

// JSON 写完了、被砍掉的只是 JSON 之后的说明：验收取得到，终稿原样交付，不多花一次调用。
func TestRun_TruncatedDraftThatPassesCheckStands(t *testing.T) {
	final := planJSON + "\n\n补充：以上动作按先后执行，"
	provider := &recordingProvider{mockLLMProvider: mockLLMProvider{responses: []llm.Response{
		{Content: final, FinishReason: "length", Usage: llm.Usage{OutTokens: 4096}},
	}}}

	result, err := NewReActRuntime().Run(context.Background(), structuredConfig(provider))
	require.NoError(t, err)
	require.Len(t, provider.requests, 1, "验收通过就不该重述")
	assert.Equal(t, final, result.FinalAnswer)
}

// 没有输出契约的运行终稿只是叙述（结论经工具发出）：被截断的非空叙述照样交付，与「写下
// 什么就是终稿」同一口径；只有空终稿才请模型补。
func TestRun_TruncatedProseWithoutOutputFormatStands(t *testing.T) {
	provider := &recordingProvider{mockLLMProvider: mockLLMProvider{responses: []llm.Response{
		{Content: "本轮态势评估如下，无需", FinishReason: "max_tokens"},
	}}}
	config := &ReActConfig{Objective: "评估", LLMProvider: provider, MaxIterations: 5}

	result, err := NewReActRuntime().Run(context.Background(), config)
	require.NoError(t, err)
	require.Len(t, provider.requests, 1)
	assert.Equal(t, "本轮态势评估如下，无需", result.FinalAnswer)
}

// 正文为空、结束原因写明截断：思考吃满了输出预算。同预算再生成一次是确定性失败（实测
// 第二次多半还是被截断），不在循环里纠偏，直接收尾——收尾按放大的输出预算请求。
func TestRun_TruncatedEmptyResponseGoesStraightToWrapUp(t *testing.T) {
	provider := &recordingProvider{mockLLMProvider: mockLLMProvider{responses: []llm.Response{
		{Content: "", FinishReason: "length", Usage: llm.Usage{OutTokens: 4096}},
		{Content: planJSON},
	}}}
	config := structuredConfig(provider)

	result, err := NewReActRuntime().Run(context.Background(), config)
	require.NoError(t, err)
	require.Len(t, provider.requests, 2, "被截断的空响应之后应当直接是一次收尾")
	ask := lastUserMessage(provider.requests[1])
	assert.True(t, strings.HasPrefix(ask, "上一版输出达到输出上限（4096 token）被截断，没有写完。"), ask)
	assert.Equal(t, maxForcedOutputTokens, provider.requests[1].MaxTokens, "收尾按放大的输出预算请求")
	assert.Equal(t, planJSON, result.FinalAnswer)
	assert.Nil(t, result.InternalError)
}
