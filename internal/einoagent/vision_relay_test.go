package einoagent_test

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/V3teran/liusha/internal/einoagent"
)

func toolResultWithImage() *compose.EnhancedInvokableToolOutput {
	b := "aW1n"
	return &compose.EnhancedInvokableToolOutput{Result: &schema.ToolResult{Parts: []schema.ToolOutputPart{
		{Type: schema.ToolPartTypeText, Text: `{"stdout":"ok"}`},
		{Type: schema.ToolPartTypeImage, Image: &schema.ToolOutputImage{
			MessagePartCommon: schema.MessagePartCommon{Base64Data: &b, MIMEType: "image/png"},
		}},
	}}}
}

func imageEndpoint(_ context.Context, _ *compose.ToolInput) (*compose.EnhancedInvokableToolOutput, error) {
	return toolResultWithImage(), nil
}

// vision=true：image part 从 tool 输出剥离 + flush 成 user message。
func TestVisionRelay_RelaysToUserMessage(t *testing.T) {
	mw := einoagent.NewVisionRelayMiddleware(true)

	wrapped := mw.WrapToolCall.EnhancedInvokable(imageEndpoint)
	out, err := wrapped(context.Background(), &compose.ToolInput{Name: "run_command"})
	if err != nil {
		t.Fatal(err)
	}
	// tool 输出剥掉 image，只剩 text（永不触发 mimo 400）
	if len(out.Result.Parts) != 1 || out.Result.Parts[0].Type != schema.ToolPartTypeText {
		t.Fatalf("tool 输出应只剩 text part: %+v", out.Result.Parts)
	}

	// BeforeChatModel flush pending 图成 user message
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{
		schema.SystemMessage("sys"), schema.UserMessage("task"),
	}}
	if err := mw.BeforeChatModel(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	last := state.Messages[len(state.Messages)-1]
	if last.Role != schema.User || len(last.UserInputMultiContent) != 1 {
		t.Fatalf("应追加带图 user message: %+v", last)
	}
	part := last.UserInputMultiContent[0]
	if part.Type != schema.ChatMessagePartTypeImageURL || part.Image == nil ||
		part.Image.MIMEType != "image/png" || part.Image.Base64Data == nil || *part.Image.Base64Data != "aW1n" {
		t.Errorf("user message 图 part 错: %+v", part)
	}
}

// 二次 BeforeChatModel 不重复插（pending 已清空）。
func TestVisionRelay_FlushOnce(t *testing.T) {
	mw := einoagent.NewVisionRelayMiddleware(true)
	wrapped := mw.WrapToolCall.EnhancedInvokable(imageEndpoint)
	_, _ = wrapped(context.Background(), &compose.ToolInput{Name: "run_command"})

	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("x")}}
	_ = mw.BeforeChatModel(context.Background(), state)
	n1 := len(state.Messages)
	_ = mw.BeforeChatModel(context.Background(), state)
	if len(state.Messages) != n1 {
		t.Errorf("pending 已 flush，二次不应再插: %d -> %d", n1, len(state.Messages))
	}
}

// vision=false：image 被丢弃，不 flush（仅文本占位降级）。
func TestVisionRelay_NonVisionDiscards(t *testing.T) {
	mw := einoagent.NewVisionRelayMiddleware(false)
	wrapped := mw.WrapToolCall.EnhancedInvokable(imageEndpoint)
	out, _ := wrapped(context.Background(), &compose.ToolInput{Name: "run_command"})
	// 仍剥掉 image（tool message 不带图）
	if len(out.Result.Parts) != 1 {
		t.Fatalf("非 vision 也应剥 image: %+v", out.Result.Parts)
	}
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("x")}}
	_ = mw.BeforeChatModel(context.Background(), state)
	if len(state.Messages) != 1 {
		t.Errorf("非 vision 不应回灌图: %d", len(state.Messages))
	}
}
