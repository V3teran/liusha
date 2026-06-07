package main

import (
	"context"

	"github.com/cloudwego/eino/adk"

	"github.com/V3teran/liusha/internal/einoagent"
	"github.com/V3teran/liusha/internal/einollm"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/sandbox"
)

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

	provider, model := h.einoFactory.ResolveProviderModel(role)
	recorder := einollm.NewUsageRecorder(h.calls, h.pricing,
		llm.CallMeta{HunterID: &hunterID, OwnerType: &ownerType, OwnerID: &ownerID, RouteKey: role},
		provider, model,
	)
	return mws, []adk.AgentRunOption{adk.WithCallbacks(recorder)}
}
