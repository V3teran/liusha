package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/skill"
)

// read_skill 是 skill 渐进式加载的 Tier 2 通道：LLM 先在 system prompt 的
// skill 索引（Tier 1，仅 name+description）里按需选题，再经本工具拉完整正文。
// Reader 由装配层按 agent.skills 声明建白名单视图（View）——未声明的 skill
// 既不进 Tier 1 索引，也无法经本工具读取。

var readSkillSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "name": {"type": "string", "description": "skill 名，取 system prompt「技能索引」段列出的名字（如 dom-xss / bac / browser-use）。"}
  },
  "required": ["name"]
}`)

type readSkillTool struct {
	registry.BaseTool
	skills skill.Reader
}

func newReadSkillTool(skills skill.Reader, timeout time.Duration, safe bool) *readSkillTool {
	t := &readSkillTool{skills: skills}
	t.SetTimeout(timeout)
	t.SetConcurrencySafe(safe)
	return t
}

func (t *readSkillTool) Name() string { return "read_skill" }
func (t *readSkillTool) ShortDesc() string {
	return "拉取技能手册全文（漏洞指南/工具手册）"
}
func (t *readSkillTool) Desc() string {
	return "拉取一个 skill 的完整手册正文。可用 skill 名以 system prompt 的「技能索引」段为准——动手挖某类漏洞（如 dom-xss、bac）或用某个工具（如 browser-use）之前先读对应 skill。"
}
func (t *readSkillTool) Schema() json.RawMessage { return readSkillSchema }

func (t *readSkillTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "read_skill: 解析参数失败: " + err.Error()}, nil
	}
	if a.Name == "" {
		return registry.ToolResult{Error: "read_skill: name 必填"}, nil
	}

	card, err := t.skills.Load(ctx, a.Name)
	if err != nil {
		// 附上本视图可见的候选名，帮 LLM 一次纠偏（白名单外的名字在这里被拦下）。
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("read_skill: %v", err))
		if metas, mErr := t.skills.Metas(ctx); mErr == nil && len(metas) > 0 {
			sb.WriteString("；可用: ")
			for i, m := range metas {
				if i > 0 {
					sb.WriteString(", ")
				}
				if m.Key != "" {
					sb.WriteString(m.Key)
				} else {
					sb.WriteString(m.Name)
				}
			}
		}
		return registry.ToolResult{Error: sb.String()}, nil
	}
	return registry.ToolResult{Output: card.Body}, nil
}
