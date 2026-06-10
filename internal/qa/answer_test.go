package qa

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/llm"
)

type fakeDeps struct {
	findings  string
	genReply  string
	appended  string
	published bool
}

func (f *fakeDeps) FindingsSummary(_ context.Context, _, _ string) (string, error) {
	return f.findings, nil
}

func (f *fakeDeps) Generate(_ context.Context, _ []llm.Message, _ []llm.ToolSchema) (llm.Result, error) {
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
	d := &fakeDeps{
		findings: "1. [high] SQLi at /login",
		genReply: "那个 SQLi 在登录框，参数 user 未过滤。",
	}
	svc := New(d)
	err := svc.Answer(context.Background(), "conv1", "scan1", "解释下那个 SQLi")
	if err != nil {
		t.Fatal(err)
	}
	if d.appended != "那个 SQLi 在登录框，参数 user 未过滤。" {
		t.Errorf("应落 assistant 回答，得 %q", d.appended)
	}
	if !d.published {
		t.Error("应 publish SSE")
	}
}
