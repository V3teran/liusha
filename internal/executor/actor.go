// Package executor — ReAct 执行引擎。
//
// 每 Step：CountTokens → 压缩判断 → Provider.Complete → ExecuteParallel
//   → checkpoint.Write → SSE 推送
package executor

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/rs/zerolog"
)

// Compactor 压缩过长的上下文历史。
type Compactor interface {
	Compact(ctx context.Context, p provider.Provider, messages []provider.Message) ([]provider.Message, error)
}

// CheckpointStore 持久化每步快照，用于崩溃恢复。
type CheckpointStore interface {
	Write(ctx context.Context, cp Checkpoint) error
	Last(ctx context.Context, taskID, actionID string) (*Checkpoint, error)
}

// Checkpoint 是单步快照。
type Checkpoint struct {
	TaskID     string
	ActionID   string
	StepIdx    int
	Thought    string
	Hypotheses []string
	CreatedAt  time.Time
}

// SSEEmitter 向前端推送流式事件。
type SSEEmitter interface {
	Emit(event SSEEvent)
}

// SSEEvent 是推给前端的一条事件。
type SSEEvent struct {
	Kind     string // "thinking" | "tool_start" | "tool_end" | "landmark" | "finding" | "action_done"
	ActionID string
	StepID   int
	Data     any
}

// ─────────────────────────────────────────────
//  Actor
// ─────────────────────────────────────────────

// Executor 是 ReAct 执行引擎。每个 Move 新建一个 Executor 实例。
type Executor struct {
	provider   provider.Provider
	reg        *registry.Registry
	compactor  Compactor
	checkpoint CheckpointStore
	emitter    SSEEmitter // 可为 nil
	logger     zerolog.Logger
	worldmodel WorldModelReader // 用于读取 metadata

	// 自我监察配置
	monitorEnabled       bool
	monitorStepInterval  int           // 每 N 步评估一次
	monitorEvaluateSteps int           // 评估最近 N 步
	monitorProvider      provider.Provider // 用于监察的 LLM

	// 事件总线（用于接收外部控制）
	eventBus EventBus
}

// WorldModelReader 是只读的 worldmodel 接口（用于解耦）。
type WorldModelReader interface {
	GetNode(ctx context.Context, id string) (*WorldModelNode, error)
}

// WorldModelNode 是 worldmodel 节点的简化表示。
type WorldModelNode struct {
	ID       string
	Metadata json.RawMessage
}

// EventBus 是事件总线接口（用于解耦）。
type EventBus interface {
	Subscribe(ctx context.Context, actionID string) EventSubscription
	Publish(event Event)
}

// EventSubscription 是订阅句柄接口。
type EventSubscription interface {
	Events() <-chan Event
	Unsubscribe()
}

// Event 是事件载体。
type Event struct {
	Type      string
	ActionID  string
	Payload   map[string]interface{}
	Timestamp time.Time
}

// New 构造 Actor。emitter 和 worldmodel 可为 nil。
func New(
	p provider.Provider,
	reg *registry.Registry,
	compactor Compactor,
	cp CheckpointStore,
	emitter SSEEmitter,
	logger zerolog.Logger,
	worldmodel WorldModelReader,
) *Executor {
	return &Executor{
		provider:   p,
		reg:        reg,
		compactor:  compactor,
		checkpoint: cp,
		emitter:    emitter,
		logger:     logger,
		worldmodel: worldmodel,

		// 默认启用监察，每 5 步评估一次，评估最近 5 步
		monitorEnabled:       true,
		monitorStepInterval:  5,
		monitorEvaluateSteps: 5,
		monitorProvider:      p, // 默认用同一个 provider
		eventBus:             nil, // 默认无事件总线
	}
}

// WithEventBus 配置事件总线。
func (a *Executor) WithEventBus(bus EventBus) *Executor {
	a.eventBus = bus
	return a
}

// WithMonitor 配置自我监察。
func (a *Executor) WithMonitor(enabled bool, stepInterval int, evaluateSteps int, provider provider.Provider) *Executor {
	a.monitorEnabled = enabled
	a.monitorStepInterval = stepInterval
	a.monitorEvaluateSteps = evaluateSteps
	if provider != nil {
		a.monitorProvider = provider
	}
	return a
}

// Run 执行 ReAct 循环，直到 done/budget/error/cancel。
// 如果启用监察，将启动两个协程：执行协程和监察协程。
func (a *Executor) Run(ctx context.Context, actionID string, req ExecutorReq) (ExecutorResult, error) {
	if req.Budget.MaxSteps <= 0 {
		req.Budget = DefaultBudget()
	}
	if req.Budget.CompactionTrigger <= 0 {
		req.Budget.CompactionTrigger = 0.70
	}

	// 如果未启用监察，使用原有单协程逻辑
	if !a.monitorEnabled {
		return a.runSingleThreaded(ctx, actionID, req)
	}

	// 双协程模式
	return a.runWithMonitoring(ctx, actionID, req)
}

