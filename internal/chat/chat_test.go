package chat

import (
	"context"
	"encoding/json"
	"testing"

	llm "github.com/V3teran/liusha/internal/framework/llm"
)

type fakeDeps struct {
	genReply  string
	sawSystem bool
	appended  string
	published bool
}

func (f *fakeDeps) Generate(_ context.Context, msgs []llm.Message, _ []llm.ToolSchema) (llm.Result, error) {
	for _, m := range msgs {
		if m.Role == llm.RoleSystem {
			f.sawSystem = true
		}
	}
	return llm.Result{Content: f.genReply}, nil
}

// GenerateStream 模拟流式：把整段回复切成三块逐段回调（覆盖打字机链路）。
func (f *fakeDeps) GenerateStream(_ context.Context, msgs []llm.Message, _ []llm.ToolSchema, onText, _ func(chunk string)) (llm.Result, error) {
	res, err := f.Generate(context.Background(), msgs, nil)
	if err != nil {
		return res, err
	}
	if onText != nil {
		runes := []rune(res.Content)
		step := len(runes)/3 + 1
		for i := 0; i < len(runes); i += step {
			onText(string(runes[i : i+step]))
		}
	}
	return res, nil
}

func (f *fakeDeps) PublishDelta(_ context.Context, _, _, _ string) error { return nil }

func (f *fakeDeps) AppendAssistant(_ context.Context, _, content, _ string) ([]byte, error) {
	f.appended = content
	return json.Marshal(map[string]string{"content": content})
}

func (f *fakeDeps) Publish(_ context.Context, _ string, _ []byte) error {
	f.published = true
	return nil
}

func TestAnswer(t *testing.T) {
	d := &fakeDeps{genReply: "你好，想测哪个目标？直接描述即可开始。"}
	svc := New(d)

	err := svc.Answer(context.Background(), "conv1", "在吗")
	if err != nil {
		t.Fatal(err)
	}
	if !d.sawSystem {
		t.Error("应带 system prompt")
	}
	if d.appended != "你好，想测哪个目标？直接描述即可开始。" {
		t.Errorf("应落 assistant 回答，得 %q", d.appended)
	}
	if !d.published {
		t.Error("应 publish SSE")
	}
}
