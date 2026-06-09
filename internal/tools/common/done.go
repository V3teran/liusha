// done.go 实现 ReAct 循环的收尾 Action `done`（其余通用 Action 见同包各文件：
// note.go / finding.go / sitemap.go 等）。
//
// 设计要点：done 收尾时释放本 hunter 占用的 browser tab（releaseBrowserTab），
// 避免共享 jar 的 tab 泄漏。
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
// 用于 subtask swarm：orchestrator LLM 调 done 时若有 active exploitations → 返错强制 orchestrator 先调
// list_exploitations 监控exploitation 进度，等exploitations 全完才能真 done。零值（nil）= 无闸，等价旧行为。
//
// Sandbox + HunterID 可选——非空时 PreDoneCheck 通过后 best-effort 调 `browser-use release-tab`
// 关闭本 task 的浏览器 tab（不关 daemon，host 内兄弟 task 继续共享 session）。
// 失败仅记 Warning 到 Result（容器销毁兜底），不阻塞 done。
type Done struct {
	PreDoneCheck func(ctx context.Context) error
	Sandbox      sandbox.Client
	HunterID     string
}

// Name 返回工具名 "done"。
func (a Done) Name() string { return "done" }

func (a Done) Description() string {
	return "终止当前任务，args 中可带 reason / summary（供 Inspector / 报告参考）。" +
		"\n\n【何时调】finding 都写完 + 攻击面已 recon 完 → done。" +
		"\n【何时不调】orchestrator 有 running exploitation（PreDoneCheck 会拦截并返结构化错误：含 running 列表 + 行动建议——按错误消息执行，不要 retry done）/ 攻击面未挖完 / 撞到证据未写 finding。" +
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
	if a.Sandbox == nil || a.HunterID == "" {
		return
	}
	_, _ = a.Sandbox.Exec(ctx, sandbox.ExecRequest{
		HunterID:       a.HunterID,
		Command:        "browser-use release-tab",
		TimeoutSeconds: 10,
		Tag:            "done-release-tab",
	})
}
