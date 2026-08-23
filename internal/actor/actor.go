// Package actor — ReAct 执行引擎。
//
// 每 Step：CountTokens → 压缩判断 → Provider.Complete → ExecuteParallel
//   → checkpoint.Write → SSE 推送 → 每 CriticInterval 步 Critic.Evaluate
package actor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/registry"
)

// Compactor 压缩过长的上下文历史。
type Compactor interface {
	Compact(ctx context.Context, p provider.Provider, messages []provider.Message) ([]provider.Message, error)
}

// CheckpointStore 持久化每步快照，用于崩溃恢复。
type CheckpointStore interface {
	Write(ctx context.Context, cp Checkpoint) error
	Last(ctx context.Context, taskID, moveID string) (*Checkpoint, error)
}

// Checkpoint 是单步快照。
type Checkpoint struct {
	TaskID     string
	MoveID     string
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
	Kind   string // "thinking" | "tool_start" | "tool_end" | "landmark" | "finding" | "move_done"
	MoveID string
	StepID int
	Data   any
}

// LLMCritic 是基于 LLM Provider 的 Critic 实现。
type LLMCritic struct {
	provider provider.Provider
}

func NewLLMCritic(p provider.Provider) *LLMCritic {
	return &LLMCritic{provider: p}
}

func (c *LLMCritic) Evaluate(ctx context.Context, move Move, recent []Step) (Assessment, error) {
	if len(recent) == 0 {
		return Assessment{Advancing: true, Verdict: VerdictContinue}, nil
	}
	// 构造简短评估提示
	var thoughts string
	for _, s := range recent {
		if s.Thought != "" {
			thoughts += s.Thought + "\n"
		}
	}
	req := provider.Request{
		Messages: []provider.Message{
			{
				Role:    "user",
				Content: fmt.Sprintf("Move objective: %s\n\nRecent thoughts:\n%s\n\nIs this making progress? Reply JSON: {\"advancing\":bool,\"observation\":\"...\",\"verdict\":\"continue|steer|abandon\"}", move.Objective, thoughts),
			},
		},
		MaxTokens: 256,
	}
	resp, err := c.provider.Complete(ctx, req)
	if err != nil {
		// critic 失败不中断执行，继续
		return Assessment{Advancing: true, Verdict: VerdictContinue}, nil
	}
	_ = resp
	// 简单启发式：默认继续（完整解析留具体实现扩展）
	return Assessment{Advancing: true, Observation: resp.Content, Verdict: VerdictContinue}, nil
}

// ─────────────────────────────────────────────
//  Actor
// ─────────────────────────────────────────────

// Actor 是 ReAct 执行引擎。每个 Move 新建一个 Actor 实例。
type Actor struct {
	provider   provider.Provider
	reg        *registry.Registry
	compactor  Compactor
	critic     *LLMCritic
	checkpoint CheckpointStore
	emitter    SSEEmitter // 可为 nil
}

// New 构造 Actor。emitter 可为 nil（无 SSE 推送）。
func New(
	p provider.Provider,
	reg *registry.Registry,
	compactor Compactor,
	critic *LLMCritic,
	cp CheckpointStore,
	emitter SSEEmitter,
) *Actor {
	return &Actor{
		provider:   p,
		reg:        reg,
		compactor:  compactor,
		critic:     critic,
		checkpoint: cp,
		emitter:    emitter,
	}
}

// Run 执行 ReAct 循环，直到 done/budget/error/cancel。
func (a *Actor) Run(ctx context.Context, moveID string, req ActorReq) (ActorResult, error) {
	if req.Budget.MaxSteps <= 0 {
		req.Budget = DefaultBudget()
	}
	if req.Budget.CriticInterval <= 0 {
		req.Budget.CriticInterval = 5
	}
	if req.Budget.CompactionTrigger <= 0 {
		req.Budget.CompactionTrigger = 0.70
	}

	// 从 checkpoint 恢复起点
	startStep := 0
	if a.checkpoint != nil {
		if cp, err := a.checkpoint.Last(ctx, extractTaskID(ctx), moveID); err == nil && cp != nil {
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
				return ActorResult{Steps: steps, Halt: HaltCancelled, TokensUsed: totalTokens}, nil
			}
			return ActorResult{Steps: steps, Halt: HaltError, TokensUsed: totalTokens}, err
		}
		totalTokens += resp.Usage.InTokens + resp.Usage.OutTokens

		// 3. 解析响应，构建 Step
		step := Step{Index: stepIdx, Thought: resp.Content}

		if a.emitter != nil && resp.Content != "" {
			a.emitter.Emit(SSEEvent{Kind: "thinking", MoveID: moveID, StepID: stepIdx, Data: resp.Content})
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
					a.emitter.Emit(SSEEvent{Kind: "tool_start", MoveID: moveID, StepID: stepIdx, Data: tc.Name})
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
					a.emitter.Emit(SSEEvent{Kind: "tool_end", MoveID: moveID, StepID: stepIdx, Data: r})
				}
			}

			// 检查 done 工具
			for _, tc := range resp.ToolCalls {
				if tc.Name == "done" {
					step.Thought += "\n[concluded]"
					steps = append(steps, step)
					saveCheckpoint(ctx, a.checkpoint, moveID, stepIdx, step)
					return ActorResult{Steps: steps, Conclusion: resp.Content, Halt: HaltDone, TokensUsed: totalTokens}, nil
				}
			}
		}

		steps = append(steps, step)
		saveCheckpoint(ctx, a.checkpoint, moveID, stepIdx, step)

		// done 判断（无工具调用 + FinishReason=stop）
		if len(resp.ToolCalls) == 0 && resp.FinishReason == "stop" {
			return ActorResult{Steps: steps, Conclusion: resp.Content, Halt: HaltDone, TokensUsed: totalTokens}, nil
		}

		// 5. Critic 评估
		if a.critic != nil && stepIdx > 0 && stepIdx%req.Budget.CriticInterval == 0 {
			recentSteps := steps
			if len(recentSteps) > req.Budget.CriticInterval {
				recentSteps = recentSteps[len(recentSteps)-req.Budget.CriticInterval:]
			}
			// Critic 调用不阻塞主循环，忽略错误
			_, _ = a.critic.Evaluate(ctx, Move{Objective: "pentest move"}, recentSteps)
		}

		// budget 检查
		if req.Budget.MaxTokens > 0 && totalTokens >= req.Budget.MaxTokens {
			return ActorResult{Steps: steps, Halt: HaltBudget, TokensUsed: totalTokens}, nil
		}
	}

	return ActorResult{Steps: steps, Halt: HaltBudget, TokensUsed: totalTokens}, nil
}

// ─────────────────────────────────────────────
//  辅助函数
// ─────────────────────────────────────────────

func buildInitialMessages(req ActorReq) []provider.Message {
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

func saveCheckpoint(ctx context.Context, store CheckpointStore, moveID string, stepIdx int, step Step) {
	if store == nil {
		return
	}
	_ = store.Write(ctx, Checkpoint{
		TaskID:     extractTaskID(ctx),
		MoveID:     moveID,
		StepIdx:    stepIdx,
		Thought:    step.Thought,
		Hypotheses: step.Hypotheses,
		CreatedAt:  time.Now(),
	})
}
