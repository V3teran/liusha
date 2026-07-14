package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	hunterbuilder "github.com/V3teran/liusha/internal/builder/hunter"
	"github.com/V3teran/liusha/internal/einoagent"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
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
		Host string `json:"host"`
	}
	if err := json.Unmarshal(entrypoint, &ep); err != nil {
		return h.failTask(ctx, p.HunterID, err)
	}

	tid := p.HunterID
	taskID := p.TaskID
	// 入口重置心跳：把 reaper 判活的起点从「建 task」移到「worker 真正接手」，避免排队时间吃掉
	// staleAfter 预算被冤杀。best-effort。
	if err := h.tasks.Heartbeat(ctx, taskID); err != nil {
		h.logger.Warn().Err(err).Str("task_id", taskID).Msg("task 入口心跳失败（不阻塞分析）")
	}

	// per-hunter 独立 eino ChatModel（铁律）
	model, err := h.einoFactory.For(ctx, "traffic-analysis")
	if err != nil {
		return h.failTask(ctx, p.HunterID, err)
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

	// BuilderParams：passive fan-in 批分析——不再渲染单条 raw 流量，agent 用 list_flows/view_flow
	// 枚举本批消费的 proxy_traffic（consumed_by_task_id=本 task）自行深挖。喂 finding/lesson/索引段。
	params := skill.BuilderParams{
		TaskID:   taskID,
		HunterID: tid,
		Mode:     "passive",
		Host:     ep.Host,
		Sandbox:  sandboxClient,
	}

	tools, err := einoagent.BuildTrafficAnalysisTools(einoagent.TrafficAnalysisToolDeps{
		Findings:          h.findings,
		Corpus:            h.corpus,
		Embedder:          h.embedder,
		Reranker:          h.reranker,
		Credentials:       h.hunterDeps.Credentials,
		Lead:              h.leads,
		ProxyFlows:        h.proxyFlows,
		ToolingLoader:     h.hunterDeps.ToolingLoader,
		VulnLoader:        h.hunterDeps.VulnLoader,
		Sandbox:           sandboxClient,
		MaxTimeoutSeconds: h.cfg.Toolruntime.StepToolTimeoutSeconds,
		TailBytes:         h.cfg.Sandbox.RunTailBytes,
	}, einoagent.TrafficAnalysisToolParams{
		TaskID:   taskID,
		Mode:     "passive",
		HunterID: tid,
		Host:     ep.Host,
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
	mws, agentHandlers, opts, cleanup, err := h.einoRunOpts(ctx, tid, taskID, "traffic-analysis", p.ConversationID)
	if err != nil {
		return h.failTask(ctx, p.HunterID, fmt.Errorf("einoRunOpts: %w", err))
	}
	defer cleanup() // run 结束后 flush 异步事件 sink（关 channel + 等缓冲事件写完落库）

	// task 终态收尾（trafficAnalysis 退出后无人收尾会卡 'active'）。§6.3 设计口径
	// "passive task 是有界批分析，跑完即终态"此前只有文档没有代码——正常跑完从未显式
	// Complete，只能靠 reaper 心跳超时误判为 aborted（语义错误：明明正常收工却被记成超时中止）。
	finalizeTask := func(complete bool, reason string) {
		fctx, fcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer fcancel()
		var ferr error
		if complete {
			ferr = h.tasks.Complete(fctx, taskID)
		} else {
			ferr = h.tasks.Abort(fctx, taskID, reason)
		}
		if ferr != nil {
			h.logger.Warn().Err(ferr).Str("task_id", taskID).Bool("complete", complete).
				Msg("task 终态写失败（task 可能卡 active，待人工排查）")
		}
	}

	// task 中止 watcher：eino 无 step 钩子，改后台轮询 task.Status，
	// 非 active 即 cancel ctx 让 RunTrafficAnalysis 自然停。
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go h.watchAbort(runCtx, cancel, taskID)

	res, err := einoagent.RunTrafficAnalysis(runCtx, model, tools, instruction, userPrompt, h.passiveRole.MaxIterations, mws, agentHandlers, h.logger, opts...)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			finalizeTask(false, "ctx "+err.Error())
			return h.abortTask(ctx, p.HunterID, "ctx "+err.Error())
		}
		finalizeTask(false, err.Error())
		return h.failTask(ctx, p.HunterID, err)
	}

	out, err := json.Marshal(map[string]any{
		"engine":     "eino",
		"tool_calls": res.ToolCalls,
		"final_text": res.FinalText,
	})
	if err != nil {
		finalizeTask(false, "marshal task result")
		return h.failTask(ctx, p.HunterID, fmt.Errorf("marshal task result: %w", err))
	}
	finalizeTask(true, "")
	// 收尾反思蒸馏（§5.2）：正常 complete 才提炼跨目标知识（best-effort，不阻塞收尾）。
	h.distillCorpus(ctx, taskID, p.ConversationID, "traffic-analysis", ep.Host)
	return h.hunters.SetDone(ctx, p.HunterID, out)
}

// watchAbort 后台轮询 task 中止状态；非 active 即 cancel，让 RunTrafficAnalysis 停。
func (h handler) watchAbort(ctx context.Context, cancel context.CancelFunc, taskID string) {
	ticker := time.NewTicker(abortPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tk, err := h.tasks.GetByID(ctx, taskID)
			if err != nil {
				continue // 短时不可用：下个 tick 再查，不误杀
			}
			if tk.Status != task.StatusActive {
				h.logger.Info().Str("task_id", taskID).Msg("task 中止，cancel eino trafficAnalysis")
				cancel()
				return
			}
		}
	}
}
