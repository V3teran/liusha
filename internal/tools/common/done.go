// Package actions 实现 ReAct 循环里通用的 Action 集合：
// done / read_state / write_fact / write_idea / write_hint / write_finding / write_graph。
//
// 设计要点（含黑客松借鉴）：
//   - Memory actions 只依赖小接口（MemoryStore），便于单测；engagement.Store 自动满足
//     接口（plan 1 part2 T6 已落库三个 Append 方法 + ReadState）。
//   - WriteFinding / WriteGraph 同样依赖窄接口（FindingStore / GraphStore），实参可换 mock。
//   - WriteFinding 写库后由 vulnfinding.Store 内部异步 fire OnSaved hook（T11 机制），订阅
//     由 main 装配阶段挂载（T30），不在本包责任范围。
package common

import (
	"context"
	"encoding/json"

	"github.com/V3teran/liusha/internal/toolfx"
)

// Done 是终止 ReAct 循环的动作。Result.Done=true 由 runtime 直接退出主循环。
//
// 注意：Done 是否"够格完成"由 T22.5 中间件 done_validate 统一裁决（Skill DoneValidator
// 注入），本结构体不做语义校验，args 原样回吐到 Result.Output 供 Observer / 任务汇总使用。
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