// runSingleThreaded 是原有的单协程执行逻辑（未启用监察时使用）。
func (a *Executor) runSingleThreaded(ctx context.Context, actionID string, req ExecutorReq) (ExecutorResult, error) {
	// 从 checkpoint 恢复起点
	startStep := 0
	if a.checkpoint != nil {
		if cp, err := a.checkpoint.Last(ctx, extractTaskID(ctx), actionID); err == nil && cp != nil {
			startStep = cp.StepIdx + 1
		}
	}

	// 构建初始消息列表
	messages := buildInitialMessages(req)

	var steps []Step
	totalTokens := 0

	// 激活 Constraint 检查
	if len(req.PendingConstraints) > 0 {
		ctx = registry.WithConstraints(ctx, req.PendingConstraints)
	}

	for stepIdx := startStep; stepIdx < req.Budget.MaxSteps; stepIdx++ {
		// 1. 计算当前 token 数，必要时压缩
		if a.compactor != nil && a.provider != nil {
			tokenCount, err := a.provider.CountTokens(ctx, provider.Request{Messages: messages})
			if err == nil && req.Budget.MaxTokens > 0 {
				ratio := float64(tokenCount) / float64(req.Budget.MaxTokens)
				if ratio > req.Budget.CompactionTrigger {
					compacted, cerr := a.compactor.Compact(ctx, a.provider, messages)
					if cerr == nil {
						messages = compacted
					}
				}
			}
		}

		// SettleConfig：budget 接近上限时注入结算指令
		if req.Budget.MaxTokens > 0 && req.Settle.Threshold > 0 {
			tc, _ := a.provider.CountTokens(ctx, provider.Request{Messages: messages})
			if float64(tc)/float64(req.Budget.MaxTokens) > req.Settle.Threshold {
				messages = append(messages, provider.Message{
					Role:    "user",
					Content: req.Settle.Directive,
				})
			}
		}

		// 2. 调用 Provider
		tools := a.reg.Schemas()
		resp, err := a.provider.Complete(ctx, provider.Request{
			Messages:  messages,
			Tools:     tools,
			MaxTokens: req.Budget.MaxTokens,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return ExecutorResult{Steps: steps, Halt: HaltCancelled, TokensUsed: totalTokens}, nil
			}
			return ExecutorResult{Steps: steps, Halt: HaltError, TokensUsed: totalTokens}, err
		}
		totalTokens += resp.Usage.InTokens + resp.Usage.OutTokens

		// 3. 解析响应，构建 Step
		step := Step{Index: stepIdx, Thought: resp.Content}

		if a.emitter != nil && resp.Content != "" {
			a.emitter.Emit(SSEEvent{Kind: "thinking", ActionID: actionID, StepID: stepIdx, Data: resp.Content})
		}

		// 将 assistant 回复加入历史
		assistantMsg := provider.Message{Role: "assistant", Content: resp.Content}
		if len(resp.ToolCalls) > 0 {
			assistantMsg.ToolCalls = resp.ToolCalls
		}
		messages = append(messages, assistantMsg)

		// 4. 执行工具调用
		if len(resp.ToolCalls) > 0 {
			if a.emitter != nil {
				for _, tc := range resp.ToolCalls {
					a.emitter.Emit(SSEEvent{Kind: "tool_start", ActionID: actionID, StepID: stepIdx, Data: tc.Name})
				}
			}

			results := a.reg.ExecuteParallel(ctx, resp.ToolCalls)

			// 把工具结果追加为 tool messages
			for i, tc := range resp.ToolCalls {
				r := results[i]
				content := r.Output
				if r.Error != "" {
					content = "ERROR: " + r.Error
				}
				messages = append(messages, provider.Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    content,
				})

				if a.emitter != nil {
					a.emitter.Emit(SSEEvent{Kind: "tool_end", ActionID: actionID, StepID: stepIdx, Data: r})
				}
			}

			// 检查 done 工具
			for _, tc := range resp.ToolCalls {
				if tc.Name == "done" {
					step.Thought += "\n[concluded]"
					steps = append(steps, step)
					saveCheckpoint(ctx, a.checkpoint, actionID, stepIdx, step)
					return ExecutorResult{Steps: steps, Conclusion: resp.Content, Halt: HaltDone, TokensUsed: totalTokens}, nil
				}
			}
		}

		steps = append(steps, step)
		saveCheckpoint(ctx, a.checkpoint, actionID, stepIdx, step)

		// done 判断（无工具调用 + FinishReason=stop）
		if len(resp.ToolCalls) == 0 && resp.FinishReason == "stop" {
			return ExecutorResult{Steps: steps, Conclusion: resp.Content, Halt: HaltDone, TokensUsed: totalTokens}, nil
		}

		// budget 检查
		if req.Budget.MaxTokens > 0 && totalTokens >= req.Budget.MaxTokens {
			return ExecutorResult{Steps: steps, Halt: HaltBudget, TokensUsed: totalTokens}, nil
		}
	}

	return ExecutorResult{Steps: steps, Halt: HaltBudget, TokensUsed: totalTokens}, nil
}

// ─────────────────────────────────────────────
//  辅助函数
// ─────────────────────────────────────────────

func buildInitialMessages(req ExecutorReq) []provider.Message {
	msgs := make([]provider.Message, 0, 1+len(req.Inbox))
	if req.System != "" {
		msgs = append(msgs, provider.Message{Role: "system", Content: req.System})
	}
	for _, m := range req.Inbox {
		msgs = append(msgs, provider.Message{Role: provider.Role(m.Role), Content: m.Content})
	}
	return msgs
}

type taskIDKey struct{}

// WithTaskID 向 ctx 注入 taskID，供 checkpoint 使用。
func WithTaskID(ctx context.Context, taskID string) context.Context {
	return context.WithValue(ctx, taskIDKey{}, taskID)
}

func extractTaskID(ctx context.Context) string {
	if v, ok := ctx.Value(taskIDKey{}).(string); ok {
		return v
	}
	return ""
}

func saveCheckpoint(ctx context.Context, store CheckpointStore, actionID string, stepIdx int, step Step) {
	if store == nil {
		return
	}
	_ = store.Write(ctx, Checkpoint{
		TaskID:     extractTaskID(ctx),
		ActionID:   actionID,
		StepIdx:    stepIdx,
		Thought:    step.Thought,
		Hypotheses: step.Hypotheses,
		CreatedAt:  time.Now(),
	})
}
