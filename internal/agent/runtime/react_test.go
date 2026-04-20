package runtime_test

import (
	"context"
	"errors"
	"testing"

	einoschema "github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/agent/runtime"
	"github.com/V3teran/liusha/internal/worker"
)

// stubGenerator 固定返回同一条消息，统计调用次数。
type stubGenerator struct {
	calls    int
	response *einoschema.Message
	err      error
}

func (s *stubGenerator) Generate(_ context.Context, _ []*einoschema.Message) (*einoschema.Message, error) {
	s.calls++
	return s.response, s.err
}

// multiResponseGenerator 按顺序返回多条预设响应。
type multiResponseGenerator struct {
	responses []*einoschema.Message
	idx       int
}

func (m *multiResponseGenerator) Generate(_ context.Context, _ []*einoschema.Message) (*einoschema.Message, error) {
	if m.idx >= len(m.responses) {
		return nil, errors.New("exhausted")
	}
	r := m.responses[m.idx]
	m.idx++
	return r, nil
}

// recordingSink 记录所有 Observation 便于断言。
type recordingSink struct {
	observations []runtime.Observation
	returnErr    error
}

func (s *recordingSink) Observe(_ context.Context, o runtime.Observation) error {
	s.observations = append(s.observations, o)
	return s.returnErr
}

// TestReAct_FinalAnswerEmitsObservation 验证首轮 final answer 直接经 Sink 输出后返回 nil。
func TestReAct_FinalAnswerEmitsObservation(t *testing.T) {
	gen := &stubGenerator{response: &einoschema.Message{
		Role:    einoschema.Assistant,
		Content: "未发现漏洞（骨架测试）",
	}}
	sink := &recordingSink{}

	err := runtime.Run(context.Background(), runtime.RunParams{
		Payload:      worker.AgentRunPayload{EngagementID: uuid.New(), TaskID: uuid.New(), Role: "web_recon"},
		SystemPrompt: "你是 web_recon",
		MaxSteps:     5,
		Gen:          gen,
		Sink:         sink,
	})

	require.NoError(t, err)
	assert.Equal(t, 1, gen.calls)
	require.Len(t, sink.observations, 1)
	assert.Equal(t, "final_answer", sink.observations[0].Kind)
	assert.Equal(t, "未发现漏洞（骨架测试）", sink.observations[0].Content)
}

// TestReAct_ToolCallEmitsDeferredObservation 验证 tool call 被标记为 deferred，后续转 final。
func TestReAct_ToolCallEmitsDeferredObservation(t *testing.T) {
	responses := []*einoschema.Message{
		{
			Role: einoschema.Assistant,
			ToolCalls: []einoschema.ToolCall{{
				ID:   "tc1",
				Type: "function",
				Function: einoschema.FunctionCall{
					Name:      "nuclei_scan",
					Arguments: `{"target":"x"}`,
				},
			}},
		},
		{Role: einoschema.Assistant, Content: "分析完成"},
	}
	gen := &multiResponseGenerator{responses: responses}
	sink := &recordingSink{}

	err := runtime.Run(context.Background(), runtime.RunParams{
		Payload:      worker.AgentRunPayload{EngagementID: uuid.New(), TaskID: uuid.New(), Role: "web_recon"},
		SystemPrompt: "x",
		MaxSteps:     5,
		Gen:          gen,
		Sink:         sink,
	})

	require.NoError(t, err)
	require.Len(t, sink.observations, 2)
	assert.Equal(t, "tool_call_deferred", sink.observations[0].Kind)
	assert.Equal(t, "nuclei_scan", sink.observations[0].ToolName)
	assert.Equal(t, "final_answer", sink.observations[1].Kind)
}

// TestReAct_ExceedsMaxStepsReturnsError 验证 tool-call 循环到 MaxSteps 依然无 final 则报错。
func TestReAct_ExceedsMaxStepsReturnsError(t *testing.T) {
	gen := &stubGenerator{response: &einoschema.Message{
		Role: einoschema.Assistant,
		ToolCalls: []einoschema.ToolCall{{
			ID: "tc", Type: "function",
			Function: einoschema.FunctionCall{Name: "nuclei_scan", Arguments: `{}`},
		}},
	}}
	sink := &recordingSink{}

	err := runtime.Run(context.Background(), runtime.RunParams{
		Payload:      worker.AgentRunPayload{EngagementID: uuid.New(), TaskID: uuid.New(), Role: "web_recon"},
		SystemPrompt: "x",
		MaxSteps:     3,
		Gen:          gen,
		Sink:         sink,
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "最大步数")
	assert.Equal(t, 3, gen.calls)
}

// TestReAct_LLMErrorPropagates 验证 Generator 错误带上下文向上透传。
func TestReAct_LLMErrorPropagates(t *testing.T) {
	gen := &stubGenerator{err: errors.New("network broken")}
	err := runtime.Run(context.Background(), runtime.RunParams{
		Payload:      worker.AgentRunPayload{TaskID: uuid.New()},
		SystemPrompt: "x",
		MaxSteps:     5,
		Gen:          gen,
		Sink:         &recordingSink{},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "LLM Generate 失败")
}
