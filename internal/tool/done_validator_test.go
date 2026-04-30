package tool

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAlwaysOK_AlwaysReturnsTrue(t *testing.T) {
	v := AlwaysOK{}
	ok, missing := v.CanDone(context.Background(), json.RawMessage(`{"any":"thing"}`))
	if !ok {
		t.Fatal("AlwaysOK 应该总是返回 true")
	}
	if missing != nil {
		t.Fatalf("AlwaysOK 应该返回 nil missing，实际: %v", missing)
	}
}
