package einotools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// NoteStore 是 owner 共享笔记板的最小访问接口（*notes.RedisStore 自动满足）。
// 对应 internal/tools/common 的 NoteStore——owner 可挂多 host，notes 按 (owner, host) 切分。
type NoteStore interface {
	ReadNotes(ctx context.Context, ownerID, host string) ([]byte, error)
	AppendNote(ctx context.Context, ownerID, host string, entry []byte) error
}

// BuildReadNotes 造原生 eino read_notes 工具。owner/host 闭包捕获。
func BuildReadNotes(store NoteStore, ownerID, host, hunterID string) (tool.BaseTool, error) {
	return utils.InferTool(
		"read_notes",
		"读取本次扫描（owner）共享笔记板——与同 host 其他 agent task（trafficAnalysis / orchestrator / exploitation）共享的过程性事实。"+
			"读到的内容包括：目标实例当前怪癖、扫描中发现的小惊喜、失败死路。owner 关闭即过期，不跨次扫描。",
		func(ctx context.Context, _ noArgs) (json.RawMessage, error) {
			if ownerID == "" || host == "" {
				return nil, errors.New("read_notes: owner/host 注入缺失")
			}
			b, err := store.ReadNotes(ctx, ownerID, host)
			if err != nil {
				return nil, fmt.Errorf("读取 note 失败: %w", err)
			}
			if len(b) == 0 {
				return json.RawMessage(`[]`), nil
			}
			return json.RawMessage(b), nil
		})
}

// writeNoteArgs 是 write_note 入参；content 必填。
type writeNoteArgs struct {
	Content string `json:"content" jsonschema:"required,description=本次扫描的过程性事实（目标怪癖/小惊喜/失败死路）。漏洞 PoC 用 write_finding，通用经验用 write_lesson。"`
}

// BuildWriteNote 造原生 eino write_note 工具。owner/host/hunter 闭包捕获。
func BuildWriteNote(store NoteStore, ownerID, host, hunterID string) (tool.BaseTool, error) {
	return utils.InferTool(
		"write_note",
		"写一条过程性事实到本 (owner + host) 信息黑板。passive 共享给同 host 后续 trafficAnalysis；active 用作长任务 step 间外置记忆（防 ReAct 滑窗压缩丢早期决策）。owner 关闭即过期。"+
			"\n\n【必写】仅适合 note 的内容："+
			"\n- 目标实例当前怪癖（如『强制 security=impossible 需 cookie 覆盖』）"+
			"\n- 待深挖的线索：暴露端口、可疑 endpoint、奇怪报错"+
			"\n- 失败死路：什么打法不通，避免后续 agent 重蹈"+
			"\n- active 长任务关键中间状态：cookie/token、webshell 路径、已测攻击路径"+
			"\n\n【禁写】（改用对应工具）：漏洞 PoC → write_finding；通用经验 → write_lesson；finding 里已写过的内容（重复浪费）。"+
			"\n\n板满 200 条时老条目会被蒸馏成摘要（语义保留但细节丢失），关键发现请尽早 promote 到 finding/lesson。",
		func(ctx context.Context, in writeNoteArgs) (map[string]any, error) {
			if in.Content == "" {
				return nil, errors.New("content 必填")
			}
			if ownerID == "" || host == "" {
				return nil, errors.New("write_note: owner/host 注入缺失")
			}
			entry, _ := json.Marshal(map[string]any{
				"content":   in.Content,
				"hunter_id": hunterID,
			})
			if err := store.AppendNote(ctx, ownerID, host, entry); err != nil {
				return nil, fmt.Errorf("追加 note 失败: %w", err)
			}
			return map[string]any{"ok": true}, nil
		})
}
