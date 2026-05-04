package common

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/toolfx"
)

// MemoryStore 是 engagement memory（notes 单层）的最小访问接口。
//
// 由 internal/engagement.Store 自动满足。
//
// v1.2 收尾：原 facts/ideas 双层合成单一 notes（kind enum 区分 observation/hypothesis/boundary）；
// 删除 AppendFact/AppendIdea/AppendHint 三个旧方法。
type MemoryStore interface {
	ReadState(ctx context.Context, engagementID string) ([]byte, error)
	ReadStateScoped(ctx context.Context, engagementID string, opts engagement.ReadOpts) ([]byte, error)
	AppendNote(ctx context.Context, engagementID string, entry []byte) error
}

// scopeEngagement 是 entry jsonb 中 scope 字段的固定值（v1.2 收尾后 notes 永远 engagement-scope）。
const scopeEngagement = "engagement"

// noteKindObservation/Hypothesis/Boundary 是 take_note 工具 kind 字段的合法枚举。
const (
	NoteKindObservation = "observation"
	NoteKindHypothesis  = "hypothesis"
	NoteKindBoundary    = "boundary"
)

// validNoteKinds 用于 enum 校验。
var validNoteKinds = map[string]struct{}{
	NoteKindObservation: {},
	NoteKindHypothesis:  {},
	NoteKindBoundary:    {},
}

// validHypothesisStatus 是 hypothesis kind 时 status 字段的合法枚举。
var validHypothesisStatus = map[string]struct{}{
	"pending":  {},
	"testing":  {},
	"verified": {},
	"failed":   {},
}

// ReadState — 一次读取 engagement memory_notes（带 NotesLimit 截断）。
//
// TaskID 字段保留为接口对称性使用，当前不参与过滤——notes 是 engagement-scope 共享，
// 所有 task 都能看到所有 notes；done_validator 凭 entry.task_id 判定本 task 是否写过。
type ReadState struct {
	Store        MemoryStore
	EngagementID string
	TaskID       string // 保留字段（不再用于 scope 过滤；done_validator 自查 entry.task_id）
}

// Name 返回动作名 "read_state"。
func (a *ReadState) Name() string { return "read_state" }

// Description 提供给 LLM 的简介。
func (a *ReadState) Description() string {
	return "读取 engagement memory_notes（同 host 跨 task 共享的工作笔记/假设/边界）"
}

// ParametersJSON 返回空对象 schema。
func (a *ReadState) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

// Execute 调 Store.ReadStateScoped 并把字节流原样塞进 Output。
func (a *ReadState) Execute(ctx context.Context, _ json.RawMessage) (toolfx.Result, error) {
	state, err := a.Store.ReadStateScoped(ctx, a.EngagementID, engagement.ReadOpts{TaskID: a.TaskID})
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("读取 memory 状态失败: %w", err)
	}
	return toolfx.Result{Output: state}, nil
}

// TakeNote — LLM 主动留笔记到 engagement memory_notes。
//
// 三种 kind：
//   - observation: 看到的事实/证据（替代旧 evidence）
//   - hypothesis:  探索假设/方向（带 status: pending|testing|verified|failed）
//   - boundary:    观察到的边界条件（替代旧 boundary）
//
// scope 永远 engagement（per host 跨 task 共享）；entry 自带 task_id 标记写入者，
// done_validator 凭它判断本 task 是否真写过。
type TakeNote struct {
	Store        MemoryStore
	EngagementID string
	TaskID       string
}

// Name 返回动作名 "take_note"。
func (a *TakeNote) Name() string { return "take_note" }

// Description 提供给 LLM 的简介。
func (a *TakeNote) Description() string {
	return "记一条工作笔记到 engagement memory_notes（同 host 跨 task 共享）：observation=证据，hypothesis=假设，boundary=边界"
}

// ParametersJSON 给出 kind 枚举 + content 必填 + status 可选枚举。
func (a *TakeNote) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "kind":{"type":"string","enum":["observation","hypothesis","boundary"]},
    "content":{"type":"string"},
    "status":{"type":"string","enum":["pending","testing","verified","failed"],"description":"仅 kind=hypothesis 时使用"}
  },
  "required":["kind","content"]
}`)
}

// Execute 解析 → 校验 enum → 序列化 entry → AppendNote。
func (a *TakeNote) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var p struct {
		Kind    string `json:"kind"`
		Content string `json:"content"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 take_note 参数失败: %w", err)
	}
	if _, ok := validNoteKinds[p.Kind]; !ok {
		return toolfx.Result{}, fmt.Errorf("非法 kind %q，必须是 observation|hypothesis|boundary", p.Kind)
	}
	if p.Content == "" {
		return toolfx.Result{}, fmt.Errorf("content 不能为空")
	}
	if p.Status != "" {
		if _, ok := validHypothesisStatus[p.Status]; !ok {
			return toolfx.Result{}, fmt.Errorf("非法 status %q，必须是 pending|testing|verified|failed", p.Status)
		}
		if p.Kind != NoteKindHypothesis {
			return toolfx.Result{}, fmt.Errorf("status 仅 kind=hypothesis 时可填，当前 kind=%q", p.Kind)
		}
	}

	entryMap := map[string]any{
		"kind":    p.Kind,
		"content": p.Content,
		"scope":   scopeEngagement,
		"task_id": a.TaskID,
	}
	if p.Status != "" {
		entryMap["status"] = p.Status
	}
	entry, _ := json.Marshal(entryMap)

	if err := a.Store.AppendNote(ctx, a.EngagementID, entry); err != nil {
		return toolfx.Result{}, fmt.Errorf("追加 note 失败: %w", err)
	}
	return toolfx.Result{Output: json.RawMessage(`{"ok":true}`)}, nil
}
