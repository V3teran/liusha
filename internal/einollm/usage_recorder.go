package einollm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/logx"
)

// usage_recorder.go：eino 路径的 LLM 调用计费埋点（替代旧 llm.Instrument）。
//
// eino 的 ChatModel 调用由框架在 graph 节点边界触发 callbacks，OnEnd 携带 TokenUsage。
// 本 handler 按 run 注入（adk.WithCallbacks），meta 携带本 hunter 的 owner/role，
// 每次 ChatModel 调用落一行 llm_invocation（成本/角色/owner 审计，与 react 路径同库同语义）。

// recorderLog 包级构建一次（避免每次 New 触发 logx 全局写入的 race）。
var recorderLog = logx.New("einollm.recorder")

// recorderState 在 OnStart→OnEnd 间经 ctx 传递（起始时刻 + 入参消息，用于算延迟 + 审计）。
type recorderState struct {
	start time.Time
	input []*schema.Message
}

type recorderStateKey struct{}

// ResolveProviderModel 返回某 role 解析到的 provider key + 默认 model，
// 供埋点 handler 填 Invocation.Provider/Model（provider key 与旧 Generator.Provider() 一致，
// 保证 pricing.Lookup 与成本聚合口径不变）。
func (f *Factory) ResolveProviderModel(role string) (provider, model string) {
	key := f.resolveProviderKey(role)
	if key == "" {
		return "", ""
	}
	return key, f.cfg.Providers[key].DefaultModel
}

// SupportsVisionFor 返回某 role 路由到的 provider 是否支持 vision（截图回灌开关，见 VisionRelayMiddleware）。
// config.Providers[key].SupportsVision 是 *bool（启动期 validate 强制非 nil）；缺失保守返 false。
func (f *Factory) SupportsVisionFor(role string) bool {
	key := f.resolveProviderKey(role)
	if key == "" {
		return false
	}
	if sv := f.cfg.Providers[key].SupportsVision; sv != nil {
		return *sv
	}
	return false
}

// NewUsageRecorder 造一个只关心 ChatModel 组件的 callbacks.Handler，把每次调用的 token usage
// 落 llm_invocation。pricing 可为 nil（仅落 usage 不算 cost）。埋点失败仅吞掉，不阻塞 agent run。
func NewUsageRecorder(sink llm.CallSink, pricing llm.PricingProvider, meta llm.CallMeta, provider, defaultModel string) callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
			if info == nil || info.Component != components.ComponentOfChatModel {
				return ctx
			}
			st := recorderState{start: time.Now()}
			if in := model.ConvCallbackInput(input); in != nil {
				st.input = in.Messages
			}
			return context.WithValue(ctx, recorderStateKey{}, st)
		}).
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
			if info == nil || info.Component != components.ComponentOfChatModel {
				return ctx
			}
			out := model.ConvCallbackOutput(output)
			if out == nil {
				return ctx
			}
			rec := buildInvocation(ctx, meta, pricing, provider, defaultModel, out)
			appendInvocation(sink, meta, rec)
			return ctx
		}).
		// OnEndWithStreamOutput：模型走 Stream（EnableStreaming）时触发——非流式走上面 OnEnd。
		// 读尽流式副本累积 token usage（在末 chunk）+ 全文（审计），落同一行 llm_invocation。
		// 不开此出口则开启流式后计费/token 全丢，故与 OnEnd 成对实现。
		OnEndWithStreamOutputFn(func(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
			if info == nil || info.Component != components.ComponentOfChatModel {
				output.Close()
				return ctx
			}
			go func() {
				defer output.Close()
				merged := mergeStreamOutput(output)
				if merged == nil {
					return
				}
				rec := buildInvocation(ctx, meta, pricing, provider, defaultModel, merged)
				appendInvocation(sink, meta, rec)
			}()
			return ctx
		}).
		// OnError：ChatModel 调用失败（瞬时 4xx/429/EOF 等）也落一行带 error 的 llm_invocation，
		// 与 react Instrument 两者都记对齐（成功率/故障率统计需要失败样本）。
		OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, runErr error) context.Context {
			if info == nil || info.Component != components.ComponentOfChatModel {
				return ctx
			}
			rec := buildInvocation(ctx, meta, pricing, provider, defaultModel, &model.CallbackOutput{})
			if runErr != nil {
				rec.Error = runErr.Error()
			}
			appendInvocation(sink, meta, rec)
			return ctx
		}).
		Build()
}

