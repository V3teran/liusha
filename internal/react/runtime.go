// Package runtime 实现 ReAct 主循环。
//
// 设计要点：
//   - 主循环：LLM 生成 → tool calls 经 Registry（含中间件链）执行 → 喂回历史 → 直到 done / 预算耗尽。
//   - Observer hook：每 N=5 步触发，根据滑动窗判决 keep_going / steer_with_hint / abort_low_value。
//   - DoneValidator：T22.5 中间件抛 ErrDoneNotReady 时 runtime 注入 user msg 让 LLM 继续；
//     被拒达到 doneForceMaxRejects 后强制放行，避免死循环。
//   - LoopDetector 已砍——MaxSteps + DoneValidator 是足够的兜底。
package react

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// debugLogger 步级日志（LLM Generate / tool_calls / finish_reason 等），走 logx 统一落 file。
// 默认 Info 级；生产想降噪用 LIUSHA_LOG_LEVEL=warn 整体降级，无需独立开关。
var debugLogger = logx.New("react.runtime")

// fallbackDoneForceMaxRejects：Config.DoneForceMaxRejects 为 0 时使用。
// 正常路径由 cmd/scanner 从 cfg.React.DoneForceMaxRejects 注入。
const fallbackDoneForceMaxRejects = 3

// Config 是 Run 的入参。
//
//   - LLM / Actions 必填；其余字段有默认值（见 Run）。
//   - OnAbort 用于外部主动停机（cron 任务取消、用户 Ctrl+C 等），返回 (true, nil) 即终止。
//   - Observer 默认 NoopObserver；ObserverEverySteps 默认 5。
type Config struct {
	LLM                 llm.Generator
	Actions             *toolfx.Registry
	Budget              Budget
	SystemPrompt        string
	UserPrompt          string
	OnAbort             func(ctx context.Context) (bool, error)
	Observer            Observer
	ObserverEverySteps  int
	DoneForceMaxRejects int // ≤0 → fallbackDoneForceMaxRejects
}

// Outcome 是 Run 的产出，便于上层做埋点 / done 报告。
//
// TerminateBy 取值：done / done_force / max_steps / max_tokens / aborted /
// observer_abort / no_tool_call。
type Outcome struct {
	TerminateBy    string
	TotalSteps     int
	TotalUsage     llm.Usage
	ObserverHints  int
	DoneForceCount int
}

