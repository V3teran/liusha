// Package actions 实现 ReAct 循环里通用的 Action 集合：
// done / read_notes / write_note / write_finding / write_graph。
//
// 设计要点：
//   - Note actions（ReadNote/WriteNote）只依赖小接口 NoteStore，便于单测；
//     engagement.Store 自动满足该接口（ReadNotesScoped + AppendNote）。
//   - WriteFinding / WriteGraph 同样依赖窄接口（FindingStore / GraphStore），实参可换 mock。
package common

import (
	"context"
	"encoding/json"

	"github.com/V3teran/liusha/internal/toolruntime"
)

// Done 是终止 ReAct 循环的动作。Result.Done=true 由 runtime 直接退出主循环。
//
// 注意：Done 是否"够格完成"由 T22.5 中间件 done_validate 统一裁决（Skill DoneValidator
// 注入），本结构体不做语义校验，args 原样回吐到 Result.Output 供 Reviewer / 任务汇总使用。
type Done struct{}

// Name 返回动作名 "done"，与 done_validate 中间件硬编码匹配。
func (Done) Name() string { return "done" }

// Description 是 LLM tool schema 的 description 字段。
func (Done) Description() string {
	return "终止当前任务，args 中可带 reason / summary（具体是否允许结束由 Skill DoneValidator 裁决）"
}

// ParametersJSON 返回 JSON Schema：reason / summary 都是可选字符串。
func (Done) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"reason":{"type":"string"},"summary":{"type":"string"}}}`)
}

// Execute 直接返回 Done=true；args 即使为 nil 也回吐为空 JSON 对象。
func (Done) Execute(_ context.Context, args json.RawMessage) (toolfx.Result, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	return toolfx.Result{Done: true, Output: args}, nil
}