// mergeStreamOutput 读尽流式 ChatModel 输出副本，合并成一个 *model.CallbackOutput：
// token usage 取末次非空（流式 usage 在最后 chunk），全文拼接进 Message（审计完整）。
// 空流返回 nil（计费跳过）。读完不关流——调用方 defer Close。
func mergeStreamOutput(sr *schema.StreamReader[callbacks.CallbackOutput]) *model.CallbackOutput {
	var merged model.CallbackOutput
	var content strings.Builder
	var got bool
	for {
		chunk, err := sr.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			break // 流出错：用已累积的部分落库（best-effort）
		}
		out := model.ConvCallbackOutput(chunk)
		if out == nil {
			continue
		}
		got = true
		if out.TokenUsage != nil {
			merged.TokenUsage = out.TokenUsage
		}
		if out.Config != nil {
			merged.Config = out.Config
		}
		if out.Message != nil {
			content.WriteString(out.Message.Content)
			merged.Message = out.Message // 保留末 chunk 的 ResponseMeta（FinishReason）
		}
	}
	if !got {
		return nil
	}
	// 用累积全文覆盖末 chunk 的局部 content（审计 Result 要完整输出）。
	if merged.Message != nil {
		m := *merged.Message
		m.Content = content.String()
		merged.Message = &m
	}
	return &merged
}

// appendInvocation 落库 + best-effort 错误处理（埋点失败仅 warn，不阻塞 agent run）。
func appendInvocation(sink llm.CallSink, meta llm.CallMeta, rec llminvocation.Invocation) {
	if _, err := sink.Append(context.Background(), rec); err != nil {
		recorderLog.Warn().Err(err).Str("role", meta.RouteKey).
			Msg("eino llm_invocation append 失败（不阻塞 agent run）")
	}
}

// buildInvocation 把 eino model callback 输出映射成 llminvocation.Invocation。
func buildInvocation(ctx context.Context, meta llm.CallMeta, pricing llm.PricingProvider, provider, defaultModel string, out *model.CallbackOutput) llminvocation.Invocation {
	var latencyMs int
	var input []*schema.Message
	if st, ok := ctx.Value(recorderStateKey{}).(recorderState); ok {
		latencyMs = int(time.Since(st.start).Milliseconds())
		input = st.input
	}

	mdl := defaultModel
	if out.Config != nil && out.Config.Model != "" {
		mdl = out.Config.Model
	}

	var usage llm.Usage
	if out.TokenUsage != nil {
		usage = llm.Usage{
			InTokens:     out.TokenUsage.PromptTokens,
			OutTokens:    out.TokenUsage.CompletionTokens,
			CachedTokens: out.TokenUsage.PromptTokenDetails.CachedTokens,
		}
	}

	var finish string
	if out.Message != nil && out.Message.ResponseMeta != nil {
		finish = out.Message.ResponseMeta.FinishReason
	}

	rec := llminvocation.Invocation{
		HunterID:     meta.HunterID,
		OwnerType:    meta.OwnerType,
		OwnerID:      meta.OwnerID,
		Provider:     provider,
		Model:        mdl,
		InTokens:     usage.InTokens,
		OutTokens:    usage.OutTokens,
		CachedTokens: usage.CachedTokens,
		LatencyMs:    latencyMs,
		FinishReason: finish,
		Role:         meta.RouteKey,
	}
	if pricing != nil {
		rec.CostUSD = pricing.Estimate(provider, mdl, usage)
	}
	// 审计：入参消息 + 输出消息序列化落库（best-effort，失败留空不阻塞）。
	if len(input) > 0 {
		if b, err := json.Marshal(input); err == nil {
			rec.Messages = b
		}
	}
	if out.Message != nil {
		if b, err := json.Marshal(out.Message); err == nil {
			rec.Result = b
		}
	}
	return rec
}
