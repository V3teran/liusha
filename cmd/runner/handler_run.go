package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/V3teran/liusha/internal/executor"
	executorbuilder "github.com/V3teran/liusha/internal/builder/executor"
	cfgagent "github.com/V3teran/liusha/internal/config/agent"
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
		fmt.Printf("[INTERCEPTOR] Tool call intercepted: %s (task=%s)\n", t.Name(), taskID)
		h.logger.Info().
			Str("task_id", taskID).
			Str("tool_name", t.Name()).
			Msg("[INTERCEPTOR] Tool call intercepted")

		start := time.Now()
		res, err := next(ctx, t, args)
		durMs := int(time.Since(start).Milliseconds())

		fmt.Printf("[INTERCEPTOR] Tool call completed: %s (duration=%dms)\n", t.Name(), durMs)

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
			invID, appendErr := h.toolCalls.Append(ctx, toolinvocation.Invocation{
				ExecutorID:    executorID,
				TaskID:        taskID,
				ToolName:      t.Name(),
				Args:          json.RawMessage(args),
				OutputSize:    len(res.Output),
				OutputPreview: preview,
				DurationMs:    durMs,
				ErrorMessage:  errMsg,
			})
			if appendErr != nil {
				fmt.Printf("[INTERCEPTOR] Failed to record: %v\n", appendErr)
				h.logger.Error().Err(appendErr).
					Str("task_id", taskID).
					Str("tool_name", t.Name()).
					Msg("[INTERCEPTOR] Failed to record tool invocation")
			} else {
				fmt.Printf("[INTERCEPTOR] Recorded successfully: id=%d\n", invID)
				h.logger.Info().
					Str("task_id", taskID).
					Str("tool_name", t.Name()).
					Int64("invocation_id", invID).
					Msg("[INTERCEPTOR] Tool invocation recorded successfully")
			}
		} else {
			fmt.Printf("[INTERCEPTOR] h.toolCalls is nil!\n")
			h.logger.Warn().
				Str("task_id", taskID).
				Str("tool_name", t.Name()).
				Msg("[INTERCEPTOR] h.toolCalls is nil, cannot record invocation")
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
//  No-op executor infrastructure stubs
// ─────────────────────────────────────────────────────────────

type noopCompactor struct{}

func (noopCompactor) Compact(_ context.Context, _ provider.Provider, msgs []provider.Message) ([]provider.Message, error) {
	return msgs, nil
}

// ─────────────────────────────────────────────────────────────
//  SSE emitter bridging executor.SSEEvent → scanagent.ScanEvent
// ─────────────────────────────────────────────────────────────

type sseEmitterAdapter struct{ sink scanagent.EventSink }

func (a *sseEmitterAdapter) Emit(ev executor.SSEEvent) {
	if a.sink == nil {
		return
	}
	a.sink.OnScanEvent(context.Background(), scanagent.ScanEvent{
		Kind: scanagent.ScanEventKind(ev.Kind),
		Text: fmt.Sprintf("action=%s step=%d", ev.ActionID, ev.StepID),
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
// shared base + optional  instruction + executor body.
func composeSoloInstruction(instruction string, op cfgagent.Agent) string {
	var b strings.Builder
	b.WriteString(executorbuilder.SystemPrompt())
	if instruction != "" {
		b.WriteString("\n\n")
		b.WriteString(instruction)
	}
	if op.SystemPrompt != "" {
		b.WriteString("\n\n")
		b.WriteString(op.SystemPrompt)
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

	var emitter executor.SSEEmitter
	if sink != nil {
		emitter = &sseEmitterAdapter{sink: sink}
	}

	// 使用 Action 级别的 EventBus（通过适配器）
	actionEventBus := executor.NewEventBusAdapter(h.actionBus)
	d := dispatcher.New(p, reg, noopCompactor{}, h.checkpoint, emitter, h.logger, h.world, actionEventBus)

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
				h.logger.Info().Str("task_id", taskID).Msg("task 中止，cancel executor run")
				cancel()
				return
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────
//  Complexity 推断
// ─────────────────────────────────────────────────────────────

// inferComplexity 根据 brief 内容推断初始 complexity
func (h handler) inferComplexity(brief string) provider.Complexity {
	lower := strings.ToLower(brief)

	// Trivial: 查询类（如果 provider 包没有定义，使用 Simple）
	if strings.Contains(lower, "列举") || strings.Contains(lower, "查询") ||
		strings.Contains(lower, "检查") || strings.Contains(lower, "读取") {
		return provider.ComplexitySimple
	}

	// Simple: 基础枚举
	if strings.Contains(lower, "扫描") || strings.Contains(lower, "探测") ||
		strings.Contains(lower, "枚举") || strings.Contains(lower, "发现") {
		return provider.ComplexitySimple
	}

	// Complex: 漏洞利用
	if strings.Contains(lower, "利用") || strings.Contains(lower, "getshell") ||
		strings.Contains(lower, "提权") || strings.Contains(lower, "执行") ||
		strings.Contains(lower, "绕过") {
		return provider.ComplexityComplex
	}

	// Extreme: 复杂攻击链（使用 Complex 作为最高级）
	if strings.Contains(lower, "横移") || strings.Contains(lower, "持久化") ||
		strings.Contains(lower, "攻击链") || strings.Contains(lower, "域控") {
		return provider.ComplexityComplex
	}

	// Moderate: 默认（漏洞测试）
	return provider.ComplexityMedium
}

// ─────────────────────────────────────────────────────────────
//  Solo handler
// ─────────────────────────────────────────────────────────────

// handleSolo runs a single-agent task via the dispatcher.
func (h handler) handleSolo(
	ctx context.Context,
	p worker.Payload,
	instruction string,
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

	sysPrompt := composeSoloInstruction(instruction, op)
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

	// 动态推断 complexity（而非固定 Medium）
	defaultComplexity := h.inferComplexity(brief)

	d, reg, err := h.buildDispatcher(ctx, defaultComplexity, sysPrompt, sink)
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

	var execResult []executor.Execution
	var agentErr error
	runAgent := func(runCtx context.Context, m worldmodel.Node) error {
		h.logger.Info().
			Str("action_id", m.ID).
			Str("kind", string(m.Kind)).
			Str("location", "handler_run.go:Solo").
			Msg("[RUN_AGENT] runAgent called")

		// 执行 Move
		action := nodeToExecutorAction(m, userPrompt)

		h.logger.Info().
			Str("action_id", action.ID).
			Msg("[RUN_AGENT] calling d.Execute")

		exec, err := d.Execute(runCtx, action)

		h.logger.Info().
			Str("action_id", action.ID).
			Bool("success", err == nil).
			Msg("[RUN_AGENT] d.Execute returned")

		if err != nil {
			agentErr = err
			return err
		}
		execResult = []executor.Execution{exec}
		return nil
	}

	h.logger.Info().
		Str("task_id", taskID).
		Str("closure_ptr", fmt.Sprintf("%p", runAgent)).
		Msg("[HANDLER] Created runAgent closure for Solo mode, calling runCognition")

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

	out, err := json.Marshal(buildRunResult("solo", execResult, report))
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
//  Cognition handler (新架构统一入口)
// ─────────────────────────────────────────────────────────────

// handleCognition 统一的任务执行入口（新架构）。
// 所有任务都走：Planner（6分钟评估） + Executor（5步评估）。
func (h handler) handleCognition(
	ctx context.Context,
	p worker.Payload,
	brief string,
) error {
	if brief == "" {
		return h.failTask(ctx, p.ExecutorID, fmt.Errorf("任务缺少 brief"))
	}

	taskID := p.TaskID
	if err := h.tasks.Heartbeat(ctx, taskID); err != nil {
		h.logger.Warn().Err(err).Str("task_id", taskID).Msg("task入口心跳失败")
	}

	var assignmentID string
	if tk, err := h.tasks.GetByID(ctx, taskID); err == nil {
		assignmentID = tk.AssignmentID
	}
	virtualHost := h.onboard(ctx, assignmentID, taskID, brief)

	// Sandbox 按 Assignment 粒度管理
	sandboxClient, err := h.sandboxMgr.Acquire(ctx, assignmentID)
	if err != nil {
		return h.failTask(ctx, p.ExecutorID, fmt.Errorf("sandboxMgr.Acquire(%s): %w", assignmentID, err))
	}
	defer func() {
		if err := h.sandboxMgr.Release(context.Background(), assignmentID); err != nil {
			h.logger.Warn().Err(err).Str("assignment_id", assignmentID).Msg("sandboxMgr.Release 失败")
		}
	}()

	// 获取Planner和Executor配置
	planner, err := h.cfgStore.GetPlanner(ctx)
	if err != nil {
		return h.failTask(ctx, p.ExecutorID, fmt.Errorf("获取planner失败: %w", err))
	}

	executorAgent, err := h.cfgStore.GetExecutor(ctx)
	if err != nil {
		return h.failTask(ctx, p.ExecutorID, fmt.Errorf("获取executor失败: %w", err))
	}

	// 构建system prompt
	sysPrompt := composeplannerInstruction(planner.SystemPrompt)

	// 动态推断 complexity
	defaultComplexity := h.inferComplexity(brief)

	cleanup := func() {}
	var sink scanagent.EventSink
	if p.ConversationID != "" && h.conversations != nil && h.eventPublisher != nil {
		es := newEventSink(h.conversations, h.eventPublisher, p.ConversationID, h.logger)
		sink = es
		cleanup = es.Close
	}
	defer cleanup()

	d, reg, err := h.buildDispatcher(ctx, defaultComplexity, sysPrompt, sink)
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

	var execResult []executor.Execution
	var agentErr error

	rt, _ := h.settings.Runtime(ctx)
	orchPrompt := executorbuilder.BuildUserPrompt(ctx, h.buildPromptDeps(), skill.BuilderParams{
		TaskID:        taskID,
		AssignmentID:  assignmentID,
		ExecutorID:    p.ExecutorID,
		Host:          virtualHost,
		Brief:         brief,
		CliTools:      executorAgent.CliTools,
		FindingsLimit: rt.FindingsLimitInPrompt,
	})
	if hist := h.conversationContext(ctx, p.ConversationID, "planner", brief); hist != "" {
		orchPrompt = hist + "\n" + orchPrompt
	}

	runAgent := func(runCtx context.Context, m worldmodel.Node) error {
		h.logger.Info().
			Str("action_id", m.ID).
			Str("kind", string(m.Kind)).
			Str("location", "handler_run.go:Cognition").
			Msg("[RUN_AGENT_2] runAgent called (cognition mode)")

		action := nodeToExecutorAction(m, orchPrompt)

		h.logger.Info().
			Str("action_id", action.ID).
			Msg("[RUN_AGENT_2] calling d.Execute")

		exec, err := d.Execute(runCtx, action)

		h.logger.Info().
			Str("action_id", action.ID).
			Bool("success", err == nil).
			Msg("[RUN_AGENT_2] d.Execute returned")

		if err != nil {
			agentErr = err
			return err
		}
		execResult = []executor.Execution{exec}
		return nil
	}

	h.logger.Info().
		Str("task_id", taskID).
		Str("closure_ptr", fmt.Sprintf("%p", runAgent)).
		Msg("[HANDLER] Created runAgent closure for Cognition mode, calling runCognition")

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

	out, err := json.Marshal(buildRunResult("cognition", execResult, report))
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
//  Helper functions
// ─────────────────────────────────────────────────────────────

// Extracts complexity and instruction from the action node.
func nodeToExecutorAction(node worldmodel.Node, userPrompt string) executor.Action {
	if !node.IsAction() {
		// Fallback for non-action nodes
		return executor.Action{
			Complexity:  worldmodel.ComplexitySimple,
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
	complexity := worldmodel.ComplexitySimple
	if node.Complexity != nil {
		complexity = worldmodel.Complexity(*node.Complexity)
	}

	return executor.Action{
		Complexity: complexity,
		Target: executor.TargetRef{
			Domain:  targetRef.Domain,
			RefKind: targetRef.RefKind,
			Locator: targetRef.Locator,
		},
		Instruction: instruction,
	}
}

// buildRunResult marshals executor executions + cognition report into the task output envelope.
func buildRunResult(engine string, execs []executor.Execution, report interface{}) map[string]any {
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
