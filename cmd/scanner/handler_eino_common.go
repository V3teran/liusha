package main

import (
	"context"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/callbacks"

	"github.com/V3teran/liusha/internal/einoagent"
	"github.com/V3teran/liusha/internal/einollm"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/toolinvocation"
)

// toolSink 返回 tool_invocation 落库适配器（best-effort）；store 缺失时 nil（recorder no-op）。
func (h handler) toolSink() einoagent.ToolSink {
	if h.hunterDeps.ToolInvocations == nil {
		return nil
	}
	return einoToolSink{store: h.hunterDeps.ToolInvocations, h: h}
}

type einoToolSink struct {
	store *toolinvocation.Store
	h     handler
}

func (s einoToolSink) RecordTool(ctx context.Context, inv einoagent.ToolInvocation) {
	if _, err := s.store.Append(ctx, toolinvocation.Invocation{
		HunterID:      inv.HunterID,
		OwnerType:     inv.OwnerType,
		OwnerID:       inv.OwnerID,
		ToolName:      inv.ToolName,
		Args:          inv.Args,
		OutputSize:    inv.OutputSize,
		OutputPreview: inv.OutputPreview,
		DurationMs:    inv.DurationMs,
		ErrorMessage:  inv.ErrorMessage,
	}); err != nil {
		s.h.logger.Warn().Err(err).Str("tool", inv.ToolName).Str("hunter_id", inv.HunterID).
			Msg("eino tool_invocation 记录失败（不阻塞业务）")
	}
}

// handler_eino_common.go：eino passive/active 路径共享的装配胶水。

// einoToolDeps 把 handler 的 store/loader（与旧 hunter.Deps 同源）打包成 einoagent 工具装配依赖。
// sandboxClient 是本次 agent run 的容器（orchestrator + exploitation 共享）。
func (h handler) einoToolDeps(sandboxClient sandbox.Client) einoagent.TrafficAnalysisToolDeps {
	return einoagent.TrafficAnalysisToolDeps{
		Findings:          h.findings,
		Lessons:           h.lessons,
		Credentials:       h.hunterDeps.Credentials,
		Flows:             h.flows,
		ToolingLoader:     h.hunterDeps.ToolingLoader,
		VulnLoader:        h.hunterDeps.VulnLoader,
		Sandbox:           sandboxClient,
		MaxTimeoutSeconds: h.cfg.Toolruntime.StepToolTimeoutSeconds,
		TailBytes:         h.cfg.Sandbox.RunTailBytes,
	}
}

// einoRunOpts 为一次 agent run（trafficAnalysis/exploitation/orchestrator）产 per-run 中间件 + 选项：
//   - 历史压缩 middleware（light compactor，装配失败降级跳过）
//   - tool_invocation 遥测 + 截图回灌
//   - 计费埋点 callbacks（按 hunterID/owner/role 落 llm_invocation）
//   - 过程事件发射（仅 conversationID 非空，即对话发起时）：落 conversation message + redis publish
//
// role ∈ trafficAnalysis/exploitation/orchestrator，决定 provider 解析 + token 用量聚合维度。
// conversationID 空（asynq 自动入口）时不发过程事件，纯后台扫描。
// 返回值新增 cleanup func()：调用方在 agent run 结束后 defer 调用，flush 异步事件 sink
// （关 channel + 等 writer 写完缓冲事件）。无事件 sink 时为 no-op。
func (h handler) einoRunOpts(ctx context.Context, hunterID, ownerType, ownerID, role, conversationID string) ([]adk.AgentMiddleware, []adk.AgentRunOption, func()) {
	var mws []adk.AgentMiddleware
	// 事件 sink 提前创建（compaction + EventEmitter 共用）：对话发起时把 agent 过程事件异步落
	// conversation message + publish redis，供前端实时展示。conversationID 空则 sink 为真 nil（纯后台扫描）。
	// 用 EventSink 接口类型声明——未赋值时是真 nil（避开 typed-nil 指针转接口后 != nil 的 Go 坑）。
	cleanup := func() {} // 默认 no-op
	var sink einoagent.EventSink
	if conversationID != "" && h.conversations != nil && h.eventPublisher != nil {
		concrete := newEinoEventSink(h.conversations, h.eventPublisher, conversationID, h.logger)
		sink = concrete
		cleanup = concrete.Close // run 结束后 flush 缓冲事件
	}
	// 工具错误守卫（注册最前 = 最外层）：单次工具出错（如 LLM 漏填必填参数）转结果回灌模型，
	// 避免被 eino deep 升级为致命 NodeRunError 炸掉整条 run。见 einoagent/tool_guard.go。
	mws = append(mws, einoagent.NewToolErrorGuard())
	if compactor, err := h.einoFactory.For(ctx, "compactor"); err == nil {
		// 传 sink → 压缩发生时发 ScanEventCompaction，前端「压缩卡」可见上下文裁剪。
		mws = append(mws, einoagent.NewCompactionMiddleware(compactor, einoagent.CompactionConfig{}, sink))
	} else {
		h.logger.Warn().Err(err).Str("role", role).Msg("eino compactor 装配失败，本次跳过历史压缩")
	}
	// tool_invocation 遥测（gap②）：每次工具调用落库。store 缺失时 sink nil → recorder no-op。
	mws = append(mws, einoagent.NewToolRecorder(h.toolSink(), hunterID, ownerType, ownerID))
	// 截图视觉回灌（TODO-1）：run_command 的截图 image part 从 tool message 抽出转 user message
	// （避免 mimo 400），按 role 的 provider 是否 vision 决定回灌或丢弃。
	mws = append(mws, einoagent.NewVisionRelayMiddleware(h.einoFactory.SupportsVisionFor(role)))
	var extraCallbacks []callbacks.Handler
	if sink != nil {
		mws = append(mws, einoagent.NewEventEmitter(sink))
		// reasoning 事件（思路文字 + 输入/输出 token + 耗时）走 callbacks 一站式捕获（OnStart→OnEnd）。
		if cb := einoagent.NewReasoningCallback(sink); cb != nil {
			extraCallbacks = append(extraCallbacks, cb)
		}
	}

	provider, model := h.einoFactory.ResolveProviderModel(role)
	recorder := einollm.NewUsageRecorder(h.calls,
		llm.CallMeta{HunterID: &hunterID, OwnerType: &ownerType, OwnerID: &ownerID, RouteKey: role},
		provider, model,
	)
	handlers := append([]callbacks.Handler{recorder}, extraCallbacks...)
	return mws, []adk.AgentRunOption{adk.WithCallbacks(handlers...)}, cleanup
}
