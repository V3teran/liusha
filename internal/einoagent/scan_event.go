package einoagent

import (
	"context"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
)

// scan_event.go：agent 过程事件发射（阶段B2，见记忆 project_phaseb_sse_arch）。
//
// 把每个 agent（orchestrator + 所有 deep sub-agent）的工具调用，经 WrapToolCall middleware
// 实时发成 ScanEvent。覆盖 sub-agent 是关键——deep 的 exploitation 内部 run_command 不冒泡到顶层
// 事件流，但 WrapToolCall 挂在每个 agent 上（tool_invocation 落库已证明：orchestrator 无
// run_command 工具，e2e 却记到 94 条 run_command，全是 exploitation 跑的），故能拿到 exploitation
// 执行的命令 + 结果，让前端实时看到「exploitation 跑了什么、结果如何」。
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
	// ScanEventReasoning：agent 每轮 ChatModel 调用后产出的推理文字（思路/分析/计划/决策叙述）。
	// 经 reasoning callback 捕获 → 前端「推理卡」展示，让用户看到 agent 在想什么/打算干什么。
	// 替代旧的 write_note（思路改输出到对话，notes 退役）。
	ScanEventReasoning ScanEventKind = "reasoning"
	// ScanEventReasoningDelta：流式推理的增量片段（Text=本次 chunk）。模型走 Stream 时逐 chunk 发，
	// 前端累积成「活动推理气泡」逐字渲染；最终 ScanEventReasoning 帧到达后替换之。
	// 瞬时帧——scanner 仅 publish redis 实时推，不落库、不占 seq（重连补历史靠最终帧即可）。
	ScanEventReasoningDelta ScanEventKind = "reasoning_delta"
	// ScanEventSpawn：orchestrator 调 deep 的 task 工具派活给子代理（active swarm 团队协作）。
	// Args 含 {subagent_type, description}——派给谁、干什么。前端「派发卡」展示 AI 指挥 AI 团队。
	ScanEventSpawn ScanEventKind = "spawn"
	// ScanEventCompaction：上下文压缩发生（老 turn 蒸馏成 1 条摘要，防 context 爆）。
	// Text=蒸馏摘要正文。前端「压缩卡」展示「这里压缩了 N 条历史」，让用户对长对话的上下文裁剪有感知。
	ScanEventCompaction ScanEventKind = "compaction"
)

// deepTaskToolName 是 eino deep prebuilt 自带的派活工具名（orchestrator 经它 spawn 子代理）。
// vendor 私有常量，值实测为 "task"（见 message 落库 ToolName）。
const deepTaskToolName = "task"

// ScanEvent 是一次 agent 运行中的过程事件（liusha 自有，不依赖 eino 细节）。
type ScanEvent struct {
	Kind       ScanEventKind
	ToolName   string // 工具名（run_command / write_finding / task / ...）
	Args       string // tool_call 的入参（JSON 文本）
	Result     string // tool_result 的结果（截断预览）
	DurationMs int    // tool_result 的执行耗时
	Err        string // 工具执行错误（如有）
	Text       string // reasoning 的推理文字（其他类型空）
	AgentName  string // reasoning：产出该推理的 agent 名（orchestrator / exploitation / reconnaissance）
	InTokens   int    // reasoning：本次 LLM 调用输入 token（其他类型 0）
	OutTokens  int    // reasoning：本次 LLM 调用输出 token
	LatencyMs  int    // reasoning：本次 LLM 调用耗时（ms）
}

// EventSink 消费 agent 过程事件。scanner 注入实现（落 conversation message + redis publish）。
type EventSink interface {
	OnScanEvent(ctx context.Context, ev ScanEvent)
}

// toolCallEvent 按工具名造 tool_call 事件；deep 的 task 工具特殊化为 spawn（派子代理）。
// 带 ctx 读 agent 名（与 reasoning 同源 agentNameFromCtx），让前端工具卡也能按主/子 agent 区分。
func toolCallEvent(ctx context.Context, name, args string) ScanEvent {
	an := agentNameFromCtx(ctx)
	if name == deepTaskToolName {
		return ScanEvent{Kind: ScanEventSpawn, ToolName: name, Args: args, AgentName: an}
	}
	return ScanEvent{Kind: ScanEventToolCall, ToolName: name, Args: args, AgentName: an}
}

// NewEventEmitter 造 WrapToolCall middleware：每次工具调用发 tool_call（执行前）+ tool_result
// （执行后）两个事件给 sink，让前端先看到「正在跑 sqlmap…」再看到结果。
//
// sink nil 时返回零值 middleware（no-op，向后兼容 asynq 自动路径——无对话则不发事件）。
// 挂在 orchestrator + 所有 sub-agent 上（einoRunOpts → deep_swarm 给每个 agent 传 middlewares），
// 故 exploitation 内部工具调用也发事件。复用 tool_recorder.go 的 truncate / toolResultText。
func NewEventEmitter(sink EventSink) adk.AgentMiddleware {
	if sink == nil {
		return adk.AgentMiddleware{}
	}
	return adk.AgentMiddleware{
		// 注：reasoning 事件（agent 思路文字 + token + 耗时）改由 NewReasoningCallback（callbacks
		// OnStart→OnEnd 一站式拿文字/token/latency）发，不在此 middleware。本 middleware 只管工具事件。
		WrapToolCall: compose.ToolMiddleware{
			// InferTool 系（write_finding / task / replay_flow…）走 Invokable。
			Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
				return func(ctx context.Context, in *compose.ToolInput) (*compose.ToolOutput, error) {
					sink.OnScanEvent(ctx, toolCallEvent(ctx, in.Name, in.Arguments))
					start := time.Now()
					out, err := next(ctx, in)
					ev := ScanEvent{
						Kind:       ScanEventToolResult,
						ToolName:   in.Name,
						AgentName:  agentNameFromCtx(ctx),
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
					sink.OnScanEvent(ctx, toolCallEvent(ctx, in.Name, in.Arguments))
					start := time.Now()
					out, err := next(ctx, in)
					ev := ScanEvent{
						Kind:       ScanEventToolResult,
						ToolName:   in.Name,
						AgentName:  agentNameFromCtx(ctx),
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
