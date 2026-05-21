// Package actions 实现 ReAct 循环里通用的 Action 集合：
// done / read_notes / write_note / write_finding / write_graph。
//
// 设计要点：
//   - Note actions（ReadNotes/WriteNote）只依赖小接口 NoteStore，便于单测；
//     *notes.RedisStore 自动满足该接口（ReadNotes + AppendNote）。
//   - WriteFinding / WriteGraph 同样依赖窄接口（FindingStore / GraphStore），实参可换 mock。
package common

import (
	"context"
	"encoding/json"

	"github.com/V3teran/liusha/internal/toolruntime"
)

// Done 是终止 ReAct 循环的动作。Result.Done=true 由 runtime 直接退出主循环。
//
// 不做语义校验：args 原样回吐到 Result.Output 供 Inspector / 任务汇总使用。
// LLM 自由收手，MaxSteps 兜死循环。
//
// PreDoneCheck 是可选的前置闸：非 nil 返错时 Execute 拒绝完成（错误透传给 LLM）。
// 用于 subtask swarm：commander LLM 调 done 时若有 active strikers → 返错强制 commander 先调
// list_strikers 监控striker 进度，等strikers 全完才能真 done。零值（nil）= 无闸，等价旧行为。
type Done struct {
	PreDoneCheck func(ctx context.Context) error
}

// Name 返回工具名 "done"。
func (a Done) Name() string { return "done" }

func (a Done) Description() string {
	return "终止当前任务，args 中可带 reason / summary（供 Inspector / 报告参考）"
}

// ParametersJSON 返回 JSON Schema：reason / summary 都是可选字符串。
func (a Done) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"reason":{"type":"string"},"summary":{"type":"string"}}}`)
}

// Execute 先调 PreDoneCheck（如有），通过后返回 Done=true。
// args 即使为 nil 也回吐为空 JSON 对象。
func (a Done) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	if a.PreDoneCheck != nil {
		if err := a.PreDoneCheck(ctx); err != nil {
			return toolfx.Result{}, err
		}
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	return toolfx.Result{Done: true, Output: args}, nil
}
