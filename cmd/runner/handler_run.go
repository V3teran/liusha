package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/actor"
	executorbuilder "github.com/V3teran/liusha/internal/builder/executor"
	cfgagent "github.com/V3teran/liusha/internal/config/agent"
	cfgscenario "github.com/V3teran/liusha/internal/config/scenario"
	"github.com/V3teran/liusha/internal/dispatcher"
	"github.com/V3teran/liusha/internal/planner"
	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/scanagent"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/tools"
	"github.com/V3teran/liusha/internal/worker"
)

// abortPollInterval is the task-status poll cadence for the abort watcher.
const abortPollInterval = 5 * time.Second

// heartbeatThrottleMs: minimum gap between consecutive heartbeat writes.
const heartbeatThrottleMs = 10_000

// ─────────────────────────────────────────────────────────────
//  Tool-invocation recorder (heartbeat + DB telemetry)
// ─────────────────────────────────────────────────────────────

// toolRecordInterceptor returns a registry.Interceptor that records every tool
// call to toolinvocation.Store and throttles task heartbeats.
func (h handler) toolRecordInterceptor(executorID, taskID string) registry.Interceptor {
	lastBeatMs := new(atomic.Int64)
	return func(ctx context.Context, t registry.Tool, args []byte, next registry.ExecuteFunc) (registry.ToolResult, error) {
		start := time.Now()
		res, err := next(ctx, t, args)
		durMs := int(time.Since(start).Milliseconds())
		errMsg := ""
		if res.Error != "" {
			errMsg = res.Error
		} else if err != nil {
			errMsg = err.Error()
		}
		// record tool invocation (best-effort)
		if h.toolCalls != nil {
			preview := res.Output
			if len(preview) > 512 {
				preview = preview[:512]
			}
			_, _ = h.toolCalls.Append(ctx, toolinvocation.Invocation{
				ExecutorID:    executorID,
				TaskID:        taskID,
				ToolName:      t.Name(),
				Args:          json.RawMessage(args),
				OutputSize:    len(res.Output),
				OutputPreview: preview,
				DurationMs:    durMs,
				ErrorMessage:  errMsg,
			})
		}
		// throttled heartbeat
		if taskID != "" {
			now := time.Now().UnixMilli()
			last := lastBeatMs.Load()
			if now-last >= heartbeatThrottleMs {
				if lastBeatMs.CompareAndSwap(last, now) {
					hctx, hcancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer hcancel()
					if h.tasks != nil {
						_ = h.tasks.Heartbeat(hctx, taskID)
					}
				}
			}
		}
		return res, err
	}
}

// ─────────────────────────────────────────────────────────────
//  No-op actor infrastructure stubs
// ─────────────────────────────────────────────────────────────

type noopCompactor struct{}

func (noopCompactor) Compact(_ context.Context, _ provider.Provider, msgs []provider.Message) ([]provider.Message, error) {
	return msgs, nil
}

// ─────────────────────────────────────────────────────────────
//  SSE emitter bridging actor.SSEEvent → scanagent.ScanEvent
// ─────────────────────────────────────────────────────────────

type sseEmitterAdapter struct{ sink scanagent.EventSink }

func (a *sseEmitterAdapter) Emit(ev actor.SSEEvent) {
	if a.sink == nil {
		return
	}
	a.sink.OnScanEvent(context.Background(), scanagent.ScanEvent{
		Kind: scanagent.ScanEventKind(ev.Kind),
		Text: fmt.Sprintf("move=%s step=%d", ev.MoveID, ev.StepID),
	})
}

// ─────────────────────────────────────────────────────────────
//  Prompt helpers
// ─────────────────────────────────────────────────────────────

// buildPromptDeps reconstructs an executorbuilder.Deps from the flat handler fields.
func (h handler) buildPromptDeps() executorbuilder.Deps {
	return executorbuilder.Deps{
		Findings:        h.findings,
		Credentials:     h.creds,
		Lead:            h.leads,
		ToolInvocations: h.toolCalls,
		ToolingLoader:   h.toolingLoader,
		ToolsManifest:   h.toolsManifest,
		VulnLoader:      h.vulnLoader,
	}
}

// composeSoloInstruction builds the full system prompt for a solo agent:
// shared base + optional scenario instruction + executor body.
func composeSoloInstruction(scen cfgscenario.Scenario, op cfgagent.Agent) string {
	var b strings.Builder
	b.WriteString(executorbuilder.SystemPrompt())
	if scen.Instruction != "" {
		b.WriteString("\n\n")
		b.WriteString(scen.Instruction)
	}
	if op.Body != "" {
		b.WriteString("\n\n")
		b.WriteString(op.Body)
	}
	return b.String()
}

