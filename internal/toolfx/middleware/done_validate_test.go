package middleware

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/toolfx"
)

type fakeValidator struct {
	ok      bool
	missing []string
	called  int
}

func (f *fakeValidator) CanDone(_ context.Context, _ json.RawMessage) (bool, []string) {
	f.called++
	return f.ok, f.missing
}

func TestDoneValidate_NotDone(t *testing.T) {
	v := &fakeValidator{ok: false, missing: []string{"recon"}}
	mw := DoneValidate(v, nil)
	called := false
	exec := mw(func(_ context.Context, _ string, _ json.RawMessage) (toolfx.Result, error) {
		called = true
		return toolfx.Result{Output: []byte(`{}`)}, nil
	})

	_, err := exec(context.Background(), "scan", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("name != done 不应被拦截: %v", err)
	}
	if !called {
		t.Fatal("base executor 应该被调用")
	}
	if v.called != 0 {
		t.Fatal("validator 不应被调用")
	}
}

func TestDoneValidate_DoneOK(t *testing.T) {
	v := &fakeValidator{ok: true}
	mw := DoneValidate(v, nil)
	called := false
	exec := mw(func(_ context.Context, _ string, _ json.RawMessage) (toolfx.Result, error) {
		called = true
		return toolfx.Result{Done: true, Output: []byte(`{}`)}, nil
	})

	res, err := exec(context.Background(), "done", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CanDone=true 应该放行: %v", err)
	}
	if !res.Done {
		t.Fatal("Result.Done 应该被透传")
	}
	if !called {
		t.Fatal("validator 通过后 base executor 应被调用")
	}
	if v.called != 1 {
		t.Fatalf("validator 应被调用 1 次, 实际 %d", v.called)
	}
}

func TestDoneValidate_DoneBlocked(t *testing.T) {
	v := &fakeValidator{ok: false, missing: []string{"recon", "exploit"}}
	mw := DoneValidate(v, nil)
	called := false
	exec := mw(func(_ context.Context, _ string, _ json.RawMessage) (toolfx.Result, error) {
		called = true
		return toolfx.Result{}, nil
	})

	_, err := exec(context.Background(), "done", json.RawMessage(`{}`))
	notReady, ok := react.IsDoneNotReady(err)
	if !ok {
		t.Fatalf("应抛 ErrDoneNotReady, 实际: %v", err)
	}
	if len(notReady.Missing) != 2 || notReady.Missing[0] != "recon" {
		t.Fatalf("Missing 应该被透传, 实际: %v", notReady.Missing)
	}
	if called {
		t.Fatal("CanDone=false 时 base executor 不应被调用")
	}
}

func TestDoneValidate_NilValidatorSafe(t *testing.T) {
	// 防御：nil validator 应当行为同 AlwaysOK（避免 NPE）。
	mw := DoneValidate(nil, nil)
	exec := mw(func(_ context.Context, _ string, _ json.RawMessage) (toolfx.Result, error) {
		return toolfx.Result{Done: true}, nil
	})

	_, err := exec(context.Background(), "done", json.RawMessage(`{}`))
	if err != nil {
		if _, ok := react.IsDoneNotReady(err); ok {
			t.Fatalf("nil validator 不应抛 ErrDoneNotReady: %v", err)
		}
	}
}
