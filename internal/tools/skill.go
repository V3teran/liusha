package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/registry"
)

// ─── read_tooling_skill ──────────────────────────────────────────────────────

var readToolingSkillSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "tool_name": {"type": "string", "description": "CLI 工具名，如 sqlmap / ffuf / nuclei。"}
  },
  "required": ["tool_name"]
}`)

type readToolingSkillTool struct{ deps Deps }

func (t *readToolingSkillTool) Name() string      { return "read_tooling_skill" }
func (t *readToolingSkillTool) ShortDesc() string { return "拉取外部 CLI 工具使用手册" }
func (t *readToolingSkillTool) Desc() string {
	return "拉取某个外部 CLI 工具的完整使用手册。"
}
func (t *readToolingSkillTool) Schema() json.RawMessage { return readToolingSkillSchema }

func (t *readToolingSkillTool) Execute(_ context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		ToolName string `json:"tool_name"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "read_tooling_skill: 解析参数失败: " + err.Error()}, nil
	}
	if a.ToolName == "" {
		return registry.ToolResult{Error: "read_tooling_skill: tool_name 必填"}, nil
	}

	card, err := t.deps.ToolingLoader.Load(a.ToolName)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("read_tooling_skill: 工具 %q 手册不存在: %v", a.ToolName, err)}, nil
	}
	return registry.ToolResult{Output: card.Body}, nil
}

// ─── read_vuln_skill ─────────────────────────────────────────────────────────

var readVulnSkillSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "vuln_type": {"type": "string", "description": "漏洞类型名，如 sqli / ssrf / xss / idor。"}
  },
  "required": ["vuln_type"]
}`)

type readVulnSkillTool struct{ deps Deps }

func (t *readVulnSkillTool) Name() string      { return "read_vuln_skill" }
func (t *readVulnSkillTool) ShortDesc() string { return "拉取漏洞挖掘指南" }
func (t *readVulnSkillTool) Desc() string {
	return "拉取某个漏洞类型的完整挖掘指南。"
}
func (t *readVulnSkillTool) Schema() json.RawMessage { return readVulnSkillSchema }

func (t *readVulnSkillTool) Execute(_ context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		VulnType string `json:"vuln_type"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "read_vuln_skill: 解析参数失败: " + err.Error()}, nil
	}
	if a.VulnType == "" {
		return registry.ToolResult{Error: "read_vuln_skill: vuln_type 必填"}, nil
	}

	card, err := t.deps.VulnLoader.Load(a.VulnType)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("read_vuln_skill: 漏洞类型 %q 指南不存在: %v", a.VulnType, err)}, nil
	}
	return registry.ToolResult{Output: card.Body}, nil
}
