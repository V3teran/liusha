package executor

import (
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/reasoning"
)

const outputJSON = `{"status":"completed","summary":"登录成功并建立了会话","observations":[{"category":"access","statement":"admin/password 可登录"}]}`

// 终稿里取得到输出 JSON 就算验收通过：裸 JSON、json 围栏、说明文字前后夹着 JSON 都认——
// 模型在 JSON 外写的说明不是不合格。取不到带 status 的 JSON（纯散文、别的 JSON）才不通过。
func TestOutputCheck(t *testing.T) {
	for name, answer := range map[string]string{
		"裸 JSON":      outputJSON,
		"json 围栏":     "```json\n" + outputJSON + "\n```",
		"说明 + 围栏":     "验证完成——匿名 POST 成功触发数据库重置。\n\n```json\n" + outputJSON + "\n```\n",
		"说明 + 裸 JSON": "执行完毕，结果如下：\n" + outputJSON + "\n以上。",
		"字符串里有裸反斜杠":   "```json\n" + strings.Replace(outputJSON, "登录成功并建立了会话", "读了 C:\\www\\config", 1) + "\n```",
	} {
		assert.NoError(t, outputCheck(answer), name)
	}
	for name, answer := range map[string]string{
		"纯散文":      "验证完成，四个路径都是 404。",
		"别的 JSON":  `{"instruction":"再探一次"}`,
		"空终稿":      "",
		"缺 status": `{"summary":"没有状态","observations":[]}`,
		"围栏外才有括号":  "输出形如 {\"a\":1} 的对象。",
	} {
		assert.Error(t, outputCheck(answer), name)
	}
}

// ParseOutput 先看终稿正文；终稿里没有时往前找带 observations 的助手消息；都没有才给空输出，
// 不报错。
func TestParseOutput_WhereTheOutputComesFrom(t *testing.T) {
	fromFinal, ok := outputInText("说明\n" + outputJSON)
	require.True(t, ok)
	require.Len(t, fromFinal.Observations, 1)
	assert.Equal(t, "admin/password 可登录", fromFinal.Observations[0].Statement)

	history := []llm.Message{
		{Role: llm.RoleAssistant, Content: "先看看。\n" + outputJSON, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "probe"}}},
		{Role: llm.RoleTool, ToolCallID: "c1", Content: "ok"},
		{Role: llm.RoleAssistant, Content: "都做完了。"},
	}
	fromHistory := ParseOutput(&runtime.ReActResult{FinalAnswer: "都做完了。", MessageHistory: history})
	require.Len(t, fromHistory.Observations, 1, "终稿是散文时，前面那条带 observations 的输出不丢")

	empty := ParseOutput(&runtime.ReActResult{FinalAnswer: "都做完了。"})
	assert.Equal(t, Output{Status: "completed", Summary: "执行完成，无观察结果", Observations: []Observation{}}, *empty)
}

// scriptedProvider 按调用次序返回预设响应，记下每一次请求。
type scriptedProvider struct {
	responses []llm.Response
	requests  []llm.Request
}

func (p *scriptedProvider) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	p.requests = append(p.requests, req)
	i := len(p.requests) - 1
	if i >= len(p.responses) {
		return llm.Response{}, assert.AnError
	}
	return p.responses[i], nil
}

func (p *scriptedProvider) Stream(context.Context, llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, llm.ErrStreamingUnsupported
}

func (p *scriptedProvider) ModelID() string    { return "scripted" }
func (p *scriptedProvider) ProviderID() string { return "test" }

// roundTap 记下结束那一轮时的终态帧。
type roundTap struct{ rounds []reasoning.Round }

func (t *roundTap) Delta(string)                              {}
func (t *roundTap) ThinkingDelta(string)                      {}
func (t *roundTap) Done(_ context.Context, r reasoning.Round) { t.rounds = append(t.rounds, r) }

func finalAnswerConfig(provider llm.Provider) *runtime.ReActConfig {
	return &runtime.ReActConfig{
		Objective:          "做一件事",
		SystemPrompt:       "章程与契约",
		LLMProvider:        provider,
		MaxIterations:      5,
		OutputCheck:        outputCheck,
		OutputFormatPrompt: "你刚才完成了动作的执行，现在需要按输出契约输出终稿。",
		OutputFormatSchema: "契约",
	}
}

// 模型在输出 JSON 前写了说明：这一版终稿直接交付，不再多花一次调用；时间线上按协议叙述
// （模型写的说明留在原文里）。
func TestExecute_SummaryBeforeJSONIsAcceptedInOneCall(t *testing.T) {
	provider := &scriptedProvider{responses: []llm.Response{{Content: "验证完成——匿名 POST 成功触发数据库重置。\n\n```json\n" + outputJSON + "\n```"}}}
	tap := &roundTap{}
	e := &Engine{thoughts: tap, logger: zerolog.Nop()}

	_, err := e.executeReActRuntime(context.Background(), runtime.NewReActRuntime(), finalAnswerConfig(provider), "a1")
	require.NoError(t, err)
	require.Len(t, provider.requests, 1, "说明文字 + JSON 的终稿不该被重述")
	require.Len(t, tap.rounds, 1)
	assert.Equal(t, "执行完成 · 1 条观察\n\n登录成功并建立了会话", tap.rounds[0].Summary)
}

// 终稿是纯散文、没有输出 JSON：带着原因请模型重述一次，重述的 JSON 成为终稿；重述也没交出
// JSON 时终稿原样留着，不被兜底摘要覆盖。
func TestExecute_ProseFinalIsRestatedOnce(t *testing.T) {
	provider := &scriptedProvider{responses: []llm.Response{
		{Content: "都做完了，没有发现。"},
		{Content: outputJSON},
	}}
	tap := &roundTap{}
	e := &Engine{thoughts: tap, logger: zerolog.Nop()}

	result, err := e.executeReActRuntime(context.Background(), runtime.NewReActRuntime(), finalAnswerConfig(provider), "a1")
	require.NoError(t, err)
	require.Len(t, provider.requests, 2)
	assert.Contains(t, provider.requests[1].Messages[len(provider.requests[1].Messages)-1].Content, "上一版终稿没通过验收（取不到带 status 的输出 JSON）")
	assert.Equal(t, outputJSON, result.FinalAnswer, "重述的 JSON 成为终稿")
	assert.Equal(t, "执行完成 · 1 条观察\n\n登录成功并建立了会话", tap.rounds[0].Summary)

	provider = &scriptedProvider{responses: []llm.Response{{Content: "都做完了，没有发现。"}, {Content: "还是没有 JSON。"}}}
	tap = &roundTap{}
	result, err = (&Engine{thoughts: tap, logger: zerolog.Nop()}).executeReActRuntime(context.Background(), runtime.NewReActRuntime(), finalAnswerConfig(provider), "a1")
	require.NoError(t, err)
	assert.Equal(t, "还是没有 JSON。", result.FinalAnswer)
	assert.Equal(t, "还是没有 JSON。", tap.rounds[0].Summary, "散文终稿原样留着")
}
