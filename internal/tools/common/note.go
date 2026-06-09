package common

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/toolruntime"
)

// NoteStore 是  owner notes 共享笔记板的最小访问接口。
// 由 *notes.RedisStore 自动满足（internal/notes 包提供）。
//
//	owner 可挂多 host，notes 按 (eid, host) 切分。
type NoteStore interface {
	ReadNotes(ctx context.Context, ownerID, host string) ([]byte, error)
	AppendNote(ctx context.Context, ownerID, host string, entry []byte) error
}

// ReadNotes — 一次读取 (owner, host) notes 共享黑板（短期记忆，本次扫描内）。
// 命名与 read_findings / read_lessons / read_credentials 等一致用复数（读多条 note）。
type ReadNotes struct {
	Store    NoteStore
	OwnerID  string
	Host     string
	HunterID string
}

// Name 返回工具名 "read_notes"。
func (a *ReadNotes) Name() string { return "read_notes" }

func (a *ReadNotes) Description() string {
	return "读取本次扫描（owner）共享笔记板——与同 host 其他 agent task（trafficAnalysis / orchestrator / exploitation）共享的过程性事实。" +
		"读到的内容包括：目标实例当前怪癖、扫描中发现的小惊喜、失败死路。" +
		"owner 关闭即过期，不跨次扫描。"
}

// ParametersJSON 返回空对象 schema。
func (a *ReadNotes) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

// Execute 调 Store.ReadNotes 并返回字节流。
func (a *ReadNotes) Execute(ctx context.Context, _ json.RawMessage) (toolfx.Result, error) {
	state, err := a.Store.ReadNotes(ctx, a.OwnerID, a.Host)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("读取 note 失败: %w", err)
	}
	return toolfx.Result{Output: state}, nil
}

// WriteNote — 写一条短期记忆到 (owner, host) notes 共享黑板（纯文本追加）。
type WriteNote struct {
	Store    NoteStore
	OwnerID  string
	Host     string
	HunterID string
}

// Name 返回工具名 "write_note"。
func (a *WriteNote) Name() string { return "write_note" }

// note vs finding vs lesson 三类记忆边界：
//   - finding：结构化漏洞 PoC（可复现）
//   - note：本次扫描的过程性事实（短期，owner 关闭即过期）
//   - lesson：跨次扫描的长期经验
func (a *WriteNote) Description() string {
	return "写一条过程性事实到本 (owner + host) 信息黑板。passive 共享给同 host 后续 trafficAnalysis；active 用作长任务 step 间外置记忆（防 ReAct 滑窗压缩丢早期决策）。owner 关闭即过期。" +
		"\n\n【必写】仅适合 note 的内容：" +
		"\n- 目标实例当前怪癖（如『强制 security=impossible 需 cookie 覆盖』）" +
		"\n- 待深挖的线索：暴露端口、可疑 endpoint、奇怪报错" +
		"\n- 失败死路：什么打法不通，避免后续 agent 重蹈" +
		"\n- active 长任务关键中间状态：cookie/token、webshell 路径、已测攻击路径" +
		"\n\n【禁写】（改用对应工具）：漏洞 PoC → write_finding；通用经验 → write_lesson；finding 里已写过的内容（重复浪费）。" +
		"\n\n板满 200 条时老条目会被蒸馏成摘要（语义保留但细节丢失），关键发现请尽早 promote 到 finding/lesson。"
}

// ParametersJSON 给出 content 必填 schema。
func (a *WriteNote) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "content":{"type":"string","minLength":1,"description":"本次扫描的过程性事实（目标怪癖/小惊喜/失败死路）。漏洞 PoC 用 write_finding，通用经验用 write_lesson。"}
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
		return toolfx.Result{}, fmt.Errorf("content 必填")
	}

	entry, _ := json.Marshal(map[string]any{
		"content":   p.Content,
		"hunter_id": a.HunterID,
	})

	if err := a.Store.AppendNote(ctx, a.OwnerID, a.Host, entry); err != nil {
		return toolfx.Result{}, fmt.Errorf("追加 note 失败: %w", err)
	}
	return toolfx.Result{Output: json.RawMessage(`{"ok":true}`)}, nil
}
