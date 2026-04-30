package actions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/tool"
)

// MemoryStore 是 engagement memory 三层（facts / ideas / hints）的最小访问接口。
//
// 由 internal/engagement.Store 自动满足（plan 1 part2 T6）。这里以接口形式声明而非
// 直接依赖具体类型，是为了：
//  1. 单元测试可注入 fake；
//  2. 未来若 memory 后端切换（如 Redis 缓冲），调用方无需改动。
type MemoryStore interface {
	// ReadState 一次返回 {facts, ideas, hints} 三层合并后的 JSON 字节流。
	ReadState(ctx context.Context, engagementID string) ([]byte, error)
	// AppendFact 追加一条事实条目（仅追加，不修改）。
	AppendFact(ctx context.Context, engagementID string, entry []byte) error
	// AppendIdea 追加一条假设条目。
	AppendIdea(ctx context.Context, engagementID string, entry []byte) error
	// AppendHint 追加一条提示条目。
	AppendHint(ctx context.Context, engagementID string, entry []byte) error
}

// ReadState — 一次读取 memory 三层（facts/ideas/hints）。
type ReadState struct {
	Store        MemoryStore
	EngagementID string
}

// Name 返回动作名 "read_state"。
func (a *ReadState) Name() string { return "read_state" }

// Description 提供给 LLM 的简介。
func (a *ReadState) Description() string {
	return "读取 engagement memory 三层（facts/ideas/hints）合并后的 JSON 状态"
}

// ParametersJSON 返回空对象 schema：read_state 不需要参数。
func (a *ReadState) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

// Execute 调 Store.ReadState 并把字节流原样塞进 Output。
func (a *ReadState) Execute(ctx context.Context, _ json.RawMessage) (tool.Result, error) {
	state, err := a.Store.ReadState(ctx, a.EngagementID)
	if err != nil {
		return tool.Result{}, fmt.Errorf("读取 memory 状态失败: %w", err)
	}
	return tool.Result{Output: state}, nil
}

// WriteFact — 追加一条事实（evidence 或 boundary）到 memory_facts。
//
// category 必须 ∈ {"evidence","boundary"}，否则报错；后端 engagement.Store 还会再做一次
// 校验（双层防御）。
type WriteFact struct {
	Store        MemoryStore
	EngagementID string
}

// Name 返回动作名 "write_fact"。
func (a *WriteFact) Name() string { return "write_fact" }

// Description 提供给 LLM 的简介。
func (a *WriteFact) Description() string {
	return "追加一条事实（category=evidence|boundary）到 memory_facts，只追加不修改"
}

// ParametersJSON 给出 category 枚举 + content 必填字段。
func (a *WriteFact) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "category":{"type":"string","enum":["evidence","boundary"]},
    "content":{"type":"string"}
  },
  "required":["category","content"]
}`)
}

// Execute 解析参数 → 校验 category → 序列化 entry → AppendFact。
func (a *WriteFact) Execute(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	var p struct {
		Category string `json:"category"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return tool.Result{}, fmt.Errorf("解析 write_fact 参数失败: %w", err)
	}
	if p.Category != "evidence" && p.Category != "boundary" {
		return tool.Result{}, fmt.Errorf("非法 category %q，必须是 evidence|boundary", p.Category)
	}
	if p.Content == "" {
		return tool.Result{}, fmt.Errorf("content 不能为空")
	}
	entry, _ := json.Marshal(map[string]any{"category": p.Category, "content": p.Content})
	if err := a.Store.AppendFact(ctx, a.EngagementID, entry); err != nil {
		return tool.Result{}, fmt.Errorf("追加 fact 失败: %w", err)
	}
	return tool.Result{Output: json.RawMessage(`{"ok":true}`)}, nil
}

// WriteIdea — 追加/更新一条假设到 memory_ideas（status: pending|testing|verified|failed）。
type WriteIdea struct {
	Store        MemoryStore
	EngagementID string
}

