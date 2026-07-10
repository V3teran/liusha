package einotools

import (
	"context"
	"testing"
)

// TestWriteFindingRequiredNotInflated 回归：eino-contrib/jsonschema 默认把无 ,omitempty 的字段
// 全标 required，撑大 required 数组触发 mimo "non-unique elements" 400。可选字段必须带 ,omitempty。
// 钉死 write_finding 的 required 只含真正必填的 summary，防回退。
func TestWriteFindingRequiredNotInflated(t *testing.T) {
	tl, err := BuildWriteFinding(nil, "task-1", "hunter-1", "host", 0)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	info, err := tl.Info(context.Background())
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	js, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatalf("ToJSONSchema: %v", err)
	}
	if len(js.Required) != 1 || js.Required[0] != "summary" {
		t.Fatalf("write_finding required 膨胀（应仅 [summary]，可选字段须带 ,omitempty）：%v", js.Required)
	}
}
