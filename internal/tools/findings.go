package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/registry"
)

// ─── read_findings ───────────────────────────────────────────────────────────

var readFindingsSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "host":  {"type": "string", "description": "目标 host（不填则用当前任务 host）。"},
    "limit": {"type": "integer", "description": "最多返回条数，默认 20。"}
  }
}`)

type readFindingsTool struct{ deps Deps }

func (t *readFindingsTool) Name() string      { return "read_findings" }
func (t *readFindingsTool) ShortDesc() string { return "列出本次扫描已有 finding" }
func (t *readFindingsTool) Desc() string {
	return "列出本次扫描已有 finding（写前必查、防重复），返回 id/severity/summary 摘要。"
}
func (t *readFindingsTool) Schema() json.RawMessage { return readFindingsSchema }

func (t *readFindingsTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Host  string `json:"host"`
		Limit int    `json:"limit"`
	}
	_ = json.Unmarshal(args, &a)
	if a.Host == "" {
		a.Host = t.deps.Host
	}
	if a.Limit <= 0 {
		a.Limit = 20
	}

	list, err := t.deps.Findings.ListByTaskAndHost(ctx, t.deps.TaskID, a.Host, a.Limit)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("read_findings: %v", err)}, nil
	}

	type row struct {
		ID       string `json:"id"`
		Severity string `json:"severity"`
		Summary  string `json:"summary"`
		Status   string `json:"status"`
		Seq      int64  `json:"seq"`
	}
	rows := make([]row, 0, len(list))
	for _, f := range list {
		rows = append(rows, row{ID: f.ID, Severity: f.Severity, Summary: f.Summary, Status: f.Status, Seq: f.Seq})
	}
	b, _ := json.Marshal(rows)
	return registry.ToolResult{Output: string(b)}, nil
}

// ─── write_finding ───────────────────────────────────────────────────────────

var writeFindingSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "summary":        {"type": "string", "description": "漏洞描述：是什么 / 怎么验证 / 推理依据。"},
    "severity":       {"type": "string", "description": "critical / high / medium / low / info。"},
    "evaluation":       {"type": "object", "description": "评估证据（payload/复现步骤/响应摘要）。"},
    "target":         {"type": "object", "description": "目标位置（url/endpoint/param）。"},
    "cwe_id":         {"type": "string", "description": "如 CWE-89。"},
    "owasp_category": {"type": "string", "description": "如 A03:2021。"},
    "remediation":    {"type": "string", "description": "修复建议。"},
    "depends_on":     {"type": "array", "items": {"type": "string"}, "description": "组合漏洞依赖的 finding ID。"}
  },
  "required": ["summary", "severity"]
}`)

type writeFindingTool struct{ deps Deps }

func (t *writeFindingTool) Name() string      { return "write_finding" }
func (t *writeFindingTool) ShortDesc() string { return "写一条新漏洞 finding" }
func (t *writeFindingTool) Desc() string {
	return "写一条新漏洞 finding：summary 短标题 + evidence 详情/复现/payload + severity。"
}
func (t *writeFindingTool) Schema() json.RawMessage { return writeFindingSchema }

func (t *writeFindingTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Summary       string          `json:"summary"`
		Severity      string          `json:"severity"`
		Evaluation json.RawMessage `json:"evaluation"`
		Target        json.RawMessage `json:"target"`
		CWEID         string          `json:"cwe_id"`
		OWASPCategory string          `json:"owasp_category"`
		Remediation   string          `json:"remediation"`
		DependsOn     []string        `json:"depends_on"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "write_finding: 解析参数失败: " + err.Error()}, nil
	}
	if a.Summary == "" {
		return registry.ToolResult{Error: "write_finding: summary 必填"}, nil
	}
	if a.Severity == "" {
		return registry.ToolResult{Error: "write_finding: severity 必填"}, nil
	}

	opID := t.deps.AgentID
	f := finding.VulnFinding{
		TaskID:        t.deps.TaskID,
		ExecutorID:    &opID,
		Host:          t.deps.Host,
		Severity:      a.Severity,
		Summary:       a.Summary,
		Evaluation:      a.Evaluation,
		Target:        a.Target,
		CWEID:         a.CWEID,
		OWASPCategory: a.OWASPCategory,
		Remediation:   a.Remediation,
		DependsOn:     a.DependsOn,
	}

	saved, err := t.deps.Findings.Save(ctx, f)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("write_finding: %v", err)}, nil
	}
	return registry.ToolResult{
		Output: fmt.Sprintf("finding 已写入: id=%s severity=%s", saved.ID, saved.Severity),
		Signal: &registry.Signal{
			Kind:     registry.SignalCmdOutput,
			ToolName: "write_finding",
			Content:  fmt.Sprintf("[%s] %s (id=%s)", saved.Severity, saved.Summary, saved.ID),
		},
	}, nil
}

// ─── update_finding ──────────────────────────────────────────────────────────

var updateFindingSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "id":       {"type": "string", "description": "Finding ID。"},
    "summary":  {"type": "string", "description": "更新 summary（可选）。"},
    "severity": {"type": "string", "description": "更新 severity（可选）。"},
    "evaluation": {"type": "object", "description": "更新 evaluation（可选）。"},
    "target":   {"type": "object", "description": "更新 target（可选）。"},
    "depends_on": {"type": "array", "items": {"type": "string"}}
  },
  "required": ["id"]
}`)

type updateFindingTool struct{ deps Deps }

func (t *updateFindingTool) Name() string      { return "update_finding" }
func (t *updateFindingTool) ShortDesc() string { return "更新已有 finding" }
func (t *updateFindingTool) Desc() string {
	return "更新已有 finding（保留首次发现时间，仅覆盖所传字段），用于补强 PoC/payload/severity。"
}
func (t *updateFindingTool) Schema() json.RawMessage { return updateFindingSchema }

func (t *updateFindingTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		ID        string          `json:"id"`
		Summary   string          `json:"summary"`
		Severity  string          `json:"severity"`
		Evaluation json.RawMessage `json:"evaluation"`
		Target    json.RawMessage `json:"target"`
		DependsOn []string        `json:"depends_on"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "update_finding: 解析参数失败: " + err.Error()}, nil
	}
	if a.ID == "" {
		return registry.ToolResult{Error: "update_finding: id 必填"}, nil
	}

	if err := t.deps.Findings.Update(ctx, a.ID, a.Summary, a.Severity, a.Target, a.Evaluation, a.DependsOn); err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("update_finding: %v", err)}, nil
	}
	return registry.ToolResult{Output: fmt.Sprintf("finding 已更新: id=%s", a.ID)}, nil
}