// Name 返回动作名 "write_idea"。
func (a *WriteIdea) Name() string { return "write_idea" }

// Description 提供给 LLM 的简介。
func (a *WriteIdea) Description() string {
	return "追加/更新一条假设到 memory_ideas，status ∈ pending|testing|verified|failed"
}

// ParametersJSON 给出 direction + status 枚举字段。
func (a *WriteIdea) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "direction":{"type":"string"},
    "status":{"type":"string","enum":["pending","testing","verified","failed"]}
  },
  "required":["direction","status"]
}`)
}

// Execute 解析 → 校验 status 枚举 → AppendIdea。
func (a *WriteIdea) Execute(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	var p struct {
		Direction string `json:"direction"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return tool.Result{}, fmt.Errorf("解析 write_idea 参数失败: %w", err)
	}
	if p.Direction == "" {
		return tool.Result{}, fmt.Errorf("direction 不能为空")
	}
	switch p.Status {
	case "pending", "testing", "verified", "failed":
	default:
		return tool.Result{}, fmt.Errorf("非法 status %q，必须是 pending|testing|verified|failed", p.Status)
	}
	entry, _ := json.Marshal(map[string]any{"direction": p.Direction, "status": p.Status})
	if err := a.Store.AppendIdea(ctx, a.EngagementID, entry); err != nil {
		return tool.Result{}, fmt.Errorf("追加 idea 失败: %w", err)
	}
	return tool.Result{Output: json.RawMessage(`{"ok":true}`)}, nil
}

// hintPriorityDefault 是 priority 缺省值（中等优先级）。
const hintPriorityDefault = 5

// hintPriorityMin / Max 是合法 priority 范围（含端点），与 ParametersJSON schema 一致。
const (
	hintPriorityMin = 1
	hintPriorityMax = 10
)

// WriteHint — 追加一条提示到 memory_hints。
//
// 系统态主体（Observer / DoneValidator / Distill）也通过此 action 写入 hint，
// from_skill 字段用于区分来源。
type WriteHint struct {
	Store        MemoryStore
	EngagementID string
}

// Name 返回动作名 "write_hint"。
func (a *WriteHint) Name() string { return "write_hint" }

// Description 提供给 LLM 的简介。
func (a *WriteHint) Description() string {
	return "追加一条提示到 memory_hints（priority 1-10，越大越优先；缺省 5）"
}

// ParametersJSON 给出 from_skill+content 必填，priority 1-10 可选。
func (a *WriteHint) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "from_skill":{"type":"string"},
    "content":{"type":"string"},
    "priority":{"type":"integer","minimum":1,"maximum":10}
  },
  "required":["from_skill","content"]
}`)
}

// Execute 解析 → 校验 priority 范围 → AppendHint。priority=0 视为未填，落默认 5。
func (a *WriteHint) Execute(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	var p struct {
		FromSkill string `json:"from_skill"`
		Content   string `json:"content"`
		Priority  int    `json:"priority"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return tool.Result{}, fmt.Errorf("解析 write_hint 参数失败: %w", err)
	}
	if p.FromSkill == "" || p.Content == "" {
		return tool.Result{}, fmt.Errorf("from_skill 与 content 都不能为空")
	}
	if p.Priority == 0 {
		p.Priority = hintPriorityDefault
	}
	if p.Priority < hintPriorityMin || p.Priority > hintPriorityMax {
		return tool.Result{}, fmt.Errorf("priority %d 越界，须在 [%d,%d]",
			p.Priority, hintPriorityMin, hintPriorityMax)
	}
	entry, _ := json.Marshal(map[string]any{
		"from_skill": p.FromSkill,
		"content":    p.Content,
		"priority":   p.Priority,
	})
	if err := a.Store.AppendHint(ctx, a.EngagementID, entry); err != nil {
		return tool.Result{}, fmt.Errorf("追加 hint 失败: %w", err)
	}
	return tool.Result{Output: json.RawMessage(`{"ok":true}`)}, nil
}
