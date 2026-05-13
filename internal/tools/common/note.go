package common

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// NoteStore 是 engagement notes 黑板的最小访问接口。
// 由 *engagement.Store 自动满足。
type NoteStore interface {
	ReadNotesScoped(ctx context.Context, engagementID string, opts engagement.ReadOpts) ([]byte, error)
	AppendNote(ctx context.Context, engagementID string, entry []byte) error
}

// ReadNote — 一次读取 engagement notes 共享黑板（短期记忆，本次扫描内）。
type ReadNote struct {
	Store        NoteStore
	EngagementID string
	TaskID       string
}

// Name 返回工具名 "read_note"。
func (a *ReadNote) Name() string { return "read_note" }

// Description 提供给 LLM 的简介。
func (a *ReadNote) Description() string {
	return "读取本次扫描（engagement）共享笔记板——与同 host 其他 hunter task 共享的过程性事实。" +
		"读到的内容包括：本次拿到的临时凭据/状态、目标实例当前怪癖、扫描中发现的小惊喜、失败死路。" +
		"engagement 关闭即过期，不跨次扫描。"
}

// ParametersJSON 返回空对象 schema。
func (a *ReadNote) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

// Execute 调 Store.ReadNotesScoped 并返回字节流。
func (a *ReadNote) Execute(ctx context.Context, _ json.RawMessage) (toolfx.Result, error) {
	state, err := a.Store.ReadNotesScoped(ctx, a.EngagementID, engagement.ReadOpts{TaskID: a.TaskID})
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("读取 note 失败: %w", err)
	}
	return toolfx.Result{Output: state}, nil
}

// WriteNote — 写一条短期记忆到 engagement notes 共享黑板（纯文本追加）。
type WriteNote struct {
	Store        NoteStore
	EngagementID string
	TaskID       string
}

// Name 返回工具名 "write_note"。
func (a *WriteNote) Name() string { return "write_note" }

// Description 提供给 LLM 的简介。
//
// 设计意图：明确区分 finding / note / lesson 三类记忆——
//   - finding（结构化漏洞 PoC）
//   - note（本次扫描的过程性事实，短期）
//   - lesson（跨次扫描的长期经验）
//
// description 重点说"只能在 note 留痕的事"和"禁写"边界，防 LLM 把漏洞 PoC 误写进 note。
func (a *WriteNote) Description() string {
	return "写一条「本次扫描」内的过程性事实到 engagement 共享笔记板" +
		"（同 host 其他 hunter task 都能读到；engagement 关闭即过期，不跨次扫描）。" +
		"\n\n【必写】只能在 note 留痕的事：" +
		"\n- 临时凭据/状态：本次拿到的 session、cookie、token、有效凭证组合" +
		"\n- 目标实例当前怪癖：本 host 现在的 server 行为（如『强制 security=impossible 需 cookie 覆盖』）" +
		"\n- 小惊喜：扫描中发现的非漏洞但有价值的信号（待深挖的暴露端口、可疑 endpoint、奇怪报错、未来可能成为攻击面的线索）" +
		"\n- 失败死路：什么打法不通，避免后续 agent 重蹈" +
		"\n\n【禁写】请改用对应工具：" +
		"\n- 漏洞 PoC（具体可复现的漏洞）→ write_finding" +
		"\n- 通用经验（默认密码、工具调用 pattern、稳定的目标特性）→ write_lesson" +
		"\n- 已写入 finding 的内容（重复浪费 prompt 字数）"
}

// ParametersJSON 给出 content 必填 schema。
func (a *WriteNote) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "content":{"type":"string","minLength":1,"description":"本次扫描的过程性事实（临时凭据/状态/目标怪癖/小惊喜/失败死路）。漏洞 PoC 用 write_finding，通用经验用 write_lesson。"}
  },
  "required":["content"]
}`)
}

// Execute 解析 content → 序列化 entry → AppendNote。
func (a *WriteNote) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var p struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 write_note 参数失败: %w", err)
	}
	if p.Content == "" {
		return toolfx.Result{}, fmt.Errorf("content 不能为空")
	}

	entry, _ := json.Marshal(map[string]any{
		"content":      p.Content,
		"agent_run_id": a.TaskID,
	})

	if err := a.Store.AppendNote(ctx, a.EngagementID, entry); err != nil {
		return toolfx.Result{}, fmt.Errorf("追加 note 失败: %w", err)
	}
	return toolfx.Result{Output: json.RawMessage(`{"ok":true}`)}, nil
}
