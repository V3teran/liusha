package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	hunterbuilder "github.com/V3teran/liusha/internal/builder/hunter"
	cfghunter "github.com/V3teran/liusha/internal/config/hunter"
	cfgplaybook "github.com/V3teran/liusha/internal/config/playbook"
	cfgscenario "github.com/V3teran/liusha/internal/config/scenario"
	"github.com/V3teran/liusha/internal/einoagent"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"
)

// handleSwarmEino 是 swarm 引擎的入口（orchestrator + playbook domain 猎手动态派活）。
//
// 用 eino deep prebuilt 装配：主代理（全局唯一 orchestrator 猎手，按 kind='orchestrator' 从配置表取）
// + 子代理（scenario 引用的 playbook 内各 domain 猎手，按 position 有序）。orchestrator 通过 deep 内建
// task 工具按各子代理的 name+description 动态派活；子代理串行（杀伤链本串行），子代理内部多工具并行
// （ToolsNode）。所有 agent 共享同一 ChatModel（per-model 铁律已作废，实测共享并发安全）+ 同一 sandbox。
//
// 与旧 spawn 派活路径的差异：
//   - 派活机制：deep 内建 task 工具，替代自定义 spawn（删 spawn.go 依赖）
//   - 子代理是 deep 临时一次性 agent，不再为每个子代理建独立 hunter 行；finding/tool_invocation
//     落 orchestrator 的 hunter_id（用户已认可 hunter_id=orchestrator 的 deep 语义）
//   - token 用量：单 UsageRecorder callback 挂顶层 runner，经 ctx 传播到子代理模型调用（task_tool
//     透传 ctx）；role 按 Agent 边界真实产出的子代理名归集（见 usage_recorder #3），hunter_id 仍归 orchestrator
func (h handler) handleSwarmEino(ctx context.Context, p worker.Payload, scen cfgscenario.Scenario, pb cfgplaybook.Playbook, hunters []cfghunter.Hunter, brief string) error {
	if brief == "" {
		return h.failTask(ctx, p.HunterID, fmt.Errorf("swarm 引擎缺 brief"))
	}
	if len(hunters) == 0 {
		return h.failTask(ctx, p.HunterID, fmt.Errorf("playbook %s 无 domain 子代理", pb.Code))
	}

	tid := p.HunterID
	taskID := p.TaskID
	// 入口重置心跳：把 reaper 判活的起点从「API 建行」移到「worker 真正接手」，
	// 避免 task 在 asynq 队列里排队等待的时间吃掉 staleAfter 预算被冤杀。best-effort。
	if err := h.tasks.Heartbeat(ctx, taskID); err != nil {
		h.logger.Warn().Err(err).Str("task_id", taskID).Msg("task 入口心跳失败（不阻塞扫描）")
	}
	virtualHost := extractHostFromBrief(brief, taskID)
	if virtualHost != taskID {
		if err := h.tasks.SetTargetHost(ctx, taskID, virtualHost); err != nil {
			h.logger.Warn().Err(err).Str("task_id", taskID).Str("host", virtualHost).
				Msg("回填 task.target_host 失败（不阻塞扫描）")
		}
	}

	// 主代理：全局唯一 orchestrator 猎手（按 kind='orchestrator' 从配置表取，非从 playbook 取）。
	orchHunter, err := h.cfgStore.Orchestrator(ctx)
	if err != nil {
		return h.failTask(ctx, p.HunterID, fmt.Errorf("swarm 缺全局 orchestrator 猎手: %w", err))
	}
	orchestrator := hunterDefFromConfig(orchHunter)
	// 子代理：playbook 内各 domain 猎手（hunters 参数已按 position 有序）。
	subAgents := make([]einoagent.HunterDef, 0, len(hunters))
	for _, hn := range hunters {
		subAgents = append(subAgents, hunterDefFromConfig(hn))
	}
	// 组装各角色的完整 system prompt（公共底座 + 猎手 body）。
	orchestrator.SystemPrompt = composeOrchestratorInstruction(orchestrator)
	// 场景领域侧重（scen.Instruction）注入 orchestrator。空则不注入（通用扫描）。
	if scen.Instruction != "" {
		orchestrator.SystemPrompt += "\n\n" + scen.Instruction
		h.logger.Info().Str("scenario", scen.Code).Str("hunter_id", p.HunterID).Msg("注入场景侧重到 orchestrator")
	}
	for i := range subAgents {
		subAgents[i].SystemPrompt = composeSubAgentInstruction(subAgents[i])
	}

	model, err := h.einoFactory.For(ctx, "orchestrator") // orchestrator + 所有子代理共享此 model
	if err != nil {
		return h.failTask(ctx, p.HunterID, err)
	}

	// orchestrator + 子代理共享 sandbox 容器；defer Destroy 覆盖正常/异常/panic。
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

	toolDeps := h.einoToolDeps(sandboxClient)
	// 所有 agent（orchestrator + 子代理）的工具都用 orchestrator 的注入值建（owner/host/hunter=orchestrator）。
	// 子代理写 finding/note 落 orchestrator hunter_id（deep 临时子代理无独立 id，用户已认可）。
	params := einoagent.TrafficAnalysisToolParams{TaskID: taskID, HunterID: tid, Host: virtualHost}

	// per-run 中间件（压缩 / tool_invocation 遥测 / 截图回灌）+ 计费 callback。
	// 中间件挂到 orchestrator 与所有子代理（截图回灌尤其需在跑 run_command 的子代理上）。
	// 计费 callback 经顶层 runner ctx 传播到子代理模型调用。
	mws, agentHandlers, opts, cleanup, err := h.einoRunOpts(ctx, tid, taskID, "orchestrator", p.ConversationID)
	if err != nil {
		return h.failTask(ctx, p.HunterID, fmt.Errorf("einoRunOpts: %w", err))
	}
	defer cleanup() // run 结束后 flush 异步事件 sink（关 channel + 等缓冲事件写完落库）

	swarm, err := einoagent.BuildDeepSwarm(ctx, einoagent.DeepSwarmConfig{
		Model:        model,
		Orchestrator: orchestrator,
		SubAgents:    subAgents,
		ToolDeps:     toolDeps,
		Params:       params,
		Middlewares:  mws,
		Handlers:     agentHandlers,
		MaxIteration: orchestrator.MaxIterations,
		Logger:       h.logger,
	})
	if err != nil {
		return h.failTask(ctx, p.HunterID, fmt.Errorf("BuildDeepSwarm: %w", err))
	}

	// orchestrator 的 user message：复用 buildUserPrompt 注入 brief + 流量/finding/lesson/索引段。
	orchestratorPrompt := hunterbuilder.BuildUserPrompt(ctx, h.hunterDeps, skill.BuilderParams{
		TaskID: taskID, HunterID: tid,
		Host: virtualHost, Brief: brief, Sandbox: sandboxClient,
	})
	// 阶段0：多轮追问连贯性——把本会话最近的会话历史拼到 prompt 前，让 orchestrator 看到上下文
	// （如"刚才那个漏洞"）。首轮 / 无会话 / 读失败时为空串，不影响。
	if hist := h.conversationContext(ctx, p.ConversationID, "orchestrator", brief); hist != "" {
		orchestratorPrompt = hist + "\n" + orchestratorPrompt
	}

	// task 终态收尾（orchestrator 退出后无人收尾会卡 'active'）。
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
		// 情报黑板（lead）过期不再在收尾特判：改由 lead.Store 每次写滚动刷新 TTL（active/passive 一视同仁）。
	}

	// task 中止 watcher：轮询 task.Status，非 active 即 cancel orchestrator。
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go h.watchAbortActive(runCtx, cancel, taskID)

	res, err := einoagent.RunDeepSwarm(runCtx, swarm, orchestratorPrompt, opts...)
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
		"role":       "orchestrator",
		"tool_calls": res.ToolCalls,
		"final_text": res.FinalText,
	})
	if err != nil {
		finalizeTask(false, "marshal task result")
		return h.failTask(ctx, p.HunterID, fmt.Errorf("marshal task result: %w", err))
	}
	finalizeTask(true, "")
	// 收尾反思蒸馏（§5.2）：正常 complete 才提炼跨目标知识（best-effort，不阻塞收尾）。
	h.distillCorpus(ctx, taskID, p.ConversationID, "orchestrator", virtualHost)
	return h.hunters.SetDone(ctx, p.HunterID, out)
}

