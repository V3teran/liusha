package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	hunterbuilder "github.com/V3teran/liusha/internal/builder/hunter"
	cfghunter "github.com/V3teran/liusha/internal/config/hunter"
	cfgscenario "github.com/V3teran/liusha/internal/config/scenario"
	"github.com/V3teran/liusha/internal/einoagent"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"
)

// abortPollInterval 是 eino solo 路径轮询 owner 中止状态的间隔。
// （react 路径走 cfg.OnAbort step 内回调；eino RunSolo 无 step 钩子，改后台 watcher + cancel ctx。）
const abortPollInterval = 5 * time.Second

// handleSoloEino 是 solo 引擎入口（单代理，无编排）。
//
// 装配规则（对应 D2）：scenario.solo_hunter_id 指向唯一执行 hunter。以该 hunter 的 code 为
// LLM 路由 role（einoFactory.For 未配置即回退默认 provider）、body 为 system 指令（scen.Instruction
// 作领域侧重置于其前）、Tools 为其内置工具集 → einoagent.RunSolo（ChatModelAgent + Runner）。
//
// 与 swarm 路径共享：sandbox 生命周期、prompt 资产、stores、owner 中止语义、per-run 中间件（einoRunOpts）。
func (h handler) handleSoloEino(ctx context.Context, p worker.Payload, scen cfgscenario.Scenario, hunter cfghunter.Hunter, brief string) error {
	if brief == "" {
		return h.failTask(ctx, p.HunterID, fmt.Errorf("solo 引擎缺 brief"))
	}

	role := hunter.Code // LLM 路由键 + 计费/会话归属键（全 handler 统一用它，不再硬编码）
	tid := p.HunterID
	taskID := p.TaskID
	// 入口重置心跳：把 reaper 判活的起点从「建 task」移到「worker 真正接手」，避免排队时间吃掉
	// staleAfter 预算被冤杀。best-effort。
	if err := h.tasks.Heartbeat(ctx, taskID); err != nil {
		h.logger.Warn().Err(err).Str("task_id", taskID).Msg("task 入口心跳失败（不阻塞分析）")
	}
	// host 从 brief 抽取回填（派生列，与 swarm 同源）。
	host := extractHostFromBrief(brief, taskID)
	if host != taskID {
		if err := h.tasks.SetTargetHost(ctx, taskID, host); err != nil {
			h.logger.Warn().Err(err).Str("task_id", taskID).Str("host", host).
				Msg("回填 task.target_host 失败（不阻塞分析）")
		}
	}

	// per-hunter 独立 eino ChatModel（铁律）：按该 hunter 的 code 路由 provider/model。
	model, err := h.einoFactory.For(ctx, role)
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

	// fan-in：全读本 task 认领的整批 proxy_traffic 填 Flows，BuildUserPrompt 全量渲染成流量清单
	// （摘要 + body 预览）推进 prompt——流量驱动场景（traffic-analysis）开箱即见全部流量，不必靠
	// list_traffic 发现；非流量场景（CTF/主动扫描等）该 task 无认领流量，返回空切片，渲染为空段落，无害。
	flows, err := h.proxyFlows.ListByTask(ctx, taskID)
	if err != nil {
		return h.failTask(ctx, p.HunterID, fmt.Errorf("全读本批 proxy_traffic 失败: %w", err))
	}
	params := skill.BuilderParams{
		TaskID:   taskID,
		HunterID: tid,
		Host:     host,
		Brief:    brief,
		Flows:    flows,
		Sandbox:  sandboxClient,
		CliTools: hunter.CliTools,
	}

	// tools 取该 hunter 的内置工具集：复用 BuildHunterTools 的注册表建法（与 swarm 子代理同源
	// 门控——依赖缺失即报错，暴露配置缺漏）。
	tools, err := einoagent.BuildHunterTools(hunterDefFromConfig(hunter), einoagent.ToolBuildCtx{
		Deps: h.einoToolDeps(sandboxClient),
		Params: einoagent.TrafficAnalysisToolParams{
			TaskID:   taskID,
			HunterID: tid,
			Host:     host,
		},
	})
	if err != nil {
		return h.failTask(ctx, p.HunterID, err)
	}

	// instruction：公共底座 + 该 hunter 的 body（scen.Instruction 作领域侧重置于 body 前）。
	instruction := composeSoloInstruction(scen, hunter)
	// maxIters 取该 hunter 的 MaxIterations（0 = 用 RunSolo 默认）。
	maxIters := hunter.MaxIterations
	userPrompt := hunterbuilder.BuildUserPrompt(ctx, h.hunterDeps, params)

	// 多轮追问连贯性：把本会话最近历史（含用户实时指导）拼到 prompt 前，让 agent 看到上下文、
	// 调整方向（与 swarm orchestrator 同源 conversationContext）。首轮/无会话/读失败时为空串。
	if hist := h.conversationContext(ctx, p.ConversationID, role, brief); hist != "" {
		userPrompt = hist + "\n" + userPrompt
	}

	// per-run 中间件 + 计费 callback：复用 einoRunOpts（与 active deep 路径同源）——
	// compaction（防 context 爆）+ tool_invocation 遥测 + 截图回灌 + llm_invocation 计费。
	// ★ 早期 passive handler 手工只挂了 compaction + 计费，漏了 ToolRecorder（→ tool_invocation
	// 不落库）和 VisionRelay（→ trafficAnalysis 跑 run_command 截图会 mimo 400）。统一走 einoRunOpts 补齐。
	mws, agentHandlers, opts, cleanup, err := h.einoRunOpts(ctx, tid, taskID, role, p.ConversationID)
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
	// 非 active 即 cancel ctx 让 RunSolo 自然停。
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go h.watchAbort(runCtx, cancel, taskID)

	res, err := einoagent.RunSolo(runCtx, scen.Code, scen.Description, model, tools, instruction, userPrompt, maxIters, mws, agentHandlers, h.logger, opts...)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			finalizeTask(false, "ctx "+err.Error())
			return h.abortTask(ctx, p.HunterID, "ctx "+err.Error())
		}
		finalizeTask(false, err.Error())
		return h.failTask(ctx, p.HunterID, err)
	}

	out, err := json.Marshal(map[string]any{
		"engine":     string(scen.Engine),
		"tool_calls": res.ToolCalls,
		"final_text": res.FinalText,
	})
	if err != nil {
		finalizeTask(false, "marshal task result")
		return h.failTask(ctx, p.HunterID, fmt.Errorf("marshal task result: %w", err))
	}
	finalizeTask(true, "")
	// 收尾反思蒸馏（§5.2）：正常 complete 才提炼跨目标知识（best-effort，不阻塞收尾）。
	h.distillCorpus(ctx, taskID, p.ConversationID, role, host)
	return h.hunters.SetDone(ctx, p.HunterID, out)
}

// composeSoloInstruction 拼 solo 单代理 system 指令：公共底座 + 该 hunter 的 body
// （scen.Instruction 作领域侧重置于 body 前）。
func composeSoloInstruction(scen cfgscenario.Scenario, hunter cfghunter.Hunter) string {
	var b strings.Builder
	b.WriteString(hunterbuilder.SystemPrompt())
	if scen.Instruction != "" {
		b.WriteString("\n\n")
		b.WriteString(scen.Instruction)
	}
	if hunter.Body != "" {
		b.WriteString("\n\n")
		b.WriteString(hunter.Body)
	}
	return b.String()
}

// watchAbort 后台轮询 task 中止状态；非 active 即 cancel，让 RunSolo 停。
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
