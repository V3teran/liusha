package common

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// MemoryStore 是 engagement memory_notes 黑板的最小访问接口。
// 由 *engagement.Store 自动满足。
type MemoryStore interface {
	ReadStateScoped(ctx context.Context, engagementID string, opts engagement.ReadOpts) ([]byte, error)
	AppendNote(ctx context.Context, engagementID string, entry []byte) error
}

// ReadMemory — 一次读取 engagement memory_notes 共享黑板（纯自由文本，LLM 想写什么写什么）。
type ReadMemory struct {
	Store        MemoryStore
	EngagementID string
	TaskID       string
}

// Name 返回工具名 "read_memory"。
func (a *ReadMemory) Name() string { return "read_memory" }

// Description 提供给 LLM 的简介。
func (a *ReadMemory) Description() string {
	return "读取本 engagement 共享黑板（与同 host 其他 hunter task 共享的工作笔记，纯自由文本）"
}

// ParametersJSON 返回空对象 schema。
func (a *ReadMemory) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

// Execute 调 Store.ReadStateScoped 并返回字节流。
func (a *ReadMemory) Execute(ctx context.Context, _ json.RawMessage) (toolfx.Result, error) {
	state, err := a.Store.ReadStateScoped(ctx, a.EngagementID, engagement.ReadOpts{TaskID: a.TaskID})
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("读取 memory 状态失败: %w", err)
	}
	return toolfx.Result{Output: state}, nil
}

// WriteMemory — 写一条自由文本到 engagement memory_notes 共享黑板（纯文本追加）。
type WriteMemory struct {
	Store        MemoryStore
	EngagementID string
	TaskID       string
}

// Name 返回工具名 "write_memory"。
func (a *WriteMemory) Name() string { return "write_memory" }

// Description 提供给 LLM 的简介。
func (a *WriteMemory) Description() string {
	return "写一条自由文本笔记到本 engagement 共享黑板（同 host 其他 hunter task 能看见）。" +
		"用途：记录跨 task 想复用的事实/假设/边界——比如『拿到 admin cookie』『此 host 用 PHP+MySQL』。"
}

// ParametersJSON 给出 content 必填 schema。
func (a *WriteMemory) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "content":{"type":"string","description":"自由文本笔记内容"}
  },
  "required":["content"]
}`)
}

// Execute 解析 content → 序列化 entry → AppendNote。
func (a *WriteMemory) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var p struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 write_memory 参数失败: %w", err)
	}
	if p.Content == "" {
		return toolfx.Result{}, fmt.Errorf("content 不能为空")
	}

	entry, _ := json.Marshal(map[string]any{
		"content":      p.Content,
		"agent_run_id": a.TaskID,
	})

	if err := a.Store.AppendNote(ctx, a.EngagementID, entry); err != nil {
		return toolfx.Result{}, fmt.Errorf("追加 memory 失败: %w", err)
	}
	return toolfx.Result{Output: json.RawMessage(`{"ok":true}`)}, nil
}
