package einoagent

import (
	"strings"
	"testing"
)

// TestSubAgentTargetSection 验证子代理固定目标段：host 非空时含目标 + 防 localhost 告诫；空则空串。
func TestSubAgentTargetSection(t *testing.T) {
	got := subAgentTargetSection("111.229.193.40:34280")
	for _, want := range []string{"111.229.193.40:34280", "目标 Host", "127.0.0.1", "localhost"} {
		if !strings.Contains(got, want) {
			t.Errorf("目标段应含 %q, got:\n%s", want, got)
		}
	}

	if s := subAgentTargetSection(""); s != "" {
		t.Errorf("host 空应返回空串, got %q", s)
	}
}
