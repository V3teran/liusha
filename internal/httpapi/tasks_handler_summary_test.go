package httpapi

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/V3teran/liusha/internal/task"
)

// 任务摘要把发现数按严重度分档交给前端（列表的发现列按档显示）；没有发现时是空表而不是 null。
func TestTaskSummaryJSON_FindingsBySeverity(t *testing.T) {
	encode := func(s task.Summary) map[string]any {
		t.Helper()
		raw, err := json.Marshal(taskSummaryJSON(s))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return got
	}

	got := encode(task.Summary{FindingCount: 3, FindingsBySeverity: map[string]int{"high": 2, "low": 1}})
	if want := map[string]any{"high": float64(2), "low": float64(1)}; !reflect.DeepEqual(got["findings_by_severity"], want) {
		t.Errorf("发现分档不符：得 %#v，期望 %#v", got["findings_by_severity"], want)
	}

	got = encode(task.Summary{})
	bySeverity, ok := got["findings_by_severity"].(map[string]any)
	if !ok || len(bySeverity) != 0 {
		t.Errorf("没有发现时 findings_by_severity 应为空表，得 %#v", got["findings_by_severity"])
	}
}
