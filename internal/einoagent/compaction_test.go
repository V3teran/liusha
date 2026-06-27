package einoagent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// fakeCompactor 实现 model.BaseChatModel，Generate 返回固定摘要，记录收到的入参。
type fakeCompactor struct {
	gotInput string
	summary  string
}

func (f *fakeCompactor) Generate(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if len(in) >= 2 {
		f.gotInput = in[1].Content // [0]=system distill prompt, [1]=老消息序列化
	}
	return schema.AssistantMessage(f.summary, nil), nil
}
func (f *fakeCompactor) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

// buildConversation 造带 tool 配对的会话：system + user(flow) + 多个 (assistant tool_call, tool result) turn。
func buildConversation(turns int) []*schema.Message {
	msgs := []*schema.Message{
		schema.SystemMessage("system prompt"),
		schema.UserMessage("flow raw http"),
	}
	for i := 0; i < turns; i++ {
		a := schema.AssistantMessage("", []schema.ToolCall{
			{ID: "c", Function: schema.FunctionCall{Name: "read_findings", Arguments: "{}"}},
		})
		msgs = append(msgs, a)
		msgs = append(msgs, &schema.Message{Role: schema.Tool, Content: "tool result", ToolCallID: "c"})
	}
	return msgs
}

func TestCompaction_TriggersAndKeepsTail(t *testing.T) {
	fc := &fakeCompactor{summary: "蒸馏后的关键证据摘要"}
	mw := NewCompactionMiddleware(fc, CompactionConfig{TriggerCount: 10, KeepTail: 6}, nil)

	conv := buildConversation(15) // 2 + 30 = 32 条，超 trigger
	state := &adk.ChatModelAgentState{Messages: conv}
	if err := mw.BeforeChatModel(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	out := state.Messages

	// system 保留在最前
	if out[0].Role != schema.System {
		t.Fatalf("system 应保留最前: %v", out[0].Role)
	}
	// 第二条应是摘要（user，带 tag）
	if out[1].Role != schema.User || !strings.Contains(out[1].Content, summaryTag) {
		t.Fatalf("第二条应是摘要 user 消息: %+v", out[1])
	}
	// 压缩后应显著变短
	if len(out) >= len(conv) {
		t.Errorf("压缩后应更短: %d -> %d", len(conv), len(out))
	}
	// 关键：摘要后第一条尾消息不能是 tool（不能有 orphan tool result = 未拆配对）
	if out[2].Role == schema.Tool {
		t.Errorf("摘要后第一条尾消息不能是 tool（会成 orphan）: %v", out[2].Role)
	}
	// distill 收到了老消息序列化（含 tool_call 痕迹）
	if !strings.Contains(fc.gotInput, "read_findings") {
		t.Errorf("distill 入参应含老 turn 的 tool_call: %q", fc.gotInput)
	}
}

func TestCompaction_BelowTriggerNoop(t *testing.T) {
	fc := &fakeCompactor{summary: "x"}
	mw := NewCompactionMiddleware(fc, CompactionConfig{TriggerCount: 100, KeepTail: 6}, nil)
	conv := buildConversation(3) // 8 条，远低于 trigger
	state := &adk.ChatModelAgentState{Messages: conv}
	if err := mw.BeforeChatModel(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if len(state.Messages) != len(conv) {
		t.Errorf("低于 trigger 不应压缩: %d -> %d", len(conv), len(state.Messages))
	}
	if fc.gotInput != "" {
		t.Error("低于 trigger 不应调 compactor")
	}
}

func TestSafeCut_NeverStartsOnToolResult(t *testing.T) {
	conv := buildConversation(10) // system,user, 然后 a,tool,a,tool...
	cut := safeCut(conv, 1, 6)    // sysEnd=1（1 条 system）
	if cut <= 1 || cut >= len(conv) {
		t.Fatalf("cut 越界: %d (len=%d)", cut, len(conv))
	}
	if conv[cut].Role == schema.Tool {
		t.Errorf("cut 落点不能是 tool result（会拆配对）: idx=%d", cut)
	}
}
