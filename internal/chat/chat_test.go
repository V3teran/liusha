package chat

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/llm"
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

func (f *fakeDeps) AppendAssistant(_ context.Context, _, content string) ([]byte, error) {
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
