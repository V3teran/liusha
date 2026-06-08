package einoagent

import (
	"context"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
)

// scan_event.go：agent 过程事件发射（阶段B2，见记忆 project_phaseb_sse_arch）。
//
// 把每个 agent（commander + 所有 deep sub-agent）的工具调用，经 WrapToolCall middleware
// 实时发成 ScanEvent。覆盖 sub-agent 是关键——deep 的 striker 内部 run_command 不冒泡到顶层
// 事件流，但 WrapToolCall 挂在每个 agent 上（tool_invocation 落库已证明：commander 无
// run_command 工具，e2e 却记到 94 条 run_command，全是 striker 跑的），故能拿到 striker
// 执行的命令 + 结果，让前端实时看到「striker 跑了什么、结果如何」。
//
// 事件去向（scanner 注入 EventSink）：落 conversation message（PG）+ publish redis（实时推前端）。

const scanEventResultPreviewLimit = 4096

// ScanEventKind 区分过程事件类型。
type ScanEventKind string

const (
	// ScanEventToolCall：agent 发起一次工具调用（命令刚开始跑，执行前发）。
	ScanEventToolCall ScanEventKind = "tool_call"
	// ScanEventToolResult：工具返回结果（命令跑完，执行后发）。
	ScanEventToolResult ScanEventKind = "tool_result"
)

// ScanEvent 是一次 agent 运行中的过程事件（liusha 自有，不依赖 eino 细节）。
type ScanEvent struct {
	Kind       ScanEventKind
	ToolName   string // 工具名（run_command / write_finding / task / ...）
	Args       string // tool_call 的入参（JSON 文本）
	Result     string // tool_result 的结果（截断预览）
	DurationMs int    // tool_result 的执行耗时
	Err        string // 工具执行错误（如有）
}

// EventSink 消费 agent 过程事件。scanner 注入实现（落 conversation message + redis publish）。
type EventSink interface {
	OnScanEvent(ctx context.Context, ev ScanEvent)
}

// NewEventEmitter 造 WrapToolCall middleware：每次工具调用发 tool_call（执行前）+ tool_result
// （执行后）两个事件给 sink，让前端先看到「正在跑 sqlmap…」再看到结果。
//
// sink nil 时返回零值 middleware（no-op，向后兼容 asynq 自动路径——无对话则不发事件）。
// 挂在 commander + 所有 sub-agent 上（einoRunOpts → deep_swarm 给每个 agent 传 middlewares），
// 故 striker 内部工具调用也发事件。复用 tool_recorder.go 的 truncate / toolResultText。
func NewEventEmitter(sink EventSink) adk.AgentMiddleware {
	if sink == nil {
		return adk.AgentMiddleware{}
	}
	return adk.AgentMiddleware{
		WrapToolCall: compose.ToolMiddleware{
			// InferTool 系（write_finding / task / replay_flow…）走 Invokable。
			Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
				return func(ctx context.Context, in *compose.ToolInput) (*compose.ToolOutput, error) {
					sink.OnScanEvent(ctx, ScanEvent{Kind: ScanEventToolCall, ToolName: in.Name, Args: in.Arguments})
					start := time.Now()
					out, err := next(ctx, in)
					ev := ScanEvent{
						Kind:       ScanEventToolResult,
						ToolName:   in.Name,
						DurationMs: int(time.Since(start).Milliseconds()),
					}
					if out != nil {
						ev.Result = truncate(out.Result, scanEventResultPreviewLimit)
					}
					if err != nil {
						ev.Err = err.Error()
					}
					sink.OnScanEvent(ctx, ev)
					return out, err
				}
			},
			// run_command 走 EnhancedInvokable（多模态 ToolResult），text part 拼成预览。
			EnhancedInvokable: func(next compose.EnhancedInvokableToolEndpoint) compose.EnhancedInvokableToolEndpoint {
				return func(ctx context.Context, in *compose.ToolInput) (*compose.EnhancedInvokableToolOutput, error) {
					sink.OnScanEvent(ctx, ScanEvent{Kind: ScanEventToolCall, ToolName: in.Name, Args: in.Arguments})
					start := time.Now()
					out, err := next(ctx, in)
					ev := ScanEvent{
						Kind:       ScanEventToolResult,
						ToolName:   in.Name,
						DurationMs: int(time.Since(start).Milliseconds()),
					}
					if out != nil && out.Result != nil {
						ev.Result = truncate(toolResultText(out.Result), scanEventResultPreviewLimit)
					}
					if err != nil {
						ev.Err = err.Error()
					}
					sink.OnScanEvent(ctx, ev)
					return out, err
				}
			},
		},
	}
}