// composeplannerInstruction builds the full system prompt for the planner agent.
func composeplannerInstruction(body string) string {
	return executorbuilder.SystemPrompt() + "\n\n" + body
}

// composeSubAgentInstruction builds the full system prompt for a sub-agent.
func composeSubAgentInstruction(body string) string {
	return executorbuilder.SystemPrompt() + "\n\n" + body
}

// swarmSystemPrompt combines planner + all sub-agent descriptions into a single
// system prompt for the unified swarm run.
func swarmSystemPrompt(orchBody, scenInstruction string, subAgents []cfgagent.Agent) string {
	var b strings.Builder
	b.WriteString(composeplannerInstruction(orchBody))
	if scenInstruction != "" {
		b.WriteString("\n\n")
		b.WriteString(scenInstruction)
	}
	if len(subAgents) > 0 {
		b.WriteString("\n\n## 可用专项代理\n")
		for _, a := range subAgents {
			b.WriteString(fmt.Sprintf("- **%s**: %s\n", a.Name, a.Description))
		}
	}
	return b.String()
}

// ─────────────────────────────────────────────────────────────
//  Dispatcher factory
// ─────────────────────────────────────────────────────────────

// buildDispatcher constructs a Dispatcher for a single run.
// The caller is responsible for registering tools into the returned registry.
func (h handler) buildDispatcher(
	ctx context.Context,
	complexity provider.Complexity,
	systemPrompt string,
	sink scanagent.EventSink,
) (*dispatcher.Dispatcher, *registry.Registry, error) {
	p, err := h.router.For(ctx, complexity)
	if err != nil {
		return nil, nil, fmt.Errorf("buildDispatcher: resolve provider complexity=%s: %w", complexity, err)
	}

	reg := registry.New()

	critic := actor.NewLLMCritic(p)

	var emitter actor.SSEEmitter
	if sink != nil {
		emitter = &sseEmitterAdapter{sink: sink}
	}

	d := dispatcher.New(p, reg, noopCompactor{}, critic, h.checkpoint, emitter)
	d.RegisterProfile(dispatcher.Profile{
		MoveKind:      actor.MoveKindEnumerate,
		SystemPrompt:  systemPrompt,
		Budget:        actor.DefaultBudget(),
		MaxExecutions: 1,
	})
	d.RegisterProfile(dispatcher.Profile{
		MoveKind:      actor.MoveKindProbe,
		SystemPrompt:  systemPrompt,
		Budget:        actor.DefaultBudget(),
		MaxExecutions: 2,
	})
	d.RegisterProfile(dispatcher.Profile{
		MoveKind:      actor.MoveKindExploit,
		SystemPrompt:  systemPrompt,
		Budget:        actor.DefaultBudget(),
		MaxExecutions: 3,
	})
	d.RegisterProfile(dispatcher.Profile{
		MoveKind:      actor.MoveKindEscalate,
		SystemPrompt:  systemPrompt,
		Budget:        actor.DefaultBudget(),
		MaxExecutions: 3,
	})
	d.RegisterProfile(dispatcher.Profile{
		MoveKind:      actor.MoveKindPersist,
		SystemPrompt:  systemPrompt,
		Budget:        actor.DefaultBudget(),
		MaxExecutions: 2,
	})

	return d, reg, nil
}

// ─────────────────────────────────────────────────────────────
//  Abort watchers
// ─────────────────────────────────────────────────────────────

