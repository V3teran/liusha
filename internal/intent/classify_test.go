package intent

import (
	"context"
	"errors"
	"testing"

	llm "github.com/V3teran/liusha/internal/framework/llm"
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
		want        Kind
	}{
		{"明确 action", "action", Action},
		{"明确 qa", "qa", QA},
		{"带空格大小写", "  ACTION\n", Action},
		{"模糊输出偏 qa", "我觉得是问答吧", QA},
		{"空输出偏 qa", "", QA},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Classify(context.Background(), fakeGen{reply: c.reply}, "用户消息")
			if err != nil {
				t.Fatalf("reply=%q 意外错误: %v", c.reply, err)
			}
			if got != c.want {
				t.Errorf("reply=%q want %v got %v", c.reply, c.want, got)
			}
		})
	}
}

func TestClassify_ErrorReturnsError(t *testing.T) {
	// 生成失败必须显式报错：意图闸失明静默降级 qa 会把扫描请求悄悄变成聊天回答。
	got, err := Classify(context.Background(), fakeGen{err: context.DeadlineExceeded}, "x")
	if err == nil {
		t.Fatal("调用失败应返回 error，得 nil")
	}
	if got != QA {
		t.Errorf("错误返回时应携带保守缺省 qa，得 %v", got)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error 应保留底层原因，得 %v", err)
	}
}
