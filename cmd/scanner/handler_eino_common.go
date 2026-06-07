package main

import (
	"context"

	"github.com/cloudwego/eino/adk"

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
// sandboxClient 是本次 agent run 的容器（commander + striker 共享）。
func (h handler) einoToolDeps(sandboxClient sandbox.Client) einoagent.TrackerToolDeps {
	return einoagent.TrackerToolDeps{
		Findings:          h.findings,
		Notes:             h.notes,
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

// einoRunOpts 为一次 agent run（tracker/striker/commander）产 per-run 中间件 + 选项：
//   - 历史压缩 middleware（light compactor，装配失败降级跳过）
//   - 计费埋点 callbacks（按 hunterID/owner/role 落 llm_invocation）
//
// role ∈ tracker/striker/commander，决定 provider 解析 + 成本聚合维度。
func (h handler) einoRunOpts(ctx context.Context, hunterID, ownerType, ownerID, role string) ([]adk.AgentMiddleware, []adk.AgentRunOption) {
	var mws []adk.AgentMiddleware
	if compactor, err := h.einoFactory.For(ctx, "compactor"); err == nil {
		mws = append(mws, einoagent.NewCompactionMiddleware(compactor, einoagent.CompactionConfig{}))
	} else {
		h.logger.Warn().Err(err).Str("role", role).Msg("eino compactor 装配失败，本次跳过历史压缩")
	}
	// tool_invocation 遥测（gap②）：每次工具调用落库。store 缺失时 sink nil → recorder no-op。
	mws = append(mws, einoagent.NewToolRecorder(h.toolSink(), hunterID, ownerType, ownerID))
	// 截图视觉回灌（TODO-1）：run_command 的截图 image part 从 tool message 抽出转 user message
	// （避免 mimo 400），按 role 的 provider 是否 vision 决定回灌或丢弃。
	mws = append(mws, einoagent.NewVisionRelayMiddleware(h.einoFactory.SupportsVisionFor(role)))

	provider, model := h.einoFactory.ResolveProviderModel(role)
	recorder := einollm.NewUsageRecorder(h.calls, h.pricing,
		llm.CallMeta{HunterID: &hunterID, OwnerType: &ownerType, OwnerID: &ownerID, RouteKey: role},
		provider, model,
	)
	return mws, []adk.AgentRunOption{adk.WithCallbacks(recorder)}
}