// Run 执行 ReAct 主循环直到终止条件命中。
//
// 不要在多个 goroutine 共享同一个 LLM Generator（见 llm 包注释）；
// Run 内部对 cfg 做了字段补全，但 Config 入参本身不被修改（按值传入）。
func Run(ctx context.Context, cfg Config) (Outcome, error) {
	if cfg.LLM == nil {
		return Outcome{}, errors.New("LLM generator nil")
	}
	if cfg.Actions == nil {
		return Outcome{}, errors.New("Actions registry nil")
	}
	if cfg.Budget.MaxSteps <= 0 {
		cfg.Budget.MaxSteps = 30
	}
	if cfg.Budget.WatchdogSeconds <= 0 {
		cfg.Budget.WatchdogSeconds = 60
	}
	if cfg.Observer == nil {
		cfg.Observer = NoopObserver{}
	}
	if cfg.ObserverEverySteps <= 0 {
		cfg.ObserverEverySteps = 5
	}
	if cfg.DoneForceMaxRejects <= 0 {
		cfg.DoneForceMaxRejects = fallbackDoneForceMaxRejects
	}

	msgs := make([]llm.Message, 0, 4)
	if cfg.SystemPrompt != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: cfg.SystemPrompt})
	}
	if cfg.UserPrompt != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: cfg.UserPrompt})
	}

	out := Outcome{}
	window := make([]StepRecord, 0, cfg.ObserverEverySteps)
	doneRejectCount := 0

	for {
		// 1) 预算 / 取消检查
		if out.TotalSteps >= cfg.Budget.MaxSteps {
			out.TerminateBy = "max_steps"
			return out, nil
		}
		if cfg.Budget.MaxTokens > 0 && out.TotalUsage.InTokens+out.TotalUsage.OutTokens >= cfg.Budget.MaxTokens {
			out.TerminateBy = "max_tokens"
			return out, nil
		}
		if cfg.OnAbort != nil {
			if abort, err := cfg.OnAbort(ctx); err == nil && abort {
				out.TerminateBy = "aborted"
				return out, nil
			}
		}

		// 2) Observer hook：每 N 步触发一次（开局不触发）
		if out.TotalSteps > 0 && out.TotalSteps%cfg.ObserverEverySteps == 0 {
			v := cfg.Observer.Evaluate(ctx, window)
			switch v.Decision {
			case VerdictAbort:
				out.TerminateBy = "observer_abort"
				return out, nil
			case VerdictSteer:
				if v.Hint != "" {
					msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: "提示：" + v.Hint})
					out.ObserverHints++
				}
			}
		}

		// 3) LLM 生成（单步 watchdog）
		stepCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Budget.WatchdogSeconds)*time.Second)
		schemas := cfg.Actions.Schemas()
		toolNames := make([]string, len(schemas))
		for i, s := range schemas {
			toolNames[i] = s.Name
		}
		debugLogger.Info().
			Int("step", out.TotalSteps+1).
			Int("msgs_count", len(msgs)).
			Int("tools_count", len(schemas)).
			Strs("tool_names", toolNames).
			Msg("LLM Generate 调用")

		res, err := cfg.LLM.Generate(stepCtx, msgs, schemas)
		cancel()
		if err != nil {
			return out, fmt.Errorf("step %d generate: %w", out.TotalSteps+1, err)
		}
		out.TotalSteps++
		out.TotalUsage = out.TotalUsage.Add(res.Usage)

		debugLogger.Info().
			Int("step", out.TotalSteps).
			Str("finish_reason", res.FinishReason).
			Int("tool_calls", len(res.ToolCalls)).
			Str("content", res.Content).
			Int("in_tokens", res.Usage.InTokens).
			Int("out_tokens", res.Usage.OutTokens).
			Msg("LLM Generate 返回")

		// 4) 没有 tool call → LLM 想直接收口，结束循环
		if len(res.ToolCalls) == 0 {
			msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: res.Content})
			out.TerminateBy = "no_tool_call"
			return out, nil
		}
		msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, ToolCalls: res.ToolCalls, Content: res.Content})

		// 5) 并行执行 tool_calls（多 spawn_skill 自动 goroutine 并发）
		type toolExecResult struct {
			tc  llm.ToolCall
			res toolfx.Result
			err error
		}

		results := make([]toolExecResult, len(res.ToolCalls))
		var wg sync.WaitGroup
		for i, tc := range res.ToolCalls {
			wg.Add(1)
			go func(i int, tc llm.ToolCall) {
				defer wg.Done()
				r, e := cfg.Actions.Execute(ctx, tc.Name, tc.Arguments)
				results[i] = toolExecResult{tc: tc, res: r, err: e}
			}(i, tc)
		}
		wg.Wait()

		// 串行处理结果（保 tool_call_id 顺序、汇总 done）
		var sawDone bool
		for _, r := range results {
			tc, tcRes, execErr := r.tc, r.res, r.err

			// DoneValidator 中间件抛错：注入 user msg 让 LLM 继续；超过阈值强制放行
			if e, ok := IsDoneNotReady(execErr); ok {
				doneRejectCount++
				if doneRejectCount >= cfg.DoneForceMaxRejects {
					out.TerminateBy = "done_force"
					out.DoneForceCount = 1
					return out, nil
				}
				// 必须先回填 done 这次 tool_call 对应的 tool message —— 否则 assistant
				// 含 N 个 tool_calls 但只有 N-1 个 tool message，DeepSeek 等严格 OpenAI
				// 协议实现会 400 "insufficient tool messages following tool_calls"。
				obsBody, _ := json.Marshal(map[string]any{
					"error":   "done_not_ready",
					"missing": e.Missing,
				})
				msgs = append(msgs, llm.Message{
					Role:       llm.RoleTool,
					ToolCallID: tc.ID,
					Name:       tc.Name,
					Content:    string(obsBody),
				})
				msgs = append(msgs, llm.Message{
					Role:    llm.RoleUser,
					Content: fmt.Sprintf("你声称完成但未达终止条件 [missing: %v]，继续工作。", e.Missing),
				})
				continue
			}

			obs := tcRes.Output
			if execErr != nil {
				obs = []byte(fmt.Sprintf(`{"error":%q}`, execErr.Error()))
			}
			msgs = append(msgs, llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    string(obs),
			})
			if tcRes.Done || tc.Name == "done" {
				sawDone = true
			}

			// 喂给 Observer 的滑动窗（仅保留最近 ObserverEverySteps*2 条，避免无限增长）
			window = append(window, StepRecord{
				StepIdx:    out.TotalSteps,
				ActionName: tc.Name,
				Args:       tc.Arguments,
				ObsSummary: tcRes.Summary,
			})
			if maxLen := cfg.ObserverEverySteps * 2; len(window) > maxLen {
				window = window[len(window)-maxLen:]
			}
		}

		if sawDone {
			out.TerminateBy = "done"
			return out, nil
		}
	}
}
