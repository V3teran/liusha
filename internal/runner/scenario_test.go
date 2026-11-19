package runner

import (
	"testing"

	"github.com/V3teran/liusha/internal/task"
)

// 场景块只取任务行快照：无场景为空，有场景按快照渲染。
func TestScenarioBlock(t *testing.T) {
	if got := scenarioBlock(task.Task{}); got != "" {
		t.Fatalf("无场景应为空: %q", got)
	}
	got := scenarioBlock(task.Task{Scenario: &task.Scenario{Code: "ctf", Name: "CTF", Body: "规约"}})
	if got != "<scenario name=\"CTF\">\n规约\n</scenario>" {
		t.Fatalf("应按快照渲染: %q", got)
	}
}
