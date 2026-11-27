package finding

import (
	"slices"
	"strings"
)

// SeverityMedium 是严重度的缺省档：交付没有给出评级、或给出的不在词表里时用它。
const SeverityMedium = "medium"

// Severities 是发现严重度的五档（由重到轻），与库里的 CHECK 约束 finding_severity_check
// 一致：人工处置只能取这几档，统计与概览按它计数。
var Severities = []string{"critical", "high", "medium", "low", "info"}

// NormalizeSeverity 把交付带来的评级收进词表：词表内的取值原样（大小写与空白从宽），
// 空值与词表之外的归缺省档。坐实了的主张不能因为评级写得不对而进不了台账。
func NormalizeSeverity(raw string) string {
	severity := strings.ToLower(strings.TrimSpace(raw))
	if slices.Contains(Severities, severity) {
		return severity
	}
	return SeverityMedium
}
