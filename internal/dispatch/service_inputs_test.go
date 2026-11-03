package dispatch

import (
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/finding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRenderFindingsSummary_SeverityOrderAndCap 问答摘要的 top-k 纪律：
// 严重度降序（同级保持传入序），截断只丢低危，溢出计数行显式留痕。
func TestRenderFindingsSummary_SeverityOrderAndCap(t *testing.T) {
	fs := make([]finding.Finding, 0, qaFindingsTopN+5)
	fs = append(fs,
		finding.Finding{Severity: "low", Summary: "低危"},
		finding.Finding{Severity: "critical", Summary: "严重"},
		finding.Finding{Severity: "info", Summary: "信息"},
	)
	// 再补 22 条 medium：总数 25 > top 20，medium 的 2 条最旧被截
	for i := 0; i < 22; i++ {
		fs = append(fs, finding.Finding{Severity: "medium", Summary: "中危"})
	}

	got := renderFindingsSummary(fs)

	// 严重度降序：critical 先于 medium，medium 先于 low/info（low/info 被截）
	assert.True(t, strings.Contains(got, "1. [critical] 严重"))
	assert.Less(t, strings.Index(got, "[critical]"), strings.Index(got, "[medium]"),
		"critical 必须排在 medium 之前")
	assert.NotContains(t, got, "[low]", "低危应被 top-k 截断")
	assert.NotContains(t, got, "[info]", "info 应被 top-k 截断")
	assert.Contains(t, got, "另有 5 条较低严重度的 finding", "溢出必须计数留痕")

	lines := strings.Split(strings.TrimSpace(got), "\n")
	require.Equal(t, qaFindingsTopN+1, len(lines), "top-20 + 溢出行")
}

// TestRenderFindingsSummary_EmptyAndSmall 空黑板与小量成果不截断。
func TestRenderFindingsSummary_EmptyAndSmall(t *testing.T) {
	assert.Equal(t, "（暂无 finding）", renderFindingsSummary(nil))
	got := renderFindingsSummary([]finding.Finding{{Severity: "HIGH", Summary: "大写等级"}})
	assert.Contains(t, got, "[HIGH] 大写等级")
	assert.NotContains(t, got, "另有")
}

// TestSeverityRank 自由文本等级定序（前缀、大小写不敏感、未知兜底）。
func TestSeverityRank(t *testing.T) {
	assert.Equal(t, 5, severityRank("Critical"))
	assert.Equal(t, 4, severityRank("high"))
	assert.Equal(t, 3, severityRank("MEDIUM risk"))
	assert.Equal(t, 2, severityRank("low"))
	assert.Equal(t, 1, severityRank("informational"))
	assert.Equal(t, 0, severityRank("离谱等级"))
}

// TestValidateBrief 入口契约：超长显式拒绝（指令不可静默截断），限内放行。
func TestValidateBrief(t *testing.T) {
	require.NoError(t, validateBrief(strings.Repeat("测", maxBriefRunes)))
	err := validateBrief(strings.Repeat("测", maxBriefRunes+1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "任务描述过长")
}