// watchAbort polls task status; cancels ctx when the task leaves active.
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
				continue
			}
			if tk.Status != task.StatusActive {
				h.logger.Info().Str("task_id", taskID).Msg("task 中止，cancel actor run")
				cancel()
				return
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────
//  Solo handler
// ─────────────────────────────────────────────────────────────

// handleSolo runs a single-agent task via the dispatcher.
func (h handler) handleSolo(
	ctx context.Context,
	p worker.Payload,
	scen cfgscenario.Scenario,
	op cfgagent.Agent,
	brief string,
) error {
	if brief == "" {
		return h.failTask(ctx, p.ExecutorID, fmt.Errorf("solo 引擎缺 brief"))
	}

	tid := p.ExecutorID
	taskID := p.TaskID
	if err := h.tasks.Heartbeat(ctx, taskID); err != nil {
		h.logger.Warn().Err(err).Str("task_id", taskID).Msg("task 入口心跳失败（不阻塞）")
	}

	var assignmentID string
	if tk, err := h.tasks.GetByID(ctx, taskID); err == nil {
		assignmentID = tk.AssignmentID
	}
	host := h.onboard(ctx, assignmentID, taskID, brief)

	trafficList, err := h.proxyStore.ListByTask(ctx, taskID)
	if err != nil {
		return h.failTask(ctx, p.ExecutorID, fmt.Errorf("读 proxy_traffic 失败: %w", err))
	}
	rt, _ := h.settings.Runtime(ctx)
	params := skill.BuilderParams{
		TaskID:        taskID,
		ExecutorID:    tid,
		Host:          host,
		Brief:         brief,
		Traffic:       trafficList,
		Sandbox:       nil, // 沙箱容器在 Spawn 后通过工具注入
		CliTools:      op.CliTools,
		FindingsLimit: rt.FindingsLimitInPrompt,
	}

	instruction := composeSoloInstruction(scen, op)
	userPrompt := executorbuilder.BuildUserPrompt(ctx, h.buildPromptDeps(), params)
	if hist := h.conversationContext(ctx, p.ConversationID, op.Code, brief); hist != "" {
		userPrompt = hist + "\n" + userPrompt
	}

	// event sink for conversation streaming
	cleanup := func() {}
	var sink scanagent.EventSink
	if p.ConversationID != "" && h.conversations != nil && h.eventPublisher != nil {
		es := newEventSink(h.conversations, h.eventPublisher, p.ConversationID, h.logger)
		sink = es
		cleanup = es.Close
	}
	defer cleanup()

	// Sandbox 按 Assignment 粒度管理，多 Task 共享同一容器（引用计数）
	sandboxClient, err := h.sandboxMgr.Acquire(ctx, assignmentID)
	if err != nil {
		return h.failTask(ctx, p.ExecutorID, fmt.Errorf("sandboxMgr.Acquire(%s): %w", assignmentID, err))
	}
	// 任务结束时释放引用，引用计数归零后延迟清理容器
	defer func() {
		if err := h.sandboxMgr.Release(context.Background(), assignmentID); err != nil {
			h.logger.Warn().Err(err).Str("assignment_id", assignmentID).Msg("sandboxMgr.Release 失败")
		}
	}()

	d, reg, err := h.buildDispatcher(ctx, provider.ComplexityMedium, instruction, sink)
	if err != nil {
		return h.failTask(ctx, p.ExecutorID, err)
	}
	tools.RegisterAll(reg, tools.Deps{
		TaskID:        taskID,
		ExecutorID:    tid,
		Host:          host,
		Findings:      h.findings,
		Corpus:        h.corpus,
		Embedder:      h.embedder,
		Reranker:      h.reranker,
		Leads:         h.leads,
		ProxyStore:    h.proxyStore,
		AgentStore:    h.agentStore,
		Creds:         h.creds,
		Sandbox:       sandboxClient,
		ToolingLoader: h.toolingLoader,
		VulnLoader:    h.vulnLoader,
	})
	reg.AddInterceptor(h.toolRecordInterceptor(tid, taskID))

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
				Msg("task 终态写失败")
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go h.watchAbort(runCtx, cancel, taskID)

	var execResult []actor.Execution
	var agentErr error
	var moveID string
	runAgent := func(runCtx context.Context, m planner.Move) error {
		// 1. Move 开始前：记录到世界模型（spawns 边）
		if m.OnNodeID != "" && m.Kind != "" {
			mid, err := h.ledger.RecordMoveStart(runCtx, assignmentID, string(m.Kind), m.OnNodeID, m.Reason)
			if err != nil {
				h.logger.Warn().Err(err).Str("move_kind", string(m.Kind)).Msg("RecordMoveStart 失败（不阻塞）")
			} else {
				moveID = mid
			}
		}

		// 2. 执行 Move
		move := plannerMoveToActorMove(m, userPrompt)
		execs, err := d.Execute(runCtx, move)
		if err != nil {
			agentErr = err
			return err
		}
		execResult = execs

		// 3. Move 完成后：记录 outcome（produces 边在外层补）
		if moveID != "" {
			outcome := map[string]interface{}{
				"engine":     "solo",
				"final_text": "",
			}
			if len(execs) > 0 {
				outcome["final_text"] = execs[len(execs)-1].Result.Conclusion
			}
			if err := h.ledger.RecordMoveComplete(runCtx, moveID, outcome, nil); err != nil {
				h.logger.Warn().Err(err).Str("move_id", moveID).Msg("RecordMoveComplete 失败（不阻塞）")
			}
		}

		return nil
	}

	report, err := h.runCognition(runCtx, assignmentID, taskID, host, runAgent)
	if err == nil {
		err = agentErr
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			finalizeTask(false, "ctx "+err.Error())
			return h.abortTask(ctx, p.ExecutorID, "ctx "+err.Error())
		}
		finalizeTask(false, err.Error())
		return h.failTask(ctx, p.ExecutorID, err)
	}

	out, err := json.Marshal(buildRunResult(string(scen.Engine), execResult, report))
	if err != nil {
		finalizeTask(false, "marshal task result")
		return h.failTask(ctx, p.ExecutorID, fmt.Errorf("marshal task result: %w", err))
	}
	finalizeTask(true, "")
	h.distillCorpus(ctx, taskID, p.ConversationID, op.Code, host)
	return h.executors.SetDone(ctx, p.ExecutorID, out)
}

