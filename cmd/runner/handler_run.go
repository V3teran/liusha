package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/V3teran/liusha/internal/actor"
	executorbuilder "github.com/V3teran/liusha/internal/builder/executor"
	cfgagent "github.com/V3teran/liusha/internal/config/agent"
	cfgscenario "github.com/V3teran/liusha/internal/config/scenario"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/dispatcher"
	dispatcherprofile "github.com/V3teran/liusha/internal/dispatcher/profile"
	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/scanagent"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/tools"
	"github.com/V3teran/liusha/internal/worker"
	"github.com/V3teran/liusha/internal/worldmodel"
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

	// 注册全部 5 个 Complexity Profile
	for _, profile := range dispatcherprofile.Profiles(systemPrompt) {
		d.RegisterProfile(profile)
	}

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
		AssignmentID:  assignmentID,
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
		Tasks:         h.tasks,
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
	runAgent := func(runCtx context.Context, m worldmodel.Node) error {
		// 执行 Move
		move := nodeToActorMove(m, userPrompt)
		execs, err := d.Execute(runCtx, move)
		if err != nil {
			agentErr = err
			return err
		}
		execResult = execs
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

	// 保存 assistant 的最终回复到 conversation
	if p.ConversationID != "" && h.conversations != nil {
		var result map[string]any
		if err := json.Unmarshal(out, &result); err == nil {
			if finalText, ok := result["final_text"].(string); ok && finalText != "" {
				if _, err := h.conversations.AppendMessage(ctx, p.ConversationID, conversation.RoleAssistant, conversation.KindMessage, finalText, nil); err != nil {
					h.logger.Warn().Err(err).Str("conversation_id", p.ConversationID).Msg("保存 assistant 消息失败")
				}
			}
		}
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
		AssignmentID:  assignmentID,
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
		Tasks:         h.tasks,
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
	runAgent := func(runCtx context.Context, m worldmodel.Node) error {
		// 执行 Move
		move := nodeToActorMove(m, orchPrompt)
		execs, err := d.Execute(runCtx, move)
		if err != nil {
			agentErr = err
			return err
		}
		execResult = execs
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

	// 保存 assistant 的最终回复到 conversation
	if p.ConversationID != "" && h.conversations != nil {
		var result map[string]any
		if err := json.Unmarshal(out, &result); err == nil {
			if finalText, ok := result["final_text"].(string); ok && finalText != "" {
				if _, err := h.conversations.AppendMessage(ctx, p.ConversationID, conversation.RoleAssistant, conversation.KindMessage, finalText, nil); err != nil {
					h.logger.Warn().Err(err).Str("conversation_id", p.ConversationID).Msg("保存 assistant 消息失败")
				}
			}
		}
	}

	finalizeTask(true, "")
	h.distillCorpus(ctx, taskID, p.ConversationID, "planner", virtualHost)
	return h.executors.SetDone(ctx, p.ExecutorID, out)
}

// ─────────────────────────────────────────────────────────────
//  Helpers
// ─────────────────────────────────────────────────────────────

// nodeToActorMove translates a worldmodel.Node (kind=move) to actor.Move.
// Extracts complexity and instruction from the move node.
func nodeToActorMove(node worldmodel.Node, userPrompt string) actor.Move {
	if !node.IsMove() {
		// Fallback for non-move nodes
		return actor.Move{
			Complexity:  actor.ComplexitySimple,
			Instruction: userPrompt,
		}
	}

	// 解析 content
	var content map[string]interface{}
	_ = json.Unmarshal(node.Content, &content)

	instruction, _ := content["instruction"].(string)
	if instruction == "" {
		instruction = userPrompt
	}

	// 解析 target_ref（可选）
	var targetRef worldmodel.TargetRef
	if tr, ok := content["target_ref"].(map[string]interface{}); ok {
		domain, _ := tr["domain"].(string)
		refKind, _ := tr["ref_kind"].(string)
		locator, _ := tr["locator"].(string)
		targetRef = worldmodel.TargetRef{
			Domain:  domain,
			RefKind: refKind,
			Locator: locator,
		}
	}

	// 使用节点的 complexity，如果为空则默认 simple
	complexity := actor.ComplexitySimple
	if node.Complexity != nil {
		complexity = actor.Complexity(*node.Complexity)
	}

	return actor.Move{
		Complexity: complexity,
		Target: actor.LandmarkRef{
			Domain:  targetRef.Domain,
			RefKind: targetRef.RefKind,
			Locator: targetRef.Locator,
		},
		Instruction: instruction,
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
