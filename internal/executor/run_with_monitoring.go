package executor

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/registry"
)

// runWithMonitoring 是双协程模式：执行协程 + 监察协程 + 事件监听。
func (a *Agent) runWithMonitoring(ctx context.Context, actionID string, req ExecutorReq) (ExecutorResult, error) {
	// 创建可取消的 context
	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// 共享状态
	state := &executionState{
		goal:           req.System + "\n" + req.Inbox[0].Content, // 简化：取第一条消息作为目标
		correctionChan: make(chan string, 10), // 增加缓冲，避免阻塞
	}

	// 结果通道
	resultChan := make(chan ExecutorResult, 1)
	errorChan := make(chan error, 1)

	// 协程 1: 执行任务
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		result, err := a.executeLoop(execCtx, actionID, req, state)
		if err != nil {
			errorChan <- err
		} else {
			resultChan <- result
		}
	}()

	// 协程 2: 监察任务
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.monitorLoop(execCtx, cancel, state)
	}()

	// 协程 3: 事件监听（如果有事件总线）
	if a.eventBus != nil {
		subscription := a.eventBus.Subscribe(execCtx, actionID)
		defer subscription.Unsubscribe()

		wg.Add(1)
		go func() {
			defer wg.Done()
			a.eventLoop(execCtx, cancel, subscription, state)
		}()
	}

	// 等待执行完成
	select {
	case result := <-resultChan:
		cancel() // 停止所有协程
		wg.Wait()
		return result, nil
	case err := <-errorChan:
		cancel()
		wg.Wait()
		return ExecutorResult{}, err
	case <-ctx.Done():
		cancel()
		wg.Wait()
		return ExecutorResult{Halt: HaltCancelled}, ctx.Err()
	}
}

// executeLoop 执行协程：ReAct 循环。
func (a *Agent) executeLoop(ctx context.Context, actionID string, req ExecutorReq, state *executionState) (ExecutorResult, error) {
	// 从 checkpoint 恢复起点
	startStep := 0
	if a.checkpoint != nil {
		if cp, err := a.checkpoint.Last(ctx, extractTaskID(ctx), actionID); err == nil && cp != nil {
			startStep = cp.StepIdx + 1
		}
	}

	// 构建初始消息列表
	messages := buildInitialMessages(req)

	// 读取并应用 steering 消息（从 worldmodel）
	messages = a.applySteeringMessages(ctx, actionID, messages)

	totalTokens := 0

	// 激活 Constraint 检查
	if len(req.PendingConstraints) > 0 {
		ctx = registry.WithConstraints(ctx, req.PendingConstraints)
	}

	for stepIdx := startStep; stepIdx < req.Budget.MaxSteps; stepIdx++ {
		// 检查是否被监察协程停止
		if stopped, _ := state.isStopped(); stopped {
			steps := state.getRecentSteps(999999) // 获取所有步骤
			return ExecutorResult{
				Steps:      steps,
				Halt:       HaltCancelled,
				TokensUsed: state.getTokens(),
			}, nil
		}

		// 检查是否有纠偏消息
		select {
		case correction := <-state.correctionChan:
			// 注入纠偏消息
			messages = append(messages, provider.Message{
				Role:    "user",
				Content: "SELF-CORRECTION: " + correction,
			})
		default:
		}

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

		// 2. 调用 Provider
		tools := a.reg.Schemas()
		resp, err := a.provider.Complete(ctx, provider.Request{
			Messages:  messages,
			Tools:     tools,
			MaxTokens: req.Budget.MaxTokens,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				steps := state.getRecentSteps(999999)
				return ExecutorResult{Steps: steps, Halt: HaltCancelled, TokensUsed: state.getTokens()}, nil
			}
			steps := state.getRecentSteps(999999)
			return ExecutorResult{Steps: steps, Halt: HaltError, TokensUsed: state.getTokens()}, err
		}
		totalTokens += resp.Usage.InTokens + resp.Usage.OutTokens
		state.addTokens(resp.Usage.InTokens + resp.Usage.OutTokens)

		// 3. 构建 Step
		step := Step{Index: stepIdx, Thought: resp.Content}

		// 记录 tool calls
		for _, tc := range resp.ToolCalls {
			step.ToolCalls = append(step.ToolCalls, ToolCall{
				ID:   tc.ID,
				Name: tc.Name,
				Args: string(tc.Arguments), // RawMessage 转 string
			})
		}

		state.addStep(step)

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

			// 把工具结果追加为 tool messages，并记录到 step
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

				// 记录工具结果到 step
				step.ToolResults = append(step.ToolResults, ToolResult{
					ToolCallID: tc.ID,
					Output:     r.Output,
					Error:      r.Error,
				})

				if a.emitter != nil {
					a.emitter.Emit(SSEEvent{Kind: "tool_end", ActionID: actionID, StepID: stepIdx, Data: tc.Name})
				}
			}

			// 更新 state 中的 step（包含 tool results）
			state.updateStep(stepIdx, step)
		}

		// 5. checkpoint
		saveCheckpoint(ctx, a.checkpoint, actionID, stepIdx, step)

		// budget 检查
		if req.Budget.MaxTokens > 0 && totalTokens >= req.Budget.MaxTokens {
			steps := state.getRecentSteps(999999)
			return ExecutorResult{Steps: steps, Halt: HaltBudget, TokensUsed: state.getTokens()}, nil
		}
	}

	steps := state.getRecentSteps(999999)
	return ExecutorResult{Steps: steps, Halt: HaltBudget, TokensUsed: state.getTokens()}, nil
}

// monitorLoop 监察协程：每 N 步评估一次。
func (a *Agent) monitorLoop(ctx context.Context, cancel context.CancelFunc, state *executionState) {
	lastEvaluatedStep := 0

	ticker := time.NewTicker(1 * time.Second) // 每秒检查一次步数
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			currentStep := state.getCurrentStep()

			// 检查是否达到评估间隔
			if currentStep-lastEvaluatedStep < a.monitorStepInterval {
				continue
			}

			// 获取最近 N 步
			recentSteps := state.getRecentSteps(a.monitorEvaluateSteps)
			if len(recentSteps) == 0 {
				continue
			}

			// 更新最后评估步数
			lastEvaluatedStep = currentStep

			// 自我评估
			assessment, err := a.selfEvaluate(ctx, state.goal, recentSteps)
			if err != nil {
				// 评估失败，继续
				continue
			}

			// 根据评估结果决策
			if assessment.Status == "off_track" {
				if assessment.Severity == "high" {
					// 严重跑偏：Kill（停止执行）
					a.logger.Warn().
						Str("status", assessment.Status).
						Str("severity", assessment.Severity).
						Str("reason", assessment.Reasoning).
						Msg("actor killing itself")

					state.stop(true)
					cancel()
					return
				} else if assessment.Correction != "" {
					// 轻微跑偏：Steer（发送纠偏消息）
					a.logger.Info().
						Str("status", assessment.Status).
						Str("severity", assessment.Severity).
						Str("correction", assessment.Correction).
						Msg("actor steering itself")

					select {
					case state.correctionChan <- assessment.Correction:
					default:
					}
				}
			} else if assessment.Status == "stalled" {
				// 卡住：Kill
				a.logger.Warn().
					Str("status", assessment.Status).
					Str("reason", assessment.Reasoning).
					Msg("actor killing itself (stalled)")

				state.stop(true)
				cancel()
				return
			}

		case <-ctx.Done():
			return
		}
	}
}