// ─────────────────────────────────────────────────────────────
//  Swarm handler
// ─────────────────────────────────────────────────────────────

// handleSwarm runs a multi-agent orchestration task via the dispatcher.
// The planner system prompt absorbs all sub-agent descriptions so the
// underlying actor can reason about delegation natively.
func (h handler) handleSwarm(
	ctx context.Context,
	p worker.Payload,
	scen cfgscenario.Scenario,
	executors []cfgagent.Agent,
	brief string,
) error {
	if brief == "" {
		return h.failTask(ctx, p.ExecutorID, fmt.Errorf("swarm 引擎缺 brief"))
	}
	if len(executors) == 0 {
		return h.failTask(ctx, p.ExecutorID, fmt.Errorf("swarm 引擎无 enabled 领域操作员"))
	}

	tid := p.ExecutorID
	taskID := p.TaskID
	if err := h.tasks.Heartbeat(ctx, taskID); err != nil {
		h.logger.Warn().Err(err).Str("task_id", taskID).Msg("task 入口心跳失败（不阻塞）")
	}

	var assignmentID string
	if tk, err := h.tasks.GetByID(ctx, taskID); err == nil {
		assignmentID = tk.AssignmentID
	}
	virtualHost := h.onboard(ctx, assignmentID, taskID, brief)

	orchAgent, err := h.cfgStore.Planner(ctx)
	if err != nil {
		return h.failTask(ctx, p.ExecutorID, fmt.Errorf("swarm 缺全局 planner 操作员: %w", err))
	}

	sysPrompt := swarmSystemPrompt(orchAgent.Body, scen.Instruction, executors)

	rt, _ := h.settings.Runtime(ctx)
	orchPrompt := executorbuilder.BuildUserPrompt(ctx, h.buildPromptDeps(), skill.BuilderParams{
		TaskID:        taskID,
		ExecutorID:    tid,
		Host:          virtualHost,
		Brief:         brief,
		CliTools:      orchAgent.CliTools,
		FindingsLimit: rt.FindingsLimitInPrompt,
	})
	if hist := h.conversationContext(ctx, p.ConversationID, "planner", brief); hist != "" {
		orchPrompt = hist + "\n" + orchPrompt
	}

	cleanup := func() {}
	var sink scanagent.EventSink
	if p.ConversationID != "" && h.conversations != nil && h.eventPublisher != nil {
		es := newEventSink(h.conversations, h.eventPublisher, p.ConversationID, h.logger)
		sink = es
		cleanup = es.Close
	}
	defer cleanup()

	// Sandbox 按 Assignment 粒度管理，多 Task 共享同一容器（引用计数）
	sandboxClient, err := h.sandboxMgr.Acquire(ctx, assignmentID)
	if err != nil {
		return h.failTask(ctx, p.ExecutorID, fmt.Errorf("sandboxMgr.Acquire(%s): %w", assignmentID, err))
	}
	// 任务结束时释放引用，引用计数归零后延迟清理容器
	defer func() {
		if err := h.sandboxMgr.Release(context.Background(), assignmentID); err != nil {
			h.logger.Warn().Err(err).Str("assignment_id", assignmentID).Msg("sandboxMgr.Release 失败")
		}
	}()

	d, reg, err := h.buildDispatcher(ctx, provider.ComplexityComplex, sysPrompt, sink)
	if err != nil {
		return h.failTask(ctx, p.ExecutorID, err)
	}
	tools.RegisterAll(reg, tools.Deps{
		TaskID:        taskID,
		ExecutorID:    p.ExecutorID,
		Host:          virtualHost,
		Findings:      h.findings,
		Corpus:        h.corpus,
		Embedder:      h.embedder,
		Reranker:      h.reranker,
		Leads:         h.leads,
		ProxyStore:    h.proxyStore,
		AgentStore:    h.agentStore,
		Creds:         h.creds,
		Sandbox:       sandboxClient,
		ToolingLoader: h.toolingLoader,
		VulnLoader:    h.vulnLoader,
	})

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
				Msg("task 终态写失败")
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go h.watchAbort(runCtx, cancel, taskID)

	var execResult []actor.Execution
	var agentErr error
	var moveID string
	runAgent := func(runCtx context.Context, m planner.Move) error {
		// 1. Move 开始前：记录到世界模型（spawns 边）
		if m.OnNodeID != "" && m.Kind != "" {
			mid, err := h.ledger.RecordMoveStart(runCtx, assignmentID, string(m.Kind), m.OnNodeID, m.Reason)
			if err != nil {
				h.logger.Warn().Err(err).Str("move_kind", string(m.Kind)).Msg("RecordMoveStart 失败（不阻塞）")
			} else {
				moveID = mid
			}
		}

		// 2. 执行 Move
		move := plannerMoveToActorMove(m, orchPrompt)
		execs, err := d.Execute(runCtx, move)
		if err != nil {
			agentErr = err
			return err
		}
		execResult = execs

		// 3. Move 完成后：记录 outcome（produces 边在外层补）
		if moveID != "" {
			outcome := map[string]interface{}{
				"engine":     "swarm",
				"final_text": "",
			}
			if len(execs) > 0 {
				outcome["final_text"] = execs[len(execs)-1].Result.Conclusion
			}
			if err := h.ledger.RecordMoveComplete(runCtx, moveID, outcome, nil); err != nil {
				h.logger.Warn().Err(err).Str("move_id", moveID).Msg("RecordMoveComplete 失败（不阻塞）")
			}

			// 发布 Move 完成事件，触发 Planner 重新规划
			if h.eventBus != nil {
				if moveUUID, parseErr := uuid.Parse(moveID); parseErr == nil {
					h.eventBus.PublishMoveCompleted(taskID, moveUUID)
				}
			}
		}

		return nil
	}

	report, err := h.runCognition(runCtx, assignmentID, taskID, virtualHost, runAgent)
	if err == nil {
		err = agentErr
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			finalizeTask(false, "ctx "+err.Error())
			return h.abortTask(ctx, p.ExecutorID, "ctx "+err.Error())
		}
		finalizeTask(false, err.Error())
		return h.failTask(ctx, p.ExecutorID, err)
	}

	out, err := json.Marshal(buildRunResult(string(scen.Engine), execResult, report))
	if err != nil {
		finalizeTask(false, "marshal task result")
		return h.failTask(ctx, p.ExecutorID, fmt.Errorf("marshal task result: %w", err))
	}
	finalizeTask(true, "")
	h.distillCorpus(ctx, taskID, p.ConversationID, "planner", virtualHost)
	return h.executors.SetDone(ctx, p.ExecutorID, out)
}

