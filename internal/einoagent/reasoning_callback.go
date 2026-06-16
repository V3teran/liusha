package einoagent

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// reasoning_callback.go：用 eino callbacks 一站式捕获每次 ChatModel 调用的 agent 思路文字 +
// 输入/输出 token + 耗时，发 reasoning 事件 → 前端推理卡展示。
//
// 两条出口（互斥，eino 按模型实际产出二选一触发）：
//   - OnEnd：模型走 Generate（非流式）→ 拿完整 out.Message，一次性发最终 reasoning 帧。
//   - OnEndWithStreamOutput：模型走 Stream（流式）→ 逐 chunk 发 reasoning_delta 帧（前端逐字打字机），
//     读完累积全文 + token，末尾再发最终 reasoning 帧（带 token/耗时，前端用它替换活动气泡）。
//
// 为什么用 callbacks 而非 AfterChatModel middleware：callbacks 同时拿到 Message（文字）+ TokenUsage
// （in/out token）+ 配合 OnStart 计时（latency），三者一站式且准确。与 UsageRecorder 并列注入
// （覆盖 orchestrator + 所有子代理的 ChatModel 调用）。

type reasoningStateKey struct{}
type agentNameKey struct{}

// agentNameFromCtx 读 OnStart 在 Agent 边界存入的 agent 名（orchestrator / exploitation / …）。
// eino callback 分层触发且 ctx 沿组件树下传：Agent 层 OnStart 存名字 → 其内部 ChatModel 回调读得到。
// 实测 RunInfo：component=Agent name=orchestrator → component=ChatModel（同 ctx 谱系），故精确归属。
func agentNameFromCtx(ctx context.Context) string {
	if n, ok := ctx.Value(agentNameKey{}).(string); ok {
		return n
	}
	return ""
}

// NewReasoningCallback 造 reasoning 事件捕获 callbacks。sink nil 时返回 nil（无对话不发）。
func NewReasoningCallback(sink EventSink) callbacks.Handler {
	if sink == nil {
		return nil
	}
	return callbacks.NewHandlerBuilder().
		OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
			if info == nil {
				return ctx
			}
			// Agent 边界：把 agent 名（orchestrator / exploitation / …）存进 ctx，下传给其内部 ChatModel 回调。
			if info.Component == adk.ComponentOfAgent && info.Name != "" {
				return context.WithValue(ctx, agentNameKey{}, info.Name)
			}
			// ChatModel 边界：记起始时刻算 latency。
			if info.Component == components.ComponentOfChatModel {
				return context.WithValue(ctx, reasoningStateKey{}, time.Now())
			}
			return ctx
		}).
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
			if info == nil || info.Component != components.ComponentOfChatModel {
				return ctx
			}
			out := model.ConvCallbackOutput(output)
			if out == nil || out.Message == nil {
				return ctx
			}
			text := strings.TrimSpace(out.Message.Content)
			if text == "" {
				return ctx // 纯 tool_call 无文字 → 不发推理事件
			}
			ev := ScanEvent{Kind: ScanEventReasoning, Text: text, AgentName: agentNameFromCtx(ctx)}
			if out.TokenUsage != nil {
				ev.InTokens = out.TokenUsage.PromptTokens
				ev.OutTokens = out.TokenUsage.CompletionTokens
			}
			ev.LatencyMs = latencyFromCtx(ctx)
			sink.OnScanEvent(ctx, ev)
			return ctx
		}).
		OnEndWithStreamOutputFn(func(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
			if info == nil || info.Component != components.ComponentOfChatModel {
				output.Close()
				return ctx
			}
			// 流是私有副本，须读后关闭。起 goroutine 读：不阻塞 agent 主流程，逐 chunk 实时发 delta。
			go streamReasoning(ctx, output, sink)
			return ctx
		}).
		Build()
}

// streamReasoning 消费 ChatModel 流式输出：逐 chunk 发 reasoning_delta（前端逐字渲染），
// 读尽后累积全文 + 末次 token 用量，发最终 reasoning 帧（带 token/耗时，落库 + 替换活动气泡）。
func streamReasoning(ctx context.Context, sr *schema.StreamReader[callbacks.CallbackOutput], sink EventSink) {
	defer sr.Close()
	agentName := agentNameFromCtx(ctx) // ChatModel 回调谱系内已含 Agent 边界存入的名字
	var full strings.Builder
	var inTok, outTok int
	for {
		chunk, err := sr.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return // 流出错：放弃本次（不发最终帧，非致命）
		}
		out := model.ConvCallbackOutput(chunk)
		if out == nil {
			continue
		}
		if out.Message != nil && out.Message.Content != "" {
			piece := out.Message.Content
			full.WriteString(piece)
			sink.OnScanEvent(ctx, ScanEvent{Kind: ScanEventReasoningDelta, Text: piece, AgentName: agentName})
		}
		if out.TokenUsage != nil {
			inTok = out.TokenUsage.PromptTokens
			outTok = out.TokenUsage.CompletionTokens
		}
	}
	text := strings.TrimSpace(full.String())
	if text == "" {
		return // 纯 tool_call 流无文字 → 不发最终帧
	}
	sink.OnScanEvent(ctx, ScanEvent{
		Kind:      ScanEventReasoning,
		Text:      text,
		AgentName: agentName,
		InTokens:  inTok,
		OutTokens: outTok,
		LatencyMs: latencyFromCtx(ctx),
	})
}

// latencyFromCtx 读 OnStart 埋的起始时刻算耗时（ms）；无则 0。
func latencyFromCtx(ctx context.Context) int {
	if st, ok := ctx.Value(reasoningStateKey{}).(time.Time); ok {
		return int(time.Since(st).Milliseconds())
	}
	return 0
}
