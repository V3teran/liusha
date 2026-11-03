package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	llm "github.com/V3teran/liusha/internal/framework/llm"
)

type fakeDeps struct {
	genReply  string
	sawSystem bool
	appended  string
	published bool
	history   []llm.Message // History 返回的上文（nil=空）
	sawMsgs   []llm.Message // GenerateStream 实际收到的消息序列（拼装断言用）
}

func (f *fakeDeps) Generate(_ context.Context, msgs []llm.Message, _ []llm.ToolSchema) (llm.Result, error) {
	for _, m := range msgs {
		if m.Role == llm.RoleSystem {
			f.sawSystem = true
		}
	}
	return llm.Result{Content: f.genReply}, nil
}

// GenerateStream 模拟流式：记录收到的消息序列，并把整段回复切成三块逐段
// 回调（覆盖打字机链路）。
func (f *fakeDeps) GenerateStream(_ context.Context, msgs []llm.Message, _ []llm.ToolSchema, onText, _ func(chunk string)) (llm.Result, error) {
	f.sawMsgs = msgs
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

func (f *fakeDeps) History(_ context.Context, _, exclude string) ([]llm.Message, error) {
	out := make([]llm.Message, 0, len(f.history))
	for _, m := range f.history {
		if m.Role == llm.RoleUser && m.Content == exclude {
			continue // 适配器契约：排除与当前消息相同的末条 user
		}
		out = append(out, m)
	}
	return out, nil
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

// TestAnswer_OversizedMessageClamped 病态超长消息进上下文前被钳制：
// 末条 user 有界，且当前消息不在历史里重复出现（History 按原文匹配剔除）。
func TestAnswer_OversizedMessageClamped(t *testing.T) {
	huge := strings.Repeat("贴", 40000) // 40k runes > 32k 上限
	d := &fakeDeps{
		genReply: "收到",
		history:  []llm.Message{{Role: llm.RoleUser, Content: huge}}, // History 原样返回（含当前消息）
	}
	svc := New(d)
	if err := svc.Answer(context.Background(), "conv1", huge); err != nil {
		t.Fatal(err)
	}
	last := d.sawMsgs[len(d.sawMsgs)-1]
	assert.LessOrEqual(t, len([]rune(last.Content)), 32000+len("…（截断）"),
		"末条消息必须钳制到上限内")
	// 探针必须按 rune 切（huge[:30000] 是字节切片，只取到 1 万字会让计数失真）
	probe := string([]rune(huge)[:30000])
	assert.Equal(t, 1, strings.Count(strings.Join(contentOf(d.sawMsgs), "\x00"), probe),
		"当前消息只应出现一次（历史剔除按原文匹配，不受钳制影响）")
}

func contentOf(msgs []llm.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Content
	}
	return out
}

// TestAnswer_HistoryInjected 验证多轮对话：上文参与拼装（连续追问不失忆），
// 当前消息仍作为末条 user。
func TestAnswer_HistoryInjected(t *testing.T) {
	d := &fakeDeps{
		genReply: "好的，改打 8443。",
		history: []llm.Message{
			{Role: llm.RoleUser, Content: "在吗"},
			{Role: llm.RoleAssistant, Content: "你好，想测哪个目标？"},
			{Role: llm.RoleUser, Content: "扫 8080 上的 web"},
			{Role: llm.RoleAssistant, Content: "8080 是 tomcat，默认页无洞。"},
		},
	}
	svc := New(d)
	if err := svc.Answer(context.Background(), "conv1", "换 8443 再试"); err != nil {
		t.Fatal(err)
	}
	// system + 4 条上文 + 当前消息
	got := d.sawMsgs
	if len(got) != 6 {
		t.Fatalf("期望 6 条消息（system+4 上文+当前），得 %d", len(got))
	}
	if got[len(got)-1].Content != "换 8443 再试" {
		t.Errorf("末条应是当前消息，得 %q", got[len(got)-1].Content)
	}
}