// ─────────────────────────────────────────────────────────────
//  Helpers
// ─────────────────────────────────────────────────────────────

// plannerMoveToActorMove translates a planner.Move to actor.Move.
// When the planner has no move (zero value), defaults to MoveKindEnumerate with the
// user prompt as the objective.
func plannerMoveToActorMove(m planner.Move, userPrompt string) actor.Move {
	kind := plannerKindToActorKind(m.Kind)
	objective := userPrompt
	if d := m.Directive(); d != "" {
		objective = d + "\n" + userPrompt
	} else if m.Reason != "" {
		objective = m.Reason + "\n" + userPrompt
	}
	return actor.Move{
		Kind: kind,
		Target: actor.LandmarkRef{
			Domain:  m.Target.Domain,
			RefKind: m.Target.RefKind,
			Locator: m.Target.Locator,
		},
		Objective: objective,
	}
}

// plannerKindToActorKind maps planner.MoveKind to actor.MoveKind.
func plannerKindToActorKind(k planner.MoveKind) actor.MoveKind {
	switch k {
	case planner.MoveEnumerate:
		return actor.MoveKindEnumerate
	case planner.MoveProbe:
		return actor.MoveKindProbe
	case planner.MoveExploit:
		return actor.MoveKindExploit
	case planner.MoveEscalate:
		return actor.MoveKindEscalate
	case planner.MovePersist:
		return actor.MoveKindPersist
	default:
		return actor.MoveKindEnumerate
	}
}

// buildRunResult marshals actor executions + cognition report into the task output envelope.
func buildRunResult(engine string, execs []actor.Execution, report interface{}) map[string]any {
	toolCalls := []string{}
	finalText := ""
	for _, ex := range execs {
		for _, s := range ex.Result.Steps {
			_ = s
		}
		finalText = ex.Result.Conclusion
	}
	return map[string]any{
		"engine":     engine,
		"tool_calls": toolCalls,
		"final_text": finalText,
		"cognition":  report,
	}
}
