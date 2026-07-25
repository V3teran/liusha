package attackgraph

import (
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/einoagent"
)

// TestTraceEventMirrorsScanEvent 守护 trace.go 的 traceEvent 与 einoagent.ScanEvent 的字段契约。
//
// 投影包刻意不 import einoagent（避免把 eino 重依赖拖进纯投影包），改用同名字段 + 大小写不敏感
// json.Unmarshal 做隐式镜像——但这样一来，若 ScanEvent 改了字段名，投影会静默丢字段而无编译错误。
// 本测试是**测试期**的显式契约守护（测试依赖不进生产构建图）：
//   - 构造带全字段的 einoagent.ScanEvent（字段名写死）→ ScanEvent 改名即此处编译失败；
//   - JSON 往返进 traceEvent → key 漂移即断言失败。
func TestTraceEventMirrorsScanEvent(t *testing.T) {
	src := einoagent.ScanEvent{
		Kind:      einoagent.ScanEventToolResult,
		ToolName:  "write_finding",
		Args:      `{"subagent_type":"exploitation"}`,
		Result:    `{"id":"f-1"}`,
		Err:       "boom",
		Text:      "推理正文",
		AgentName: "orchestrator",
	}

	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatal(err)
	}
	var got traceEvent
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	if got.Kind != string(src.Kind) {
		t.Errorf("Kind 未镜像：got %q want %q", got.Kind, src.Kind)
	}
	if got.ToolName != src.ToolName {
		t.Errorf("ToolName 未镜像：got %q want %q", got.ToolName, src.ToolName)
	}
	if got.Args != src.Args {
		t.Errorf("Args 未镜像：got %q want %q", got.Args, src.Args)
	}
	if got.Result != src.Result {
		t.Errorf("Result 未镜像：got %q want %q", got.Result, src.Result)
	}
	if got.Err != src.Err {
		t.Errorf("Err 未镜像：got %q want %q", got.Err, src.Err)
	}
	if got.Text != src.Text {
		t.Errorf("Text 未镜像：got %q want %q", got.Text, src.Text)
	}
	if got.AgentName != src.AgentName {
		t.Errorf("AgentName 未镜像：got %q want %q", got.AgentName, src.AgentName)
	}
}
