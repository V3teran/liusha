package einoagent

import (
	"context"
	"strings"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
)

// reasoning_callback.go：用 eino callbacks（OnStart→OnEnd）一站式捕获每次 ChatModel 调用的
// agent 思路文字 + 输入/输出 token + 耗时，发 reasoning 事件 → 前端推理卡展示。
//
// 为什么用 callbacks 而非 AfterChatModel middleware：callbacks 的 OnEnd 同时拿到 out.Message（文字）
// + out.TokenUsage（in/out token）+ 配合 OnStart 计时（latency），三者一站式且准确；middleware 的
// AfterChatModel 拿不到耗时。与 UsageRecorder 并列注入（覆盖 orchestrator + 所有子代理的 ChatModel 调用）。

type reasoningStateKey struct{}

// NewReasoningCallback 造 reasoning 事件捕获 callbacks。sink nil 时返回 nil（无对话不发）。
func NewReasoningCallback(sink EventSink) callbacks.Handler {
	if sink == nil {
		return nil
	}
	return callbacks.NewHandlerBuilder().
		OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
			if info == nil || info.Component != components.ComponentOfChatModel {
				return ctx
			}
			return context.WithValue(ctx, reasoningStateKey{}, time.Now())
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
			ev := ScanEvent{Kind: ScanEventReasoning, Text: text}
			if out.TokenUsage != nil {
				ev.InTokens = out.TokenUsage.PromptTokens
				ev.OutTokens = out.TokenUsage.CompletionTokens
			}
			if st, ok := ctx.Value(reasoningStateKey{}).(time.Time); ok {
				ev.LatencyMs = int(time.Since(st).Milliseconds())
			}
			sink.OnScanEvent(ctx, ev)
			return ctx
		}).
		Build()
}
