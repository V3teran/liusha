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

	"github.com/V3teran/liusha/internal/sandbox"
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
//
// Sandbox + TaskID 可选——非空时 PreDoneCheck 通过后 best-effort 调 `browser-use release-tab`
// 关闭本 task 的浏览器 tab（不关 daemon，host 内兄弟 task 继续共享 session）。
// 失败仅记 Warning 到 Result（容器销毁兜底），不阻塞 done。
type Done struct {
	PreDoneCheck func(ctx context.Context) error
	Sandbox      sandbox.Client
	TaskID       string
}

// Name 返回工具名 "done"。
func (a Done) Name() string { return "done" }

func (a Done) Description() string {
	return "终止当前任务，args 中可带 reason / summary（供 Inspector / 报告参考）。" +
		"\n\n【何时调】finding 都写完 + 攻击面已 recon 完 → done。" +
		"\n【何时不调】commander 有 running striker（PreDoneCheck 会自动拦）/ 攻击面未挖完 / 撞到证据未写 finding。" +
		"\n【避免空白 done】调前若 read_findings 显示本任务 0 finding，先评估：(a) 真无漏洞 → 写 write_lesson 沉淀'此 host 攻面已穷举无漏洞'再 done；(b) 还能挖 → 继续 ReAct 不调 done。"
}

// ParametersJSON 返回 JSON Schema：reason / summary 都是可选字符串。
func (a Done) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"reason":{"type":"string"},"summary":{"type":"string"}}}`)
}

// Execute 先调 PreDoneCheck（如有），通过后 best-effort 关本 task 浏览器 tab，再返回 Done=true。
// args 即使为 nil 也回吐为空 JSON 对象。
func (a Done) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	if a.PreDoneCheck != nil {
		if err := a.PreDoneCheck(ctx); err != nil {
			return toolfx.Result{}, err
		}
	}
	a.releaseBrowserTab(ctx)
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	return toolfx.Result{Done: true, Output: args}, nil
}

// releaseBrowserTab best-effort 调 sandbox 内 `browser-use release-tab` 关本 task 的 tab。
// wrapper 内部判断：无 TAB_FILE（本 task 没用过 browser）静默 exit 0。
// 失败不影响 done——容器销毁会兜底回收所有 tab。
func (a Done) releaseBrowserTab(ctx context.Context) {
	if a.Sandbox == nil || a.TaskID == "" {
		return
	}
	_, _ = a.Sandbox.Exec(ctx, sandbox.ExecRequest{
		TaskID:         a.TaskID,
		Command:        "browser-use release-tab",
		TimeoutSeconds: 10,
		Tag:            "done-release-tab",
	})
}
