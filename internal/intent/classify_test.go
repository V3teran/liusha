package intent

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/llm"
)

type fakeGen struct {
	reply string
	err   error
}

func (f fakeGen) Generate(_ context.Context, _ []llm.Message, _ []llm.ToolSchema) (llm.Result, error) {
	return llm.Result{Content: f.reply}, f.err
}

func (f fakeGen) Provider() string {
	return "fake"
}

func (f fakeGen) Model() string {
	return "fake-model"
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name, reply string
		want        Intent
	}{
		{"明确 action", "action", IntentAction},
		{"明确 qa", "qa", IntentQA},
		{"带空格大小写", "  ACTION\n", IntentAction},
		{"模糊输出偏 qa", "我觉得是问答吧", IntentQA},
		{"空输出偏 qa", "", IntentQA},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Classify(context.Background(), fakeGen{reply: c.reply}, "用户消息")
			if got != c.want {
				t.Errorf("reply=%q want %v got %v", c.reply, c.want, got)
			}
		})
	}
}

func TestClassify_ErrorFallsBackQA(t *testing.T) {
	got := Classify(context.Background(), fakeGen{err: context.DeadlineExceeded}, "x")
	if got != IntentQA {
		t.Errorf("调用失败应默认 qa，得 %v", got)
	}
}
