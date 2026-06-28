package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	hunterbuilder "github.com/V3teran/liusha/internal/builder/hunter"
	"github.com/V3teran/liusha/internal/einoagent"
	"github.com/V3teran/liusha/internal/passivesession"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/worker"
)

// abortPollInterval 是 eino passive 路径轮询 owner 中止状态的间隔。
// （react 路径走 cfg.OnAbort step 内回调；eino RunTrafficAnalysis 无 step 钩子，改后台 watcher + cancel ctx。）
const abortPollInterval = 5 * time.Second

// handlePassiveEino 是 handlePassive 的 eino 版（默认路径；LIUSHA_USE_REACT=1 才切回旧 react）：
// einollm.For(trafficAnalysis) 独立 model + einoagent.BuildTrafficAnalysisTools 13 工具 + hunter prompt 资产
// → einoagent.RunTrafficAnalysis（ChatModelAgent + Runner）替代 react.Run。
//
// 与 react 路径共享：sandbox 生命周期、prompt 资产、stores、owner 中止语义。
// gap（待后续 eino middleware 增量补）：LLM 调用计费 instrument、inspector terminate/hints、history 压缩。
func (h handler) handlePassiveEino(ctx context.Context, p worker.Payload, entrypoint json.RawMessage) error {
	var ep struct {
		FlowID int64  `json:"flow_id"`
		Host   string `json:"host"`
		URL    string `json:"url"`
		Method string `json:"method"`
	}
	if err := json.Unmarshal(entrypoint, &ep); err != nil {
		return h.failTask(ctx, p.HunterID, err)
	}

	tid := p.HunterID
	ot, oid := p.OwnerType, p.OwnerID

	// per-hunter 独立 eino ChatModel（铁律）
	model, err := h.einoFactory.For(ctx, "traffic-analysis")
	if err != nil {
		return h.failTask(ctx, p.HunterID, err)
	}

	// 拉 flow 完整 raw（请求 + 响应）
	fl, err := h.flows.GetByID(ctx, ep.FlowID)
	if err != nil {
		return h.failTask(ctx, p.HunterID, fmt.Errorf("flows.GetByID(%d): %w", ep.FlowID, err))
	}

	// 为本次 agent run 启动 sandbox 容器；defer Destroy 覆盖正常/异常/panic。
	sandboxClient, err := h.launcher.Spawn(ctx, p.HunterID)
	if err != nil {
		return h.failTask(ctx, p.HunterID, fmt.Errorf("launcher.Spawn(%s): %w", p.HunterID, err))
	}
	defer func() {
		destroyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := h.launcher.Destroy(destroyCtx, p.HunterID); err != nil {
			h.logger.Warn().Err(err).Str("hunter_id", p.HunterID).
				Msg("launcher.Destroy 失败（max lifetime / 下次启动 CleanupOrphans 兜底）")
		}
	}()

	// BuilderParams：与 handlePassive 同构，喂 hunter.BuildUserPrompt 拼流量/finding/notes/lesson/索引段。
	params := skill.BuilderParams{
		OwnerType:       ot,
		OwnerID:         oid,
		HunterID:        tid,
		Mode:            "passive",
		FlowID:          ep.FlowID,
		Host:            ep.Host,
		URL:             ep.URL,
		Method:          ep.Method,
		RequestHeaders:  fl.RequestHeaders,
		RequestBody:     fl.RequestBody,
		ResponseStatus:  fl.StatusCode,
		ResponseHeaders: fl.ResponseHeaders,
		ResponseBody:    fl.ResponseBody,
		Sandbox:         sandboxClient,
	}

	tools, err := einoagent.BuildTrafficAnalysisTools(einoagent.TrafficAnalysisToolDeps{
		Findings:          h.findings,
		Lessons:           h.lessons,
		Credentials:       h.hunterDeps.Credentials,
		Flows:             h.flows,
		ToolingLoader:     h.hunterDeps.ToolingLoader,
		VulnLoader:        h.hunterDeps.VulnLoader,
		Sandbox:           sandboxClient,
		MaxTimeoutSeconds: h.cfg.Toolruntime.StepToolTimeoutSeconds,
		TailBytes:         h.cfg.Sandbox.RunTailBytes,
	}, einoagent.TrafficAnalysisToolParams{
		OwnerType: ot,
		OwnerID:   oid,
		HunterID:  tid,
		Host:      ep.Host,
		FlowID:    ep.FlowID,
	})
	if err != nil {
		return h.failTask(ctx, p.HunterID, err)
	}

	instruction := hunterbuilder.SystemPrompt() + "\n\n" + h.passiveRole.SystemPrompt
	userPrompt := hunterbuilder.BuildUserPrompt(ctx, h.hunterDeps, params)

	// 阶段2 可插话：把本 passive 会话最近的对话历史（含用户插话指导）拼到 prompt 前，
	// 让 traffic agent 看到用户实时指导、调整分析方向（与 active orchestrator 同源 conversationContext）。
	if hist := h.conversationContext(ctx, p.ConversationID, "traffic-analysis", ""); hist != "" {
		userPrompt = hist + "\n" + userPrompt
	}

	// per-run 中间件 + 计费 callback：复用 einoRunOpts（与 active deep 路径同源）——
	// compaction（防 context 爆）+ tool_invocation 遥测 + 截图回灌 + llm_invocation 计费。
	// ★ 早期 passive handler 手工只挂了 compaction + 计费，漏了 ToolRecorder（→ tool_invocation
	// 不落库）和 VisionRelay（→ trafficAnalysis 跑 run_command 截图会 mimo 400）。统一走 einoRunOpts 补齐。
	mws, agentHandlers, opts, cleanup, err := h.einoRunOpts(ctx, tid, ot, oid, "traffic-analysis", p.ConversationID)
	if err != nil {
		return h.failTask(ctx, p.HunterID, fmt.Errorf("einoRunOpts: %w", err))
	}
	defer cleanup() // run 结束后 flush 异步事件 sink（关 channel + 等缓冲事件写完落库）

	// owner 中止 watcher：react 路径靠 step 内 cfg.OnAbort；eino 无 step 钩子，
	// 改后台轮询 passive_session.Status，非 active 即 cancel ctx 让 RunTrafficAnalysis 自然停。
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go h.watchAbort(runCtx, cancel, oid)

	res, err := einoagent.RunTrafficAnalysis(runCtx, model, tools, instruction, userPrompt, h.passiveRole.MaxIterations, mws, agentHandlers, opts...)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return h.abortTask(ctx, p.HunterID, "ctx "+err.Error())
		}
		return h.failTask(ctx, p.HunterID, err)
	}

	out, err := json.Marshal(map[string]any{
		"engine":     "eino",
		"tool_calls": res.ToolCalls,
		"final_text": res.FinalText,
	})
	if err != nil {
		return h.failTask(ctx, p.HunterID, fmt.Errorf("marshal task result: %w", err))
	}
	return h.tasks.SetDone(ctx, p.HunterID, out)
}

// watchAbort 后台轮询 owner（passive_session）中止状态；非 active 即 cancel，让 RunTrafficAnalysis 停。
func (h handler) watchAbort(ctx context.Context, cancel context.CancelFunc, ownerID string) {
	ticker := time.NewTicker(abortPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sess, err := h.passiveSessions.GetByID(ctx, ownerID)
			if err != nil {
				continue // 短时不可用：下个 tick 再查，不误杀
			}
			if sess.Status != passivesession.StatusActive {
				h.logger.Info().Str("owner_id", ownerID).Msg("owner 中止，cancel eino trafficAnalysis")
				cancel()
				return
			}
		}
	}
}
