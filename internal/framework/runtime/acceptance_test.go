package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// lastUserMessage 是一次请求里最后一条用户消息（收尾请求写在最后）。
func lastUserMessage(req llm.Request) string {
	messages := req.Messages
	return messages[len(messages)-1].Content
}

// planCheck 是规划者那样的验收：取得到的 JSON 必须带 should_continue。
func planCheck(answer string) error {
	var shape map[string]json.RawMessage
	if err := llm.ParseJSON(answer, &shape); err != nil {
		return errors.New("取不出 JSON 对象")
	}
	if _, ok := shape["should_continue"]; !ok {
		return errors.New("JSON 里没有 should_continue")
	}
	return nil
}

const planJSON = `{"should_continue":true,"actions":[]}`

func structuredConfig(provider llm.Provider) *ReActConfig {
	return &ReActConfig{
		Objective:          "规划",
		SystemPrompt:       "章程",
		LLMProvider:        provider,
		MaxIterations:      5,
		OutputFormatPrompt: "你是规划者。",
		OutputFormatSchema: "契约",
		OutputCheck:        planCheck,
	}
}

// 模型在 JSON 前写了说明、验收方取得出 JSON：终稿原样交付，不多花一次调用。
func TestRun_AcceptedFinalAnswerIsNotRestated(t *testing.T) {
	final := "先摸清对象面再深挖。\n\n```json\n" + planJSON + "\n```"
	provider := &recordingProvider{mockLLMProvider: mockLLMProvider{responses: []llm.Response{{Content: final}}}}
	config := structuredConfig(provider)
	config.OutputCheck = planCheck

	result, err := NewReActRuntime().Run(context.Background(), config)
	require.NoError(t, err)
	require.Len(t, provider.requests, 1, "验收通过就不该重述")
	assert.Equal(t, final, result.FinalAnswer)
	assert.Nil(t, result.InternalError)
}

// 验收不通过：带着原因请模型重述一次，重述的终稿成为终稿；原因写在收尾请求的开头，前面
// 是模型自己那一版。
func TestRun_RejectedFinalAnswerIsRestatedWithReason(t *testing.T) {
	provider := &recordingProvider{mockLLMProvider: mockLLMProvider{responses: []llm.Response{
		{Content: `{"reasoning":"还没想好"}`},
		{Content: planJSON},
	}}}
	config := structuredConfig(provider)
	config.OutputCheck = planCheck

	result, err := NewReActRuntime().Run(context.Background(), config)
	require.NoError(t, err)
	require.Len(t, provider.requests, 2)
	ask := lastUserMessage(provider.requests[2-1])
	assert.True(t, strings.HasPrefix(ask, "上一版终稿没通过验收（JSON 里没有 should_continue）。\n\n你是规划者。"), ask)
	assert.Contains(t, ask, "契约", "收尾请求重申输出契约")
	messages := provider.requests[1].Messages
	assert.Equal(t, `{"reasoning":"还没想好"}`, messages[len(messages)-2].Content, "模型看得到自己没通过验收的那一版")
	assert.Equal(t, planJSON, result.FinalAnswer)
	assert.Nil(t, result.InternalError)
}

// 配了输出格式却没有验收方：运行时无从判断终稿合不合格，配置错误在开跑前报出来——
// 不留「只看是否以 JSON 开头」的默认判定，那会把说明文字 + JSON 的终稿打成不合格。
func TestValidate_OutputFormatRequiresCheck(t *testing.T) {
	config := structuredConfig(&mockLLMProvider{})
	require.NoError(t, config.Validate())
	config.OutputCheck = nil
	assert.EqualError(t, config.Validate(), "配了输出格式（OutputFormatPrompt / OutputFormatSchema）就必须给出终稿验收（OutputCheck）")
	assert.NoError(t, (&ReActConfig{Objective: "评估", LLMProvider: &mockLLMProvider{}}).Validate(), "没有输出契约的运行不需要验收")
}

// 没配输出格式的运行：结论经工具发出，终稿只是叙述——模型写下什么就是终稿，不再多花
// 一次调用要一份「最终总结」；只有空终稿才请它补。
func TestRun_WithoutOutputFormatProseFinalStands(t *testing.T) {
	final := "本轮态势评估如下，无需发布监察决策。"
	provider := &recordingProvider{mockLLMProvider: mockLLMProvider{responses: []llm.Response{{Content: final}}}}
	config := &ReActConfig{Objective: "评估", SystemPrompt: "章程", LLMProvider: provider, MaxIterations: 5}

	result, err := NewReActRuntime().Run(context.Background(), config)
	require.NoError(t, err)
	require.Len(t, provider.requests, 1, "没有输出契约的运行不该强求总结")
	assert.Equal(t, final, result.FinalAnswer)

	// 空响应由循环自己纠偏（注入一句提示继续），也不走强制总结。
	provider = &recordingProvider{mockLLMProvider: mockLLMProvider{responses: []llm.Response{{Content: "   "}, {Content: final}}}}
	result, err = NewReActRuntime().Run(context.Background(), &ReActConfig{Objective: "评估", SystemPrompt: "章程", LLMProvider: provider, MaxIterations: 5})
	require.NoError(t, err)
	for _, req := range provider.requests {
		assert.NotContains(t, lastUserMessage(req), "输出一个最终总结")
	}
	assert.Equal(t, final, result.FinalAnswer)
}

