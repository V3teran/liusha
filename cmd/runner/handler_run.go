package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/V3teran/liusha/internal/bus"
	executorbuilder "github.com/V3teran/liusha/internal/builder/executor"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/scanagent"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/toolinvocation"
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
		h.logger.Info().
			Str("task_id", taskID).
			Str("tool_name", t.Name()).
			Msg("[INTERCEPTOR] Tool call intercepted")

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
				h.logger.Error().Err(appendErr).
					Str("task_id", taskID).
					Str("tool_name", t.Name()).
					Msg("[INTERCEPTOR] Failed to record tool invocation")
			} else {
				h.logger.Info().
					Str("task_id", taskID).
					Str("tool_name", t.Name()).
					Int64("invocation_id", invID).
					Msg("[INTERCEPTOR] Tool invocation recorded successfully")
			}
		} else {
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

func (noopCompactor) Compact(_ context.Context, _ llm.Provider, msgs []llm.Message) ([]llm.Message, error) {
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

// composeplannerInstruction builds the full system prompt for the planner agent.
func composeplannerInstruction(body string) string {
	return executorbuilder.SystemPrompt() + "\n\n" + body
}

// composeSubAgentInstruction builds the full system prompt for a sub-agent.
func composeSubAgentInstruction(body string) string {
	return executorbuilder.SystemPrompt() + "\n\n" + body
}

// ─────────────────────────────────────────────────────────────
//  Dispatcher factory
// ─────────────────────────────────────────────────────────────

// buildDispatcher constructs a Dispatcher for a single run.
// The caller is responsible for registering tools into the returned registry.
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
func (h handler) inferComplexity(brief string) llm.Complexity {
	lower := strings.ToLower(brief)

	// Trivial: 查询类（如果 provider 包没有定义，使用 Simple）
	if strings.Contains(lower, "列举") || strings.Contains(lower, "查询") ||
		strings.Contains(lower, "检查") || strings.Contains(lower, "读取") {
		return llm.ComplexitySimple
	}

	// Simple: 基础枚举
	if strings.Contains(lower, "扫描") || strings.Contains(lower, "探测") ||
		strings.Contains(lower, "枚举") || strings.Contains(lower, "发现") {
		return llm.ComplexitySimple
	}

	// Complex: 漏洞利用
	if strings.Contains(lower, "利用") || strings.Contains(lower, "getshell") ||
		strings.Contains(lower, "提权") || strings.Contains(lower, "执行") ||
		strings.Contains(lower, "绕过") {
		return llm.ComplexityComplex
	}

	// Extreme: 复杂攻击链（使用 Complex 作为最高级）
	if strings.Contains(lower, "横移") || strings.Contains(lower, "持久化") ||
		strings.Contains(lower, "攻击链") || strings.Contains(lower, "域控") {
		return llm.ComplexityComplex
	}

	// Moderate: 默认（漏洞测试）
	return llm.ComplexityMedium
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
		return h.failTask(ctx, p.AgentID, fmt.Errorf("任务缺少 brief"))
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
	_, err := h.sandboxMgr.Acquire(ctx, assignmentID)
	if err != nil {
		return h.failTask(ctx, p.AgentID, fmt.Errorf("sandboxMgr.Acquire(%s): %w", assignmentID, err))
	}
	defer func() {
		if err := h.sandboxMgr.Release(context.Background(), assignmentID); err != nil {
			h.logger.Warn().Err(err).Str("assignment_id", assignmentID).Msg("sandboxMgr.Release 失败")
		}
	}()

	// 执行四Agent认知循环
	report, err := h.runCognition(ctx, assignmentID, taskID, virtualHost)
	if err != nil {
		return h.failTask(ctx, p.AgentID, err)
	}

	h.logger.Info().
		Str("task_id", taskID).
		Int("steps", report.Steps).
		Int("promoted", report.Promoted).
		Str("stop_why", report.StopWhy).
		Msg("认知循环完成")

	// 任务完成
	if err := h.tasks.Complete(ctx, taskID); err != nil {
		h.logger.Error().Err(err).Str("task_id", taskID).Msg("failed to mark task complete")
	}
	return nil
}

// ─────────────────────────────────────────────────────────────
//  Helper functions
// ─────────────────────────────────────────────────────────────

// Extracts complexity and instruction from the action node.
func nodeToExecutorAction(node explorationgraph.Node, userPrompt string) executor.Action {
	if !node.IsAction() {
		// Fallback for non-action nodes
		return executor.Action{
			Complexity:  explorationgraph.ComplexitySimple,
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
	var targetRef explorationgraph.TargetRef
	if tr, ok := content["target_ref"].(map[string]interface{}); ok {
		domain, _ := tr["domain"].(string)
		refKind, _ := tr["ref_kind"].(string)
		locator, _ := tr["locator"].(string)
		targetRef = explorationgraph.TargetRef{
			Domain:  domain,
			RefKind: refKind,
			Locator: locator,
		}
	}

	// 使用节点的 complexity，如果为空则默认 simple
	complexity := explorationgraph.ComplexitySimple
	if node.Complexity != nil {
		complexity = explorationgraph.Complexity(*node.Complexity)
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

// ─────────────────────────────────────────────────────────────
//  EventBus 适配器
// ─────────────────────────────────────────────────────────────

// eventBusAdapter 将 bus.Bus 适配为 executor.EventBus 接口
type eventBusAdapter struct {
	bus bus.Bus
}

func newEventBusAdapter(b bus.Bus) *eventBusAdapter {
	return &eventBusAdapter{bus: b}
}

func (a *eventBusAdapter) Subscribe(ctx context.Context, actionID string) executor.EventSubscription {
	// bus.Bus.SubscribeAction 返回 *bus.Subscription
	sub := a.bus.SubscribeAction(ctx, actionID)
	return &eventSubscriptionAdapter{sub: sub}
}

func (a *eventBusAdapter) Publish(event executor.ControlEvent) {
	// 转换 executor.ControlEvent 到 bus.Event
	a.bus.Publish(bus.Event{
		Type:      bus.EventType(event.Type),
		ActionID:  event.ActionID,
		Payload:   event.Payload,
		Timestamp: event.Timestamp,
	})
}

// eventSubscriptionAdapter 实现 executor.EventSubscription
type eventSubscriptionAdapter struct {
	sub *bus.Subscription
}

func (s *eventSubscriptionAdapter) Events() <-chan executor.ControlEvent {
	// 从 bus.Subscription 获取事件 channel
	eventbusCh := s.sub.Events()

	// 创建转换 channel
	out := make(chan executor.ControlEvent, 10)
	go func() {
		defer close(out)
		for e := range eventbusCh {
			out <- executor.ControlEvent{
				Type:      string(e.Type),
				ActionID:  e.ActionID,
				Payload:   e.Payload,
				Timestamp: e.Timestamp,
			}
		}
	}()
	return out
}

func (s *eventSubscriptionAdapter) Unsubscribe() {
	s.sub.Unsubscribe()
}
