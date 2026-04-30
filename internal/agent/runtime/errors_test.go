package runtime

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrDoneNotReady_AsError(t *testing.T) {
	missing := []string{"recon", "exploit"}
	original := ErrDoneNotReady{Missing: missing}
	wrapped := fmt.Errorf("外层: %w", original)

	got, ok := IsDoneNotReady(wrapped)
	if !ok {
		t.Fatal("IsDoneNotReady 应该识别被包装的错误")
	}
	if len(got.Missing) != 2 || got.Missing[0] != "recon" || got.Missing[1] != "exploit" {
		t.Fatalf("Missing 字段应该被保留，实际: %v", got.Missing)
	}
}

func TestErrDoneNotReady_ErrorMessage(t *testing.T) {
	e := ErrDoneNotReady{Missing: []string{"step_a", "step_b"}}
	want := "done not ready: missing step_a, step_b"
	if e.Error() != want {
		t.Fatalf("Error 文本不匹配，期望 %q, 实际 %q", want, e.Error())
	}
}

func TestIsDoneNotReady_NotMatch(t *testing.T) {
	other := errors.New("something else")
	if _, ok := IsDoneNotReady(other); ok {
		t.Fatal("无关 error 不应被识别为 ErrDoneNotReady")
	}
}
