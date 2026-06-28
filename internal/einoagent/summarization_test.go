package einoagent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
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

// TestEstimateCJKTokens_Chinese：中文按 ~1 token/字，远高于 eino 默认 char/4（按字节/4 会低估）。
func TestEstimateCJKTokens_Chinese(t *testing.T) {
	s := "你好世界一二三四" // 8 个 CJK 字
	got := estimateCJKTokens(s)
	if got != 8 {
		t.Errorf("8 个中文字应估 8 token，得 %d", got)
	}
	// 对比 eino 默认 char/4（按字节）：8 字 = 24 字节 / 4 = 6，明显低估 → 证明纠偏。
	defaultEstimate := (len(s) + 3) / 4
	if got <= defaultEstimate {
		t.Errorf("CJK counter 应高于默认 char/4（%d），得 %d", defaultEstimate, got)
	}
}

// TestEstimateCJKTokens_English：英文仍按 ~4 字符/token（与默认一致）。
func TestEstimateCJKTokens_English(t *testing.T) {
	if got := estimateCJKTokens("hello world test"); got != 4 { // 16 字符 /4
		t.Errorf("英文 16 字符应估 4 token，得 %d", got)
	}
}

// TestEstimateCJKTokens_Mixed：中英混合分别计。
func TestEstimateCJKTokens_Mixed(t *testing.T) {
	// 2 CJK + " sqlmap"(7 非 CJK) = 2 + (7+3)/4 = 2 + 2 = 4
	if got := estimateCJKTokens("测试 sqlmap"); got != 4 {
		t.Errorf("混合串应估 4 token，得 %d", got)
	}
}

// TestMessageText_IncludesToolCalls：tool_call 名/参数必须计入（sqlmap 长命令体积大）。
func TestMessageText_IncludesToolCalls(t *testing.T) {
	m := &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{Function: schema.FunctionCall{Name: "run_command", Arguments: `{"cmd":"sqlmap -u ..."}`}},
		},
	}
	txt := messageText(m)
	if !strings.Contains(txt, "run_command") || !strings.Contains(txt, "sqlmap") {
		t.Errorf("messageText 应含 tool_call 名与参数，得 %q", txt)
	}
}

// TestCJKTokenCounter_CountsMessages：counter 聚合多条消息文本。
func TestCJKTokenCounter_CountsMessages(t *testing.T) {
	in := &summarization.TokenCounterInput{
		Messages: []adk.Message{
			&schema.Message{Role: schema.User, Content: "你好世界"},     // 4 CJK = 4
			&schema.Message{Role: schema.Assistant, Content: "test"}, // 4 字符 = 1
		},
	}
	got, err := cjkTokenCounter(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got != 5 { // 4 + 1
		t.Errorf("两条消息应估 5 token，得 %d", got)
	}
}
