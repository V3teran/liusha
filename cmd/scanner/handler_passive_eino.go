package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cloudwego/eino/adk"

	hunterbuilder "github.com/V3teran/liusha/internal/builder/hunter"
	"github.com/V3teran/liusha/internal/einoagent"
	"github.com/V3teran/liusha/internal/einollm"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/passivesession"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/worker"
)

// abortPollInterval 是 eino passive 路径轮询 owner 中止状态的间隔。
// （react 路径走 cfg.OnAbort step 内回调；eino RunTracker 无 step 钩子，改后台 watcher + cancel ctx。）
const abortPollInterval = 5 * time.Second

// handlePassiveEino 是 handlePassive 的 eino 版（LIUSHA_EINO_PASSIVE=1 启用）：
// einollm.For(tracker) 独立 model + einoagent.BuildTrackerTools 13 工具 + hunter prompt 资产
// → einoagent.RunTracker（ChatModelAgent + Runner）替代 react.Run。
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
	model, err := h.einoFactory.For(ctx, "tracker")
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

	tools, err := einoagent.BuildTrackerTools(einoagent.TrackerToolDeps{
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
	}, einoagent.TrackerToolParams{
		OwnerType: ot,
		OwnerID:   oid,
		HunterID:  tid,
		Host:      ep.Host,
		FlowID:    ep.FlowID,
	})
	if err != nil {
		return h.failTask(ctx, p.HunterID, err)
	}

	instruction := hunterbuilder.SystemPromptFor("passive", true)
	userPrompt := hunterbuilder.BuildUserPrompt(ctx, h.hunterDeps, params)

	// LLM 计费埋点（替代旧 llm.Instrument）：按 run 注入 callbacks handler，
	// 每次 ChatModel 调用落 llm_invocation（owner/role 维度成本审计，与 react 同库）。
	provider, defaultModel := h.einoFactory.ResolveProviderModel("tracker")
	recorder := einollm.NewUsageRecorder(h.calls, h.pricing,
		llm.CallMeta{HunterID: &tid, OwnerType: &ot, OwnerID: &oid, RouteKey: "tracker"},
		provider, defaultModel,
	)

	// 历史压缩（gap③）：light 模型蒸馏老 turn 防 context 爆。compactor 解析失败仅降级跳过压缩。
	var middlewares []adk.AgentMiddleware
	if compactor, cErr := h.einoFactory.For(ctx, "compactor"); cErr == nil {
		middlewares = append(middlewares, einoagent.NewCompactionMiddleware(compactor, einoagent.CompactionConfig{}))
	} else {
		h.logger.Warn().Err(cErr).Msg("eino compactor 装配失败，本次跳过历史压缩")
	}

	// owner 中止 watcher：react 路径靠 step 内 cfg.OnAbort；eino 无 step 钩子，
	// 改后台轮询 passive_session.Status，非 active 即 cancel ctx 让 RunTracker 自然停。
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go h.watchAbort(runCtx, cancel, oid)

	res, err := einoagent.RunTracker(runCtx, model, tools, instruction, userPrompt, middlewares, adk.WithCallbacks(recorder))
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

// watchAbort 后台轮询 owner（passive_session）中止状态；非 active 即 cancel，让 RunTracker 停。
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
				h.logger.Info().Str("owner_id", ownerID).Msg("owner 中止，cancel eino tracker")
				cancel()
				return
			}
		}
	}
}
