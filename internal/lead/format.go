package lead

import (
	"fmt"
	"strings"
)

// sectionOrder 是渲染顺序：clue（待验证）→ fact（已确认）→ deadend（勿重试），
// 与 §7.3 触发动作的自然阅读顺序一致（先看要不要去验证，再看已知事实，最后避坑）。
var sectionOrder = []struct {
	kind  Kind
	label string
}{
	{KindClue, "待验证线索（clue）"},
	{KindFact, "已确认事实（fact）"},
	{KindDeadend, "死路，勿重试（deadend）"},
}

// FormatSection 把 ReadRecent 的分组结果渲染成 prompt 用的 markdown 段。
// grouped 全空（无任何情报）时返回空串，不污染 prompt。
//
// 供两处复用：顶层 agent（orchestrator/passive）经 BuildUserPrompt 注入只读段；
// 子代理（recon/exploitation）经 BuildDeepSwarm 拼进 system prompt 的固定段（§7.5）。
func FormatSection(grouped map[Kind][]Entry) string {
	total := 0
	for _, es := range grouped {
		total += len(es)
	}
	if total == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## 情报黑板（本 host 已知情报，跨 agent / 跨次扫描共享）\n")
	for _, sec := range sectionOrder {
		entries := grouped[sec.kind]
		if len(entries) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n### %s\n\n", sec.label)
		for _, e := range entries {
			b.WriteString("- " + e.Note)
			if e.SourceTaskID != "" {
				fmt.Fprintf(&b, "（来自 task %s）", e.SourceTaskID)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}