// hunterDefFromConfig 把配置层猎手（cfghunter.Hunter）映射成 einoagent 的 HunterDef。
// kind 需翻译——两侧词汇不同（配置层用 orchestrator/domain，einoagent 用 orchestrator/subagent/solo，见 D1/M2）：
// domain→HunterSubAgent、orchestrator→HunterOrchestrator。Body 即猎手方法论正文，作 SystemPrompt；
// Code 不进 HunterDef（无此字段），SourceFile 在 DB 事实源下留空。
func hunterDefFromConfig(h cfghunter.Hunter) einoagent.HunterDef {
	kind := einoagent.HunterSubAgent
	if h.Kind == cfghunter.KindOrchestrator {
		kind = einoagent.HunterOrchestrator
	}
	return einoagent.HunterDef{
		ID:            h.Code,
		Name:          h.Name,
		Description:   h.Description,
		Kind:          kind,
		Tools:         h.Tools,
		MaxIterations: h.MaxIterations,
		SystemPrompt:  h.Body,
	}
}

// composeOrchestratorInstruction 组装 orchestrator 完整 system prompt：
// 公共底座（域上下文/黑板/finding 格式）+ 角色 md body（deep-native 编排 charter）。
// 主代理只用公共底座 + hunters/orchestrator.md，不复用任何编译期角色 addendum（那些是子代理深挖方法论）。
func composeOrchestratorInstruction(h einoagent.HunterDef) string {
	return hunterbuilder.SystemPrompt() + "\n\n" + h.SystemPrompt
}

// composeSubAgentInstruction 组装子代理完整 system prompt：公共底座 + 角色 md 自带的完整 charter
// （hunters/active/<role>.md，含深挖方法论 + 单攻击面框架）。与 orchestrator 同构——各角色 charter 自包含，
// 不再叠加编译期 exploitation addendum（已并入 hunters/active/exploitation.md，避免 recon 误吃 exploitation 方法论）。
func composeSubAgentInstruction(h einoagent.HunterDef) string {
	return hunterbuilder.SystemPrompt() + "\n\n" + h.SystemPrompt
}

// watchAbortActive 后台轮询 task 中止状态；非 active 即 cancel，让 RunDeepSwarm 停。
func (h handler) watchAbortActive(ctx context.Context, cancel context.CancelFunc, taskID string) {
	ticker := time.NewTicker(abortPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tk, err := h.tasks.GetByID(ctx, taskID)
			if err != nil {
				continue
			}
			if tk.Status != task.StatusActive {
				h.logger.Info().Str("task_id", taskID).Msg("task 中止，cancel eino deep orchestrator")
				cancel()
				return
			}
		}
	}
}