// 迭代达到上限时同样收尾，要的是配了的输出契约（模型交回的观察、判定、规划才取得出来），
// 没配才只要一份总结；收尾请求如实说明原因。
func TestRun_IterationLimitAsksForTheConfiguredFormat(t *testing.T) {
	toolCall := llm.Response{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "probe", Arguments: json.RawMessage(`{}`)}}}
	for name, config := range map[string]*ReActConfig{
		"配了输出格式": structuredConfig(nil),
		"没配":     {Objective: "评估", SystemPrompt: "章程", MaxIterations: 2},
	} {
		t.Run(name, func(t *testing.T) {
			provider := &recordingProvider{mockLLMProvider: mockLLMProvider{responses: []llm.Response{toolCall, toolCall, {Content: planJSON}}}}
			config.LLMProvider = provider
			config.MaxIterations = 2
			react := NewReActRuntime()
			require.NoError(t, react.RegisterTool(&mockTool{name: "probe", description: "探测", handler: func(json.RawMessage) (any, error) { return "ok", nil }}))

			result, err := react.Run(context.Background(), config)
			require.NoError(t, err)
			assert.Equal(t, ReActStatusMaxIterations, result.Status)
			require.Len(t, provider.requests, 3)
			ask := lastUserMessage(provider.requests[3-1])
			assert.True(t, strings.HasPrefix(ask, "迭代已达上限，不能再调用工具。"), ask)
			if config.wantsOutputFormat() {
				assert.Contains(t, ask, "你是规划者。")
				assert.Contains(t, ask, "必须是有效的JSON格式")
				assert.NotContains(t, ask, "输出一个最终总结")
			} else {
				assert.Contains(t, ask, "输出一个最终总结")
			}
			assert.Equal(t, planJSON, result.FinalAnswer)
		})
	}
}

// checkFinalAnswer：原因是一句交给模型的话；没有输出契约的运行写下就算。
func TestCheckFinalAnswer(t *testing.T) {
	accept := func(string) error { return nil }
	reject := func(string) error { return errors.New("缺 should_continue") }
	assert.Equal(t, "", (&ReActConfig{OutputCheck: accept}).checkFinalAnswer("说明 + JSON"))
	assert.Equal(t, "上一版终稿没通过验收（缺 should_continue）。", (&ReActConfig{OutputCheck: reject}).checkFinalAnswer("{}"))
	assert.Equal(t, "", (&ReActConfig{}).checkFinalAnswer("评估完毕。"))
}

// stopBeforeIteration 在第 at 次迭代开始前终止循环（止损中间件的替身）。
type stopBeforeIteration struct{ at int }

func (m stopBeforeIteration) Name() string { return "stop" }

func (m stopBeforeIteration) BeforeIteration(_ context.Context, state *IterationState) (*StopSignal, error) {
	if state.Iteration >= m.at {
		return &StopSignal{ShouldStop: true, Reason: StopReasonCustom}, nil
	}
	return nil, nil
}

func (m stopBeforeIteration) AfterIteration(context.Context, *IterationState) (*StopSignal, error) {
	return nil, nil
}

// 循环在模型交出终稿之前被中间件终止：最后一条消息是工具结果，没有终稿可交付——请模型
// 基于已经做完的事收尾，执行者的观察、评估者的判定、规划者的动作都写在这份终稿里。
// 配了输出格式按契约要，没配只要一份总结。这一条曾被「最后一条不是模型回复就放行」
// 的写法弄丢过：靶场里 24 个动作有 20 个因此落成「执行完成，无观察结果」。
func TestRun_StoppedBeforeFinalAnswerAsksForWrapUp(t *testing.T) {
	toolCall := llm.Response{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "probe", Arguments: json.RawMessage(`{}`)}}}
	for name, config := range map[string]*ReActConfig{
		"配了输出格式": structuredConfig(nil),
		"没配":     {Objective: "评估", SystemPrompt: "章程"},
	} {
		t.Run(name, func(t *testing.T) {
			provider := &recordingProvider{mockLLMProvider: mockLLMProvider{responses: []llm.Response{toolCall, toolCall, {Content: planJSON}}}}
			config.LLMProvider = provider
			config.MaxIterations = 10
			config.MiddlewareChain = NewMiddlewareChain(stopBeforeIteration{at: 3})
			react := NewReActRuntime()
			require.NoError(t, react.RegisterTool(&mockTool{name: "probe", description: "探测", handler: func(json.RawMessage) (any, error) { return "ok", nil }}))

			result, err := react.Run(context.Background(), config)
			require.NoError(t, err)
			require.Len(t, provider.requests, 3, "两次工具迭代之后应当是一次收尾")
			messages := provider.requests[2].Messages
			assert.Equal(t, llm.RoleTool, messages[len(messages)-2].Role, "收尾请求接在工具结果之后")
			ask := lastUserMessage(provider.requests[2])
			assert.True(t, strings.HasPrefix(ask, whyNoFinalAnswer), ask)
			if config.wantsOutputFormat() {
				assert.Contains(t, ask, "必须是有效的JSON格式")
			} else {
				assert.Contains(t, ask, "输出一个最终总结")
			}
			assert.Equal(t, planJSON, result.FinalAnswer)
			assert.Nil(t, result.InternalError)
		})
	}
}
