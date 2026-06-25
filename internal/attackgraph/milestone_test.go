package attackgraph

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/conversation"
)

// fakeSummarizer 回显 prompt 前缀，便于断言调用；failOn 命中则返回错误。
type fakeSummarizer struct {
	calls  int
	failOn string
}

func (f *fakeSummarizer) Summarize(_ context.Context, prompt string) (string, error) {
	f.calls++
	if f.failOn != "" && strings.Contains(prompt, f.failOn) {
		return "", errors.New("boom")
	}
	n := 10
	if len(prompt) < n {
		n = len(prompt)
	}
	return "摘要:" + prompt[:n], nil
}

func TestMilestones(t *testing.T) {
	ctx := context.Background()

	t.Run("按子代理聚合且保首次出现序", func(t *testing.T) {
		msgs := []conversation.Message{
			mkEventMsg("o1", evReasoning, map[string]any{"AgentName": "orchestrator", "Text": "规划"}),
			mkEventMsg("e1", evReasoning, map[string]any{"AgentName": "exploitation", "Text": "试注入"}),
			mkEventMsg("o2", evReasoning, map[string]any{"AgentName": "orchestrator", "Text": "再派活"}),
			mkEventMsg("a1", evToolCall, map[string]any{"AgentName": "exploitation", "ToolName": "curl"}), // 非 reasoning 不计
		}
		f := &fakeSummarizer{}
		ms, err := Milestones(ctx, msgs, f)
		if err != nil {
			t.Fatal(err)
		}
		if len(ms) != 2 {
			t.Fatalf("里程碑数=%d 期望 2（两个 agent）：%+v", len(ms), ms)
		}
		if ms[0].Agent != "orchestrator" || ms[1].Agent != "exploitation" {
			t.Errorf("顺序错：%s,%s 期望 orchestrator,exploitation", ms[0].Agent, ms[1].Agent)
		}
		if ms[0].NodeCount != 2 { // orchestrator 两条 reasoning
			t.Errorf("orchestrator NodeCount=%d 期望 2", ms[0].NodeCount)
		}
		if f.calls != 2 {
			t.Errorf("LLM 调用数=%d 期望 2（每 agent 一次）", f.calls)
		}
	})

	t.Run("总结失败回退不阻断", func(t *testing.T) {
		msgs := []conversation.Message{
			mkEventMsg("e1", evReasoning, map[string]any{"AgentName": "exploitation", "Text": "x"}),
		}
		ms, err := Milestones(ctx, msgs, &fakeSummarizer{failOn: "exploitation"})
		if err != nil {
			t.Fatal(err)
		}
		if len(ms) != 1 || ms[0].Summary != "（摘要生成失败）" {
			t.Errorf("期望 fallback 摘要，得 %+v", ms)
		}
	})

	t.Run("空 summarizer 报错", func(t *testing.T) {
		if _, err := Milestones(ctx, nil, nil); err == nil {
			t.Fatal("期望 summarizer 为空报错")
		}
	})

	t.Run("无 reasoning 返回空", func(t *testing.T) {
		msgs := []conversation.Message{
			mkEventMsg("a1", evToolCall, map[string]any{"ToolName": "curl"}),
		}
		ms, err := Milestones(ctx, msgs, &fakeSummarizer{})
		if err != nil || len(ms) != 0 {
			t.Errorf("期望空里程碑，得 %+v err=%v", ms, err)
		}
	})
}
