package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/V3teran/liusha/internal/agent/action"
	"github.com/V3teran/liusha/internal/agent/runtime"
)

// passthroughExec 是一个永远成功的 base executor。
func passthroughExec(_ context.Context, _ string, _ json.RawMessage) (action.Result, error) {
	return action.Result{Output: json.RawMessage(`{}`)}, nil
}

func TestLoopDetect_NoRepeat(t *testing.T) {
	mw := LoopDetect()
	exec := mw(passthroughExec)

	for i, args := range []string{`{"a":1}`, `{"a":2}`, `{"a":3}`} {
		_, err := exec(context.Background(), "scan", json.RawMessage(args))
		if err != nil {
			t.Fatalf("第 %d 次不同参数不应抛错: %v", i, err)
		}
	}
}

func TestLoopDetect_SameRepeatTwice(t *testing.T) {
	mw := LoopDetect()
	exec := mw(passthroughExec)

	for i := 0; i < 2; i++ {
		_, err := exec(context.Background(), "scan", json.RawMessage(`{"a":1}`))
		if err != nil {
			t.Fatalf("第 %d 次连续相同应该不抛: %v", i, err)
		}
	}
}

func TestLoopDetect_SameRepeatThree(t *testing.T) {
	mw := LoopDetect()
	exec := mw(passthroughExec)

	args := json.RawMessage(`{"a":1}`)
	if _, err := exec(context.Background(), "scan", args); err != nil {
		t.Fatalf("第 1 次不应抛: %v", err)
	}
	if _, err := exec(context.Background(), "scan", args); err != nil {
		t.Fatalf("第 2 次不应抛: %v", err)
	}
	_, err := exec(context.Background(), "scan", args)
	if !errors.Is(err, runtime.ErrLoopDetectorAbort) {
		t.Fatalf("第 3 次连续相同应抛 ErrLoopDetectorAbort, 实际: %v", err)
	}
}

func TestLoopDetect_AlternatingHashes(t *testing.T) {
	mw := LoopDetect()
	exec := mw(passthroughExec)

	a := json.RawMessage(`{"x":"A"}`)
	b := json.RawMessage(`{"x":"B"}`)
	pattern := []json.RawMessage{a, b, a, b, a, b, a}
	for i, args := range pattern {
		if _, err := exec(context.Background(), "scan", args); err != nil {
			t.Fatalf("第 %d 次交替不应抛: %v", i, err)
		}
	}
}

func TestLoopDetect_DifferentNameNotConfused(t *testing.T) {
	mw := LoopDetect()
	exec := mw(passthroughExec)

	args := json.RawMessage(`{"a":1}`)
	for i, name := range []string{"scan", "leak", "exploit"} {
		if _, err := exec(context.Background(), name, args); err != nil {
			t.Fatalf("第 %d 次不同 name 不应抛: %v", i, err)
		}
	}
}

func TestLoopDetect_CallerSkillSeparates(t *testing.T) {
	mw := LoopDetect()
	exec := mw(passthroughExec)

	args := json.RawMessage(`{"a":1}`)
	ctxA := context.WithValue(context.Background(), CtxKeyCallerSkill, "sqli")
	ctxB := context.WithValue(context.Background(), CtxKeyCallerSkill, "bac")

	if _, err := exec(ctxA, "scan", args); err != nil {
		t.Fatal(err)
	}
	if _, err := exec(ctxB, "scan", args); err != nil {
		t.Fatal(err)
	}
	if _, err := exec(ctxA, "scan", args); err != nil {
		t.Fatal(err)
	}
	if _, err := exec(ctxB, "scan", args); err != nil {
		t.Fatal(err)
	}
}
