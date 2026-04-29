package action

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeAction 是测试用的最简 Action 实现。
type fakeAction struct {
	name string
	desc string
	out  []byte
	err  error
	done bool
}

func (a fakeAction) Name() string        { return a.name }
func (a fakeAction) Description() string { return a.desc }
func (a fakeAction) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func (a fakeAction) Execute(_ context.Context, args json.RawMessage) (Result, error) {
	if a.err != nil {
		return Result{}, a.err
	}
	out := a.out
	if out == nil {
		out = args
	}
	return Result{Output: out, Done: a.done, Summary: a.name + " ok"}, nil
}

func TestRegistry_Register_DuplicateError(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(fakeAction{name: "echo"}); err != nil {
		t.Fatalf("首次注册不应该出错: %v", err)
	}
	err := r.Register(fakeAction{name: "echo"})
	if err == nil {
		t.Fatal("重名注册应该报错")
	}
	if !strings.Contains(err.Error(), "echo") {
		t.Fatalf("错误信息应该包含动作名 echo, 实际: %v", err)
	}
}

func TestRegistry_Execute_NotFound(t *testing.T) {
	r := NewRegistry()
	_, err := r.Execute(context.Background(), "nonexistent", nil)
	if err == nil {
		t.Fatal("调用不存在的动作应该返回 error")
	}
}

func TestRegistry_Execute_BasicAction(t *testing.T) {
	r := NewRegistry()
	a := fakeAction{name: "echo", desc: "回显参数"}
	if err := r.Register(a); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	args := json.RawMessage(`{"msg":"hi"}`)
	res, err := r.Execute(context.Background(), "echo", args)
	if err != nil {
		t.Fatalf("Execute 不应该出错: %v", err)
	}
	if string(res.Output) != `{"msg":"hi"}` {
		t.Fatalf("Output 不匹配, 实际: %s", string(res.Output))
	}
	if res.Summary != "echo ok" {
		t.Fatalf("Summary 不匹配, 实际: %q", res.Summary)
	}
}

func TestRegistry_Use_MiddlewareWrapsExecution(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(fakeAction{name: "echo"}); err != nil {
		t.Fatalf("注册失败: %v", err)
	}

	var counter int32
	mw := func(next ActionExecutor) ActionExecutor {
		return func(ctx context.Context, name string, args json.RawMessage) (Result, error) {
			atomic.AddInt32(&counter, 1)
			return next(ctx, name, args)
		}
	}
	r.Use(mw)

	if _, err := r.Execute(context.Background(), "echo", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	if atomic.LoadInt32(&counter) != 1 {
		t.Fatalf("中间件应该被调用 1 次, 实际: %d", counter)
	}
}

func TestRegistry_Use_MultipleMiddlewareInOrder(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(fakeAction{name: "echo"}); err != nil {
		t.Fatalf("注册失败: %v", err)
	}

	var trace []string
	mw1 := func(next ActionExecutor) ActionExecutor {
		return func(ctx context.Context, name string, args json.RawMessage) (Result, error) {
			trace = append(trace, "mw1-enter")
			res, err := next(ctx, name, args)
			trace = append(trace, "mw1-exit")
			return res, err
		}
	}
	mw2 := func(next ActionExecutor) ActionExecutor {
		return func(ctx context.Context, name string, args json.RawMessage) (Result, error) {
			trace = append(trace, "mw2-enter")
			res, err := next(ctx, name, args)
			trace = append(trace, "mw2-exit")
			return res, err
		}
	}
	r.Use(mw1, mw2)

	if _, err := r.Execute(context.Background(), "echo", nil); err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}

	want := []string{"mw1-enter", "mw2-enter", "mw2-exit", "mw1-exit"}
	if len(trace) != len(want) {
		t.Fatalf("trace 长度不匹配, 期望 %v, 实际 %v", want, trace)
	}
	for i := range want {
		if trace[i] != want[i] {
			t.Fatalf("trace[%d] 期望 %q, 实际 %q (full: %v)", i, want[i], trace[i], trace)
		}
	}
}

func TestRegistry_Schemas_ReturnsAll(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"a", "b", "c"} {
		if err := r.Register(fakeAction{name: name, desc: "desc-" + name}); err != nil {
			t.Fatalf("注册 %s 失败: %v", name, err)
		}
	}
	schemas := r.Schemas()
	if len(schemas) != 3 {
		t.Fatalf("Schemas 应该返回 3 项, 实际: %d", len(schemas))
	}
	seen := map[string]bool{}
	for _, s := range schemas {
		seen[s.Name] = true
		if s.Description != "desc-"+s.Name {
			t.Fatalf("Description 不匹配 name=%s desc=%q", s.Name, s.Description)
		}
		if len(s.Parameters) == 0 {
			t.Fatalf("Parameters 不应为空, name=%s", s.Name)
		}
	}
	for _, name := range []string{"a", "b", "c"} {
		if !seen[name] {
			t.Fatalf("缺少 schema name=%s", name)
		}
	}
}

func TestRegistry_Execute_PropagatesError(t *testing.T) {
	r := NewRegistry()
	wantErr := errors.New("boom")
	if err := r.Register(fakeAction{name: "fail", err: wantErr}); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	_, err := r.Execute(context.Background(), "fail", nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("应该透传原始错误, 实际: %v", err)
	}
}
