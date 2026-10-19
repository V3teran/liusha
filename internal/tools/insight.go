package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/insight"
	"github.com/V3teran/liusha/internal/registry"
)

// ─── read_insights ───────────────────────────────────────────────────────────

var readInsightsSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "category": {
      "type": "string",
      "enum": ["entity", "access", "structure", "behavior", "issue", "note"],
      "description": "过滤类别（可选）"
    },
    "priority": {
      "type": "string",
      "enum": ["critical", "high", "medium", "low"],
      "description": "最低优先级（可选，例如 high 会返回 high 和 critical）"
    },
    "confidence": {
      "type": "string",
      "enum": ["confirmed", "probable", "possible"],
      "description": "最低置信度（可选，例如 probable 会返回 probable 和 confirmed）"
    },
    "tags": {
      "type": "array",
      "items": {"type": "string"},
      "description": "标签过滤（可选，返回包含任一标签的洞察）"
    },
    "limit": {
      "type": "integer",
      "description": "最多返回条数，默认 20，最大 100"
    }
  }
}`)

type readInsightsTool struct {
	registry.BaseTool
	deps Deps
}

func newReadInsightsTool(deps Deps, timeout time.Duration) *readInsightsTool {
	t := &readInsightsTool{deps: deps}
	t.SetTimeout(timeout)
	return t
}

func (t *readInsightsTool) Name() string { return "read_insights" }
func (t *readInsightsTool) ShortDesc() string {
	return "查询黑板上的协作情报"
}
func (t *readInsightsTool) Desc() string {
	return "从黑板读取其他 agent 写入的情报，用于了解目标信息、可用凭证、基础设施发现、业务逻辑、遇到的障碍等。"
}
func (t *readInsightsTool) Schema() json.RawMessage { return readInsightsSchema }

func (t *readInsightsTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Category   string   `json:"category"`
		Priority   string   `json:"priority"`
		Confidence string   `json:"confidence"`
		Tags       []string `json:"tags"`
		Limit      int      `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "read_insights: 解析参数失败: " + err.Error()}, nil
	}

	if a.Limit <= 0 {
		a.Limit = 20
	}
	if a.Limit > 100 {
		a.Limit = 100
	}

	if t.deps.Insights == nil {
		return registry.ToolResult{Error: "read_insights: Insights 未配置"}, nil
	}

	// 黑板按 assignment 归档：write_insight 以 task.AssignmentID 落库，
	// 读取必须解析同一个 assignment（曾误用 TaskID 过滤，永远查空）。
	if t.deps.Tasks == nil {
		return registry.ToolResult{Error: "read_insights: Tasks 未配置"}, nil
	}
	task, err := t.deps.Tasks.GetByID(ctx, t.deps.TaskID)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("read_insights: 查询 task 失败: %v", err)}, nil
	}

	var insights []insight.Insight
	switch {
	case a.Category != "":
		insights, err = t.deps.Insights.ListByCategory(ctx, task.AssignmentID, insight.Category(a.Category), a.Limit*2)
	case a.Priority != "":
		insights, err = t.deps.Insights.ListByPriority(ctx, task.AssignmentID, insight.Priority(a.Priority), a.Limit*2)
	default:
		insights, err = t.deps.Insights.List(ctx, task.AssignmentID, a.Limit*2)
	}
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("read_insights: %v", err)}, nil
	}

	// 后置过滤
	filtered := insights
	if a.Confidence != "" {
		filtered = filterByConfidence(filtered, insight.Confidence(a.Confidence))
	}
	if len(a.Tags) > 0 {
		filtered = filterByTags(filtered, a.Tags)
	}

	// 限制结果数量
	if len(filtered) > a.Limit {
		filtered = filtered[:a.Limit]
	}

	if len(filtered) == 0 {
		return registry.ToolResult{Output: "未找到匹配的洞察。"}, nil
	}

	// 格式化输出
	var sb strings.Builder
	fmt.Fprintf(&sb, "找到 %d 条洞察：\n\n", len(filtered))
	for i, ins := range filtered {
		fmt.Fprintf(&sb, "【%d】%s [%s | %s | %s]\n", i+1, ins.Summary, ins.Category, ins.Priority, ins.Confidence)
		if ins.Body != "" {
			fmt.Fprintf(&sb, "    %s\n", ins.Body)
		}
		if len(ins.Tags) > 0 {
			fmt.Fprintf(&sb, "    标签: %s\n", strings.Join(ins.Tags, ", "))
		}
		sb.WriteString("\n")
	}

	return registry.ToolResult{Output: sb.String()}, nil
}

// filterByConfidence 按最低置信度过滤（返回 >= minConfidence 的洞察）
func filterByConfidence(insights []insight.Insight, minConfidence insight.Confidence) []insight.Insight {
	order := map[insight.Confidence]int{
		insight.ConfidenceConfirmed: 3,
		insight.ConfidenceProbable:  2,
		insight.ConfidencePossible:  1,
	}
	minLevel := order[minConfidence]
	if minLevel == 0 {
		return insights
	}

	var result []insight.Insight
	for _, ins := range insights {
		if order[ins.Confidence] >= minLevel {
			result = append(result, ins)
		}
	}
	return result
}

// filterByTags 按标签过滤（返回包含任一标签的洞察）
func filterByTags(insights []insight.Insight, tags []string) []insight.Insight {
	tagSet := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		tagSet[t] = struct{}{}
	}

	var result []insight.Insight
	for _, ins := range insights {
		for _, t := range ins.Tags {
			if _, ok := tagSet[t]; ok {
				result = append(result, ins)
				break
			}
		}
	}
	return result
}

// ─── write_insight ───────────────────────────────────────────────────────────

var writeLeadSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "category": {
      "type": "string",
      "enum": ["entity", "access", "structure", "behavior", "issue", "note"],
      "description": "信息分类：entity（实体）/access（访问）/structure（结构）/behavior（行为）/issue（问题）/note（备注）"
    },
    "priority": {
      "type": "string",
      "enum": ["critical", "high", "medium", "low"],
      "default": "medium",
      "description": "优先级：critical（关键，P0）/high（高，P1）/medium（中，P2）/low（低，P3）"
    },
    "confidence": {
      "type": "string",
      "enum": ["possible", "probable", "confirmed"],
      "default": "possible",
      "description": "置信度：possible（可能）/probable（很可能）/confirmed（已确认）"
    },
    "severity": {
      "type": "string",
      "enum": ["low", "medium", "high", "critical"],
      "description": "严重性（仅 category=issue 时使用）：low（低）/medium（中）/high（高）/critical（严重）"
    },
    "summary": {
      "type": "string",
      "maxLength": 200,
      "description": "一句话摘要（必填，200 字符以内）"
    },
    "body": {
      "type": "string",
      "description": "详细内容（可选，markdown 格式）"
    },
    "tags": {
      "type": "array",
      "items": {"type": "string"},
      "description": "自由标签（可选）"
    },
    "related_nodes": {
      "type": "array",
      "items": {"type": "string"},
      "description": "关联的探索图节点 ID（observation/result/action），用于追溯情报来源"
    }
  },
  "required": ["category", "summary"]
}`)

type writeInsightTool struct {
	registry.BaseTool
	deps Deps
}

func newWriteInsightTool(deps Deps, timeout time.Duration) *writeInsightTool {
	t := &writeInsightTool{deps: deps}
	t.SetTimeout(timeout)
	return t
}

func (t *writeInsightTool) Name() string      { return "write_insight" }
func (t *writeInsightTool) ShortDesc() string { return "写一条跨 task 情报" }
func (t *writeInsightTool) Desc() string {
	return "写一条情报到 assignment 级别的黑板（同一批测试的多个 task 共享，跨 agent 可见）。"
}
func (t *writeInsightTool) Schema() json.RawMessage { return writeLeadSchema }

func (t *writeInsightTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Category     string   `json:"category"`
		Priority     string   `json:"priority"`
		Confidence   string   `json:"confidence"`
		Severity     string   `json:"severity"`
		Summary      string   `json:"summary"`
		Body         string   `json:"body"`
		Tags         []string `json:"tags"`
		RelatedNodes []string `json:"related_nodes"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "write_insight: 解析参数失败: " + err.Error()}, nil
	}
	if a.Summary == "" {
		return registry.ToolResult{Error: "write_insight: summary 必填"}, nil
	}
	if a.Category == "" {
		return registry.ToolResult{Error: "write_insight: category 必填"}, nil
	}

	// 默认值
	if a.Priority == "" {
		a.Priority = "medium"
	}
	if a.Confidence == "" {
		a.Confidence = "possible"
	}

	// 查询当前 task 所属的 assignment_id
	task, err := t.deps.Tasks.GetByID(ctx, t.deps.TaskID)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("write_insight: 查询 task 失败: %v", err)}, nil
	}

	entry := insight.Insight{
		Category:         normalizeCategory(a.Category),
		Priority:         normalizePriority(a.Priority),
		Confidence:       normalizeConfidence(a.Confidence),
		Summary:          a.Summary,
		Body:             a.Body,
		Tags:             a.Tags,
		RelatedNodes:     a.RelatedNodes,
		SourceTaskID:     t.deps.TaskID,
		SourceAgentRunID: t.deps.AgentRunID,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	// severity 仅对 category=issue 有意义
	if a.Severity != "" && entry.Category == insight.CategoryIssue {
		normalizedSeverity := normalizeSeverity(a.Severity)
		entry.Severity = &normalizedSeverity
	}

	if err := t.deps.Insights.Append(ctx, task.AssignmentID, entry); err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("write_insight: %v", err)}, nil
	}

	outputMsg := fmt.Sprintf("情报已写入黑板: [%s/%s/%s] %s", a.Category, a.Priority, a.Confidence, a.Summary)
	if entry.Severity != nil {
		outputMsg = fmt.Sprintf("情报已写入黑板: [%s/%s/%s/severity=%s] %s", a.Category, a.Priority, a.Confidence, *entry.Severity, a.Summary)
	}

	return registry.ToolResult{Output: outputMsg}, nil
}

// normalizeCategory/normalizePriority/normalizeConfidence/normalizeSeverity 把 LLM 自由输出归一到
// insight 表 CHECK 约束的枚举集——越界值直接撞 DB 约束只会把报错抛回 LLM。
func normalizeCategory(s string) insight.Category {
	switch insight.Category(s) {
	case insight.CategoryEntity, insight.CategoryAccess, insight.CategoryStructure,
		insight.CategoryBehavior, insight.CategoryIssue, insight.CategoryNote:
		return insight.Category(s)
	default:
		return insight.CategoryNote
	}
}

func normalizePriority(s string) insight.Priority {
	switch insight.Priority(s) {
	case insight.PriorityCritical, insight.PriorityHigh, insight.PriorityMedium, insight.PriorityLow:
		return insight.Priority(s)
	default:
		return insight.PriorityMedium
	}
}

func normalizeConfidence(s string) insight.Confidence {
	switch insight.Confidence(s) {
	case insight.ConfidencePossible, insight.ConfidenceProbable, insight.ConfidenceConfirmed:
		return insight.Confidence(s)
	default:
		return insight.ConfidencePossible
	}
}

func normalizeSeverity(s string) string {
	switch s {
	case "low", "medium", "high", "critical":
		return s
	default:
		return "medium"
	}
}
