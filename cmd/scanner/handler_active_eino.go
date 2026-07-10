package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	hunterbuilder "github.com/V3teran/liusha/internal/builder/hunter"
	"github.com/V3teran/liusha/internal/einoagent"
	"github.com/V3teran/liusha/internal/scenario"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"
)

// handleActiveEino 是 active 扫描的唯一入口（旧 react 退路已删）。
//
// 用 eino deep prebuilt 装配：orchestrator 主代理 + 子代理（reconnaissance/exploitation），角色由
// hunters/*.md 动态加载（h.roles）。orchestrator 通过 deep 内建 task 工具按攻击面派活给子代理；
// 子代理串行（杀伤链本串行），子代理内部多工具并行（ToolsNode）。所有 agent 共享同一 ChatModel
// （per-model 铁律已作废，实测共享并发安全）+ 同一 sandbox 容器。
//
// 与旧 spawn 派活路径的差异：
//   - 派活机制：deep 内建 task 工具，替代自定义 spawn（删 spawn.go 依赖）
//   - 子代理是 deep 临时一次性 agent，不再为每个子代理建独立 hunter 行；finding/tool_invocation
//     落 orchestrator 的 hunter_id（用户已认可 hunter_id=orchestrator 的 deep 语义）
//   - token 用量：单 UsageRecorder callback 挂顶层 runner，经 ctx 传播到子代理模型调用（task_tool
//     透传 ctx）；role 按 Agent 边界真实产出的子代理名归集（见 usage_recorder #3），hunter_id 仍归 orchestrator
func (h handler) handleActiveEino(ctx context.Context, p worker.Payload, entrypoint json.RawMessage) error {
	var ep struct {
		Brief string `json:"brief"`
	}
	if err := json.Unmarshal(entrypoint, &ep); err != nil {
		return h.failTask(ctx, p.HunterID, err)
	}
	if ep.Brief == "" {
		return h.failTask(ctx, p.HunterID, fmt.Errorf("active entrypoint 缺 brief"))
	}

	tid := p.HunterID
	taskID := p.TaskID
	// 入口重置心跳：把 reaper 判活的起点从「API 建行」移到「worker 真正接手」，
	// 避免 task 在 asynq 队列里排队等待的时间吃掉 staleAfter 预算被冤杀。best-effort。
	if err := h.tasks.Heartbeat(ctx, taskID); err != nil {
		h.logger.Warn().Err(err).Str("task_id", taskID).Msg("task 入口心跳失败（不阻塞扫描）")
	}
	virtualHost := extractHostFromBrief(ep.Brief, taskID)
	if virtualHost != taskID {
		if err := h.tasks.SetTargetHost(ctx, taskID, virtualHost); err != nil {
			h.logger.Warn().Err(err).Str("task_id", taskID).Str("host", virtualHost).
				Msg("回填 task.target_host 失败（不阻塞扫描）")
		}
	}

	// deep 角色（hunters/*.md 加载）：恰一个 orchestrator + ≥1 子代理。
	orchestrator, err := einoagent.Orchestrator(h.roles)
	if err != nil {
		return h.failTask(ctx, p.HunterID, fmt.Errorf("deep 角色装配: %w", err))
	}
	subAgents := einoagent.SubAgents(h.roles)
	if len(subAgents) == 0 {
		return h.failTask(ctx, p.HunterID, fmt.Errorf("deep 角色装配: 无子代理（hunters/ 至少需一个 kind=subagent）"))
	}
	// 组装各角色的完整 system prompt（shared/exploitation 资产 + 角色 md body）。
	orchestrator.SystemPrompt = composeOrchestratorInstruction(orchestrator)
	// 阶段C：注入用户选的场景人设（web 渗透等）到 orchestrator。空/未匹配则不注入（通用扫描）。
	if scen, ok := scenario.ByID(h.scenarioRoles, p.ScenarioID); ok && scen.SystemPrompt != "" {
		orchestrator.SystemPrompt += "\n\n" + scen.SystemPrompt
		h.logger.Info().Str("scenario", scen.ID).Str("hunter_id", p.HunterID).Msg("注入场景人设到 orchestrator")
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
	params := einoagent.TrafficAnalysisToolParams{TaskID: taskID, Mode: "active", HunterID: tid, Host: virtualHost}

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
	})
	if err != nil {
		return h.failTask(ctx, p.HunterID, fmt.Errorf("BuildDeepSwarm: %w", err))
	}

	// orchestrator 的 user message：复用 buildUserPrompt 注入 brief + 流量/finding/lesson/索引段。
	orchestratorPrompt := hunterbuilder.BuildUserPrompt(ctx, h.hunterDeps, skill.BuilderParams{
		TaskID: taskID, HunterID: tid,
		Host: virtualHost, Mode: "active", Brief: ep.Brief, Sandbox: sandboxClient,
	})
	// 阶段0：多轮追问连贯性——把本对话最近的对话历史拼到 prompt 前，让 orchestrator 看到上下文
	// （如"刚才那个漏洞"）。首轮 / 无对话 / 读失败时为空串，不影响。
	if hist := h.conversationContext(ctx, p.ConversationID, "orchestrator", ep.Brief); hist != "" {
		orchestratorPrompt = hist + "\n" + orchestratorPrompt
	}

	// task 终态收尾（orchestrator 退出后无人收尾会卡 'active'）。
	finalizeScan := func(complete bool, reason string) {
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

	// task 中止 watcher：轮询 task.Status，非 active 即 cancel orchestrator。
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go h.watchAbortActive(runCtx, cancel, taskID)

	res, err := einoagent.RunDeepSwarm(runCtx, swarm, orchestratorPrompt, opts...)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			finalizeScan(false, "ctx "+err.Error())
			return h.abortTask(ctx, p.HunterID, "ctx "+err.Error())
		}
		finalizeScan(false, err.Error())
		return h.failTask(ctx, p.HunterID, err)
	}

	out, err := json.Marshal(map[string]any{
		"engine":     "eino-deep",
		"role":       "orchestrator",
		"tool_calls": res.ToolCalls,
		"final_text": res.FinalText,
	})
	if err != nil {
		finalizeScan(false, "marshal task result")
		return h.failTask(ctx, p.HunterID, fmt.Errorf("marshal task result: %w", err))
	}
	finalizeScan(true, "")
	return h.hunters.SetDone(ctx, p.HunterID, out)
}

// composeOrchestratorInstruction 组装 orchestrator 完整 system prompt：
// 公共底座（域上下文/黑板/finding 格式）+ 角色 md body（deep-native 编排 charter）。
// 主代理只用公共底座 + hunters/orchestrator.md，不复用任何编译期角色 addendum（那些是子代理深挖方法论）。
func composeOrchestratorInstruction(role einoagent.RoleDef) string {
	return hunterbuilder.SystemPrompt() + "\n\n" + role.SystemPrompt
}

// composeSubAgentInstruction 组装子代理完整 system prompt：公共底座 + 角色 md 自带的完整 charter
// （hunters/active/<role>.md，含深挖方法论 + 单攻击面框架）。与 orchestrator 同构——各角色 charter 自包含，
// 不再叠加编译期 exploitation addendum（已并入 hunters/active/exploitation.md，避免 recon 误吃 exploitation 方法论）。
func composeSubAgentInstruction(role einoagent.RoleDef) string {
	return hunterbuilder.SystemPrompt() + "\n\n" + role.SystemPrompt
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
