package einoagent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/compose"

	"github.com/V3teran/liusha/internal/einoagent"
)

type fakeEventSink struct{ events []einoagent.ScanEvent }

func (f *fakeEventSink) OnScanEvent(_ context.Context, ev einoagent.ScanEvent) {
	f.events = append(f.events, ev)
}

func TestEventEmitter_EmitsCallThenResult(t *testing.T) {
	sink := &fakeEventSink{}
	mw := einoagent.NewEventEmitter(sink)
	if mw.WrapToolCall.Invokable == nil {
		t.Fatal("Invokable middleware 应非 nil")
	}

	// 包一个返回固定结果的 endpoint，模拟一次工具执行。
	endpoint := mw.WrapToolCall.Invokable(func(_ context.Context, _ *compose.ToolInput) (*compose.ToolOutput, error) {
		return &compose.ToolOutput{Result: "注入点确认 DB=dvwa"}, nil
	})
	_, err := endpoint(context.Background(), &compose.ToolInput{
		Name: "run_command", Arguments: `{"command":"sqlmap -u ... --batch"}`,
	})
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}

	// 应发 2 个事件：tool_call（执行前）+ tool_result（执行后）。
	if len(sink.events) != 2 {
		t.Fatalf("应发 2 个事件，得 %d: %+v", len(sink.events), sink.events)
	}
	call := sink.events[0]
	if call.Kind != einoagent.ScanEventToolCall || call.ToolName != "run_command" {
		t.Errorf("事件1 应是 tool_call/run_command，得 %s/%s", call.Kind, call.ToolName)
	}
	if call.Args != `{"command":"sqlmap -u ... --batch"}` {
		t.Errorf("tool_call 应带命令参数，得 %q", call.Args)
	}
	res := sink.events[1]
	if res.Kind != einoagent.ScanEventToolResult || res.ToolName != "run_command" {
		t.Errorf("事件2 应是 tool_result/run_command，得 %s/%s", res.Kind, res.ToolName)
	}
	if res.Result != "注入点确认 DB=dvwa" {
		t.Errorf("tool_result 应带结果，得 %q", res.Result)
	}
}

func TestEventEmitter_RecordsError(t *testing.T) {
	sink := &fakeEventSink{}
	mw := einoagent.NewEventEmitter(sink)
	endpoint := mw.WrapToolCall.Invokable(func(_ context.Context, _ *compose.ToolInput) (*compose.ToolOutput, error) {
		return nil, errors.New("sandbox 超时")
	})
	_, _ = endpoint(context.Background(), &compose.ToolInput{Name: "run_command", Arguments: "{}"})

	if len(sink.events) != 2 {
		t.Fatalf("应发 2 个事件，得 %d", len(sink.events))
	}
	if sink.events[1].Err != "sandbox 超时" {
		t.Errorf("tool_result 应记录错误，得 %q", sink.events[1].Err)
	}
}

func TestEventEmitter_NilSinkNoop(t *testing.T) {
	mw := einoagent.NewEventEmitter(nil)
	if mw.WrapToolCall.Invokable != nil || mw.WrapToolCall.EnhancedInvokable != nil {
		t.Error("nil sink 应返回零值 middleware（no-op）")
	}
}
