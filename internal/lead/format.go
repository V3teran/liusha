package lead

import (
	"fmt"
	"strings"
)

// categoryOrder 是分类的渲染顺序（重要的在前）
var categoryOrder = []Category{
	CategoryFinding,        // 发现（最重要）
	CategoryCredential,     // 凭证
	CategoryTarget,         // 目标
	CategoryInfrastructure, // 基础设施
	CategoryBusiness,       // 业务逻辑
	CategoryData,           // 数据特征
	CategoryObstacle,       // 障碍
	CategoryNote,           // 笔记（最不重要）
}

// FormatSection 把 ReadRecent 的分组结果渲染成 prompt 用的 markdown 段。
// grouped 全空（无任何情报）时返回空串，不污染 prompt。
//
// 供两处复用：顶层 agent（planner/passive）经 BuildUserPrompt 注入只读段；
// 子代理（recon/exploitation）经 BuildDeepSwarm 拼进 system prompt 的固定段。
func FormatSection(grouped map[Priority]map[Category][]Entry) string {
	// 统计总数
	total := 0
	for _, cats := range grouped {
		for _, entries := range cats {
			total += len(entries)
		}
	}
	if total == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## 情报黑板（assignment 共享）\n")

	// 按优先级顺序渲染（critical → high → medium → low）
	for _, priority := range []Priority{PriorityCritical, PriorityHigh, PriorityMedium, PriorityLow} {
		cats, ok := grouped[priority]
		if !ok || len(cats) == 0 {
			continue
		}

		// 优先级标题
		b.WriteString("\n### ")
		b.WriteString(priorityIcon(priority))
		b.WriteString(" ")
		b.WriteString(priorityLabel(priority))
		b.WriteString("\n")

		// 按 category 顺序渲染
		for _, category := range categoryOrder {
			entries, ok := cats[category]
			if !ok || len(entries) == 0 {
				continue
			}

			// 分类标题
			fmt.Fprintf(&b, "\n#### %s\n\n", categoryLabel(category))

			// 渲染每条情报
			for _, e := range entries {
				renderEntry(&b, e)
			}
		}
	}

	return b.String()
}

// renderEntry 渲染单条情报
func renderEntry(b *strings.Builder, e Entry) {
	// 格式：- [置信度图标] 摘要 (来源: task-xxx) [tag1, tag2]
	icon := confidenceIcon(e.Confidence)
	fmt.Fprintf(b, "- %s %s", icon, e.Summary)

	// 来源
	if e.SourceTaskID != "" {
		taskShort := e.SourceTaskID
		if len(taskShort) > 8 {
			taskShort = taskShort[:8]
		}
		fmt.Fprintf(b, " `(来源: task-%s)`", taskShort)
	}

	// 标签
	if len(e.Tags) > 0 {
		fmt.Fprintf(b, " `[%s]`", strings.Join(e.Tags, ", "))
	}

	b.WriteString("\n")

	// 详细内容（缩进显示）
	if e.Body != "" {
		lines := strings.Split(strings.TrimSpace(e.Body), "\n")
		for _, line := range lines {
			if len(line) > 0 {
				fmt.Fprintf(b, "  %s\n", line)
			}
		}
	}
}

// priorityIcon 返回优先级图标
func priorityIcon(p Priority) string {
	switch p {
	case PriorityCritical:
		return "🔴"
	case PriorityHigh:
		return "🟠"
	case PriorityMedium:
		return "🟡"
	case PriorityLow:
		return "🟢"
	default:
		return "⚪"
	}
}

// priorityLabel 返回优先级标签
func priorityLabel(p Priority) string {
	switch p {
	case PriorityCritical:
		return "关键优先级"
	case PriorityHigh:
		return "高优先级"
	case PriorityMedium:
		return "中优先级"
	case PriorityLow:
		return "低优先级"
	default:
		return "未知优先级"
	}
}

// confidenceIcon 返回置信度图标
func confidenceIcon(c Confidence) string {
	switch c {
	case ConfidenceConfirmed:
		return "✓" // 已确认
	case ConfidenceProbable:
		return "◐" // 很可能
	case ConfidencePossible:
		return "?" // 可能
	default:
		return "•"
	}
}

// categoryLabel 返回分类标签
func categoryLabel(c Category) string {
	switch c {
	case CategoryTarget:
		return "目标信息"
	case CategoryCredential:
		return "凭证信息"
	case CategoryInfrastructure:
		return "基础设施"
	case CategoryBusiness:
		return "业务逻辑"
	case CategoryData:
		return "数据特征"
	case CategoryFinding:
		return "发现"
	case CategoryObstacle:
		return "障碍"
	case CategoryNote:
		return "笔记"
	default:
		return string(c)
	}
}
