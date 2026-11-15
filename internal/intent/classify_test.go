package intent

import (
	"context"
	"errors"
	"strings"
	"testing"

	llm "github.com/V3teran/liusha/internal/framework/llm"
)

// fakeGen 依次给出 replies（用完后重复最后一条），记录每次收到的消息。
type fakeGen struct {
	replies []string
	err     error
	calls   [][]llm.Message
}

func (f *fakeGen) Generate(_ context.Context, msgs []llm.Message, _ []llm.ToolSchema) (llm.Result, error) {
	f.calls = append(f.calls, msgs)
	if f.err != nil {
		return llm.Result{}, f.err
	}
	i := min(len(f.calls)-1, len(f.replies)-1)
	return llm.Result{Content: f.replies[i]}, nil
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name, reply string
		want        Kind
	}{
		{"明确 action", "action", Action},
		{"明确 qa", "qa", QA},
		{"带空格大小写", "  ACTION\n", Action},
		{"带标点与 Markdown", "**`Action`。**", Action},
		{"带引号", "\"qa\"", QA},
		{"句子里只出现一个类别", "分类结果：action", Action},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := &fakeGen{replies: []string{c.reply}}
			got, err := Classify(context.Background(), g, "用户消息", nil)
			if err != nil {
				t.Fatalf("reply=%q 意外错误: %v", c.reply, err)
			}
			if got != c.want {
				t.Errorf("reply=%q want %v got %v", c.reply, c.want, got)
			}
			if len(g.calls) != 1 {
				t.Errorf("可识别的输出不应重问，调用了 %d 次", len(g.calls))
			}
		})
	}
}

// 待分类的消息是数据，不是给分类模型的指令：放进分隔标签，输出要求在它之后——
// 否则模型会照着消息去回答或执行（实测把「对某目标做 XSS 测试」当成指令，输出了测试计划）。
func TestClassify_MessageIsDataNotInstruction(t *testing.T) {
	g := &fakeGen{replies: []string{"action"}}
	recent := []llm.Message{
		{Role: llm.RoleUser, Content: "扫一下 http://t.local"},
		{Role: llm.RoleAssistant, Content: "已开始扫描"},
	}
	if _, err := Classify(context.Background(), g, "继续，重点看登录页", recent); err != nil {
		t.Fatal(err)
	}
	msgs := g.calls[0]
	if len(msgs) != 2 || msgs[0].Role != llm.RoleSystem || msgs[1].Role != llm.RoleUser {
		t.Fatalf("应只有 system + 一条 user（上文与待分类消息都作为数据），得 %d 条", len(msgs))
	}
	body := msgs[1].Content
	msgAt := strings.Index(body, "<message>\n继续，重点看登录页\n</message>")
	if msgAt < 0 {
		t.Fatalf("待分类消息应包在 <message> 标签里：\n%s", body)
	}
	if !strings.Contains(body, "扫一下 http://t.local") || !strings.Contains(body, "已开始扫描") {
		t.Errorf("此前对话应作为数据带上，用于消解指代：\n%s", body)
	}
	if strings.Index(body, "已开始扫描") > msgAt {
		t.Errorf("此前对话应在待分类消息之前")
	}
	if tail := body[msgAt:]; !strings.Contains(tail, "action 或 qa") {
		t.Errorf("输出要求应放在待分类消息之后（模型最后读到的是指令）：\n%s", tail)
	}
}

func TestClassify_RetriesOnceOnUnrecognizedOutput(t *testing.T) {
	g := &fakeGen{replies: []string{"开始对目标进行反射型 XSS 测试。先探测页面结构……", "action"}}
	got, err := Classify(context.Background(), g, "对 http://t.local 做 XSS 测试", nil)
	if err != nil || got != Action {
		t.Fatalf("重问后应得 action，得 %v, %v", got, err)
	}
	if len(g.calls) != 2 {
		t.Fatalf("应重问一次，调用了 %d 次", len(g.calls))
	}
	retry := g.calls[1]
	if n := len(retry); n < 4 || retry[n-2].Role != llm.RoleAssistant || retry[n-1].Role != llm.RoleUser {
		t.Fatalf("重问应带上无效输出与纠正要求，得 %+v", retry)
	}
}

// 两次都无法识别：明确报错，不悄悄归为 qa——那会把扫描请求变成聊天回答，任务不启动，
// 回答读起来却像已经开始测试。
func TestClassify_UnrecognizedTwiceIsError(t *testing.T) {
	cases := []string{
		"我是助手，不是分类器。",
		"action 或 qa 都有可能",
		"",
		"This request needs immediate action: I will start probing the target now.",
	}
	for _, reply := range cases {
		g := &fakeGen{replies: []string{reply}}
		got, err := Classify(context.Background(), g, "用户消息", nil)
		if !errors.Is(err, ErrUnrecognized) {
			t.Errorf("reply=%q 应返回 ErrUnrecognized，得 %v", reply, err)
		}
		if got != QA {
			t.Errorf("出错时携带保守缺省 qa，得 %v", got)
		}
		if len(g.calls) != 2 {
			t.Errorf("reply=%q 应重问一次后放弃，调用了 %d 次", reply, len(g.calls))
		}
	}
}

func TestClassify_ErrorReturnsError(t *testing.T) {
	// 生成失败必须显式报错：意图闸失明静默降级 qa 会把扫描请求悄悄变成聊天回答。
	got, err := Classify(context.Background(), &fakeGen{err: context.DeadlineExceeded}, "x", nil)
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
