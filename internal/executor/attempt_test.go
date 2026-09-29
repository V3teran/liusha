package executor

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/finding"
)

// AttemptFromFinding 是执行器→复现晋升门的关键桥梁：
// 只有无 Repro 配方的 finding 才生成 Attempt（其余不可复现，门会拒收）。

func TestAttemptFromFinding_WithRepro(t *testing.T) {
	f := finding.VulnFinding{
		ID:            "f-1",
		TaskID:        "task-1",
		Host:          "target.com",
		Severity:      "high",
		Summary:       "SQL 注入",
		CWEID:         "CWE-89",
		OWASPCategory: "A03:2021",
		Target:        json.RawMessage(`{"path":"/login"}`),
		Repro:         json.RawMessage(`{"traffic_id":42,"modifications":{},"assert":{"status":200}}`),
	}

	attempt, ok, err := AttemptFromFinding("task-1", f)
	require.NoError(t, err)
	require.True(t, ok, "有 Repro 的 finding 应生成 Attempt")

	assert.Equal(t, "task-1", attempt.TaskID)
	assert.Equal(t, "result", string(attempt.Kind))
	assert.Equal(t, f.Repro, attempt.Primitives, "复现配方原样透传给 Verifier")
	assert.Equal(t, "high", attempt.Priority)

	var content struct {
		Type      string `json:"type"`
		FindingID string `json:"finding_id"`
		Severity  string `json:"severity"`
		Summary   string `json:"summary"`
		CWEID     string `json:"cwe_id"`
		TargetRef struct {
			Domain  string `json:"domain"`
			RefKind string `json:"ref_kind"`
			Locator string `json:"locator"`
		} `json:"target_ref"`
	}
	require.NoError(t, json.Unmarshal(attempt.Content, &content))
	assert.Equal(t, "vulnerability", content.Type)
	assert.Equal(t, "f-1", content.FindingID)
	assert.Equal(t, "high", content.Severity)
	assert.Equal(t, "web", content.TargetRef.Domain)
	assert.Equal(t, "endpoint", content.TargetRef.RefKind)
	assert.Equal(t, "target.com/login", content.TargetRef.Locator, "host+path 拼接为 locator")
}

func TestAttemptFromFinding_WithoutRepro(t *testing.T) {
	for _, repro := range []json.RawMessage{nil, json.RawMessage("{}")} {
		f := finding.VulnFinding{ID: "f-1", TaskID: "task-1", Host: "h", Severity: "high", Summary: "s", Repro: repro}
		attempt, ok, err := AttemptFromFinding("task-1", f)
		require.NoError(t, err)
		assert.False(t, ok, "无复现配方的 finding 应跳过（不是错误）")
		assert.Empty(t, attempt.TaskID)
		assert.Empty(t, attempt.Primitives)
	}
}

func TestAttemptFromFinding_PathWithoutSlash(t *testing.T) {
	f := finding.VulnFinding{
		ID: "f-1", TaskID: "task-1", Host: "target.com", Severity: "critical", Summary: "s",
		Target: json.RawMessage(`{"path":"admin/panel"}`),
		Repro:  json.RawMessage(`{"traffic_id":1}`),
	}
	attempt, ok, err := AttemptFromFinding("task-1", f)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "critical", attempt.Priority)

	var content struct {
		TargetRef struct{ Locator string } `json:"target_ref"`
	}
	require.NoError(t, json.Unmarshal(attempt.Content, &content))
	assert.Equal(t, "target.com/admin/panel", content.TargetRef.Locator, "path 缺前导斜杠时自动补")
}

func TestSeverityToPriority(t *testing.T) {
	cases := map[string]string{
		"critical": "critical",
		"HIGH":     "high", // 大小写不敏感
		"medium":   "medium",
		"low":      "low",
		"info":     "low",    // info 降级为 low
		"":         "medium", // 未知退化 medium
		"bogus":    "medium",
	}
	for sev, want := range cases {
		assert.Equal(t, want, severityToPriority(sev), "severity %q", sev)
	}
}
