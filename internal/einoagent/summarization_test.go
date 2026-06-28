package einoagent

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// fakeChatModel 是最小 model.BaseChatModel——summarization.New 只校验 Model != nil，不实际调用。
type fakeChatModel struct{}

func (fakeChatModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage("ok", nil), nil
}
func (fakeChatModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func TestNewSummarizationHandler_NilCompactor(t *testing.T) {
	_, err := NewSummarizationHandler(nil, SummarizationParams{ContextWindow: 32000}, nil)
	if err == nil {
		t.Fatal("compactor 为 nil 应报错")
	}
}

func TestNewSummarizationHandler_ZeroContextWindow(t *testing.T) {
	_, err := NewSummarizationHandler(fakeChatModel{}, SummarizationParams{ContextWindow: 0}, nil)
	if err == nil {
		t.Fatal("ContextWindow<=0 应报错（不允许瞎猜窗口）")
	}
}

func TestNewSummarizationHandler_OK(t *testing.T) {
	h, err := NewSummarizationHandler(fakeChatModel{}, SummarizationParams{ContextWindow: 32000, TriggerRatio: 0.75}, nil)
	if err != nil {
		t.Fatalf("合法入参不应报错: %v", err)
	}
	if h == nil {
		t.Fatal("应返回非 nil handler")
	}
}

func TestSummaryTextFromState_FromMultiContent(t *testing.T) {
	// eino 把摘要正文放 UserInputMultiContent 的 text part（Content 清空）。
	msg := &schema.Message{
		Role: schema.User,
		UserInputMultiContent: []schema.MessageInputPart{
			{Type: schema.ChatMessagePartTypeText, Text: "蒸馏后的摘要"},
		},
	}
	got := summaryTextFromState(adk.ChatModelAgentState{Messages: []adk.Message{msg}})
	if got != "蒸馏后的摘要" {
		t.Errorf("应从 UserInputMultiContent 取摘要，得 %q", got)
	}
}

func TestSummaryTextFromState_FallbackContent(t *testing.T) {
	msg := &schema.Message{Role: schema.User, Content: "纯 Content 摘要"}
	got := summaryTextFromState(adk.ChatModelAgentState{Messages: []adk.Message{msg}})
	if got != "纯 Content 摘要" {
		t.Errorf("应回退读 Content，得 %q", got)
	}
}

func TestSummaryTextFromState_Empty(t *testing.T) {
	if got := summaryTextFromState(adk.ChatModelAgentState{}); got != "" {
		t.Errorf("空 state 应返空串，得 %q", got)
	}
}
