package einoagent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/compose"

	"github.com/V3teran/liusha/internal/einoagent"
)

type recordingSink struct {
	got []einoagent.ToolInvocation
}

func (r *recordingSink) RecordTool(_ context.Context, inv einoagent.ToolInvocation) {
	r.got = append(r.got, inv)
}

func TestNewToolRecorder_NilSinkNoop(t *testing.T) {
	mw := einoagent.NewToolRecorder(nil, "h1", "task-1")
	if mw.WrapToolCall.Invokable != nil || mw.WrapToolCall.EnhancedInvokable != nil {
		t.Error("nil sink 应返回 no-op middleware")
	}
}

func TestNewToolRecorder_EmptyHunterNoop(t *testing.T) {
	mw := einoagent.NewToolRecorder(&recordingSink{}, "", "task-1")
	if mw.WrapToolCall.Invokable != nil {
		t.Error("空 hunterID 应返回 no-op middleware")
	}
}

// 直接驱动 Invokable middleware：包一个 fake endpoint，调用后断言 sink 落库正确。
func TestToolRecorder_InvokableRecords(t *testing.T) {
	sink := &recordingSink{}
	mw := einoagent.NewToolRecorder(sink, "hunter-1", "task-1")

	fakeEndpoint := func(_ context.Context, _ *compose.ToolInput) (*compose.ToolOutput, error) {
		return &compose.ToolOutput{Result: `{"id":"f1"}`}, nil
	}
	wrapped := mw.WrapToolCall.Invokable(fakeEndpoint)
	_, err := wrapped(context.Background(), &compose.ToolInput{Name: "write_finding", Arguments: `{"summary":"x"}`})
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.got) != 1 {
		t.Fatalf("应落 1 行 tool_invocation，得到 %d", len(sink.got))
	}
	g := sink.got[0]
	if g.ToolName != "write_finding" || g.HunterID != "hunter-1" || g.TaskID != "task-1" {
		t.Errorf("注入/工具名错: %+v", g)
	}
	if string(g.Args) != `{"summary":"x"}` || g.OutputSize != len(`{"id":"f1"}`) {
		t.Errorf("args/output 错: args=%s size=%d", g.Args, g.OutputSize)
	}
	if g.ErrorMessage != "" {
		t.Errorf("成功调用不应有 error: %q", g.ErrorMessage)
	}
}

// 工具报错也落库（ErrorMessage 非空）。
func TestToolRecorder_RecordsError(t *testing.T) {
	sink := &recordingSink{}
	mw := einoagent.NewToolRecorder(sink, "hunter-1", "task-1")

	failEndpoint := func(_ context.Context, _ *compose.ToolInput) (*compose.ToolOutput, error) {
		return nil, errors.New("boom")
	}
	wrapped := mw.WrapToolCall.Invokable(failEndpoint)
	_, _ = wrapped(context.Background(), &compose.ToolInput{Name: "replay_traffic", Arguments: `{}`})
	if len(sink.got) != 1 || sink.got[0].ErrorMessage != "boom" {
		t.Fatalf("失败调用应落库带 error: %+v", sink.got)
	}
}
