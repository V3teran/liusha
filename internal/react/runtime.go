// Package runtime 实现 ReAct 主循环。
//
// 设计要点：
//   - 主循环：LLM 生成 → tool calls 经 Registry（含 Interceptor 链）执行 → 喂回历史 → 直到 done / 预算耗尽。
//   - Inspector hook：每 N=5 步触发，根据滑动窗判决 continue / redirect / terminate。
//   - LLM 自由收手（不强制结构化 done.reason）；MaxSteps + WatchdogSeconds + ctx
//     cancel 是死循环兜底。
package react

import (
	"context"
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

// fallbackMaxImagesInHistory 是 multimodal message 历史保留图片张数的兜底默认。
// caller 通常从 cfg.React.MaxImagesInHistory 注入；零值走此 fallback。
// 默认 3——
// 实战经验：3 张图覆盖最近视觉演化足够 reasoning，再多对 vision encoder 仅增延迟不增信息。
const fallbackMaxImagesInHistory = 3

// imageRemovedPlaceholder 是 compressImages 替换被剔除图后填入的文本，提示 LLM 该位置曾有截图。
// 中文表述更对齐国内 vision 模型（qwen-vl 系列）的语义训练分布；英文 vision 模型也能理解。
const imageRemovedPlaceholder = "[此处历史截图已折叠以节省上下文窗口]"

// Config 是 Run 的入参。
//
//   - LLM / Actions 必填；其余字段有默认值（见 Run）。
//   - OnAbort 用于外部主动停机（cron 任务取消、用户 Ctrl+C 等），返回 (true, nil) 即终止。
//   - Inspector 默认 NoopInspector；InspectorEverySteps 默认 5。
type Config struct {
	LLM          llm.Generator
	Actions      *toolfx.Registry
	Budget       Budget
	SystemPrompt string
	UserPrompt   string
	OnAbort      func(ctx context.Context) (bool, error)
	// OnNoToolCall 在 LLM 返回零 tool call（想直接收口）时被调用，用于拦截过早收口。
	// 返回 keepAlive=true 时 runtime 不终止，而是把 LLM 的 content 作为 assistant 消息、
	// observation 作为 user 消息追加进历史后 continue 主循环（让 LLM 重新决策）。
	// 典型用途：commander 仍有 running striker 时不允许收口（与 PreDoneCheck 闸门对齐）。
	// nil → 维持旧行为（直接以 no_tool_call 终止），passive/tracker 不受影响。
	OnNoToolCall        func(ctx context.Context) (keepAlive bool, observation string, err error)
	Inspector           Inspector
	InspectorEverySteps int
	// MaxImagesInHistory 是 multimodal message 历史保留的最大图片张数；
	// 零值走 fallbackMaxImagesInHistory（=3）。
	// 慢 vision 节点可 yaml 调小到 2 控延迟；商业 API（claude/gpt-4o）可放宽到 10+。
	MaxImagesInHistory int

	// HistoryCompactor 是 ReAct msgs 滑窗压缩器（防 context 爆）。
	// nil → 跳过压缩等价 disabled，runtime 仍跑只是不再蒸馏文本（图压缩 compressImages 不受影响）。
	// 通常注入 *LLMHistoryCompactor（caller 装配时拿 light_provider Generator 构造）。
	HistoryCompactor HistoryCompactor

	// ContextWindow 是当前 hunter 所用 provider 的总 context tokens 数。
	// 必填且 > 0（caller 从 cfg.Providers[providerKey].ContextWindow 取注入）。
	// 0 值会让 compactHistory 跳过——等价 disabled，但 prompt 真爆时仍会被 LLM API 报 400。
	ContextWindow int

	// HistoryCompact 是压缩超参；零值走 compactHistory 内部判断（不触发）。
	HistoryCompact HistoryCompactConfig

	// HistoryCompactTimeout 是单次 LLM 蒸馏调用的硬超时（runtime 包内传给 compactor）。
	// 零值 → 30s。超时退化为 head-truncate 兜底。
	HistoryCompactTimeout time.Duration
}

// Outcome 是 Run 的产出，便于上层做埋点 / done 报告。
//
// TerminateBy 取值：done / max_steps / max_tokens / aborted / no_tool_call。
type Outcome struct {
	TerminateBy    string
	TotalSteps     int
	TotalUsage     llm.Usage
	InspectorHints int
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
		return Outcome{}, errors.New("actions registry nil")
	}
	if cfg.Budget.MaxSteps <= 0 {
		cfg.Budget.MaxSteps = 30
	}
	if cfg.Budget.WatchdogSeconds <= 0 {
		cfg.Budget.WatchdogSeconds = 60
	}
	if cfg.Inspector == nil {
		cfg.Inspector = NoopInspector{}
	}
	if cfg.InspectorEverySteps <= 0 {
		cfg.InspectorEverySteps = 5
	}

	msgs := make([]llm.Message, 0, 4)
	if cfg.SystemPrompt != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: cfg.SystemPrompt})
	}
	if cfg.UserPrompt != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: cfg.UserPrompt})
	}

	out := Outcome{}
	window := make([]StepRecord, 0, cfg.InspectorEverySteps)

	// 跨 step 压缩状态（cooldown 节流用），栈上 alloc 即可——runtime.Run 单 goroutine。
	compactSt := &compactState{}
	historyCompactTimeout := cfg.HistoryCompactTimeout
	if historyCompactTimeout <= 0 {
		historyCompactTimeout = 30 * time.Second
	}

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

		// 2) Inspector hook：每 N 步触发一次（开局不触发）
		//
		// inspector 不能强中断主循环——曾观察到 agent 已挖到漏洞但还没 write_finding 时
		// 被 terminate 掐死，丢失 finding。terminate / redirect 统一注入 hint，让 LLM
		// 自决是否 done()；MaxSteps 兜底防死循环。
		if out.TotalSteps > 0 && out.TotalSteps%cfg.InspectorEverySteps == 0 {
			v := cfg.Inspector.Evaluate(ctx, window)
			var hint string
			switch v.Decision {
			case VerdictTerminate:
				hint = v.Hint
				if hint == "" {
					hint = "任务已完成，请立即调用 done()。如尚未 write_finding 务必先调。"
				}
				hint = "**强建议结束**：" + hint
			case VerdictRedirect:
				if v.Hint != "" {
					hint = "提示：" + v.Hint
				}
			}
			if hint != "" {
				msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: hint})
				out.InspectorHints++
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

		// 多模态历史压缩：只保留最近 cfg.MaxImagesInHistory 张图（零值 fallback 3），
		// 更早的 image_url 块换文本占位。防长 task（active 60+ 步含截图）context 撑爆 +
		// 控慢 vision 节点（公网 ollama）单步延迟雪球。
		maxImages := cfg.MaxImagesInHistory
		if maxImages <= 0 {
			maxImages = fallbackMaxImagesInHistory
		}
		compressImages(msgs, maxImages)

		// ReAct msgs 文本压缩：超 trigger_ratio × ctx_window 触发蒸馏；与图压缩并行 in-place。
		// 失败兜底 head-truncate by token budget，不阻断主循环。
		// 单步压缩硬超时（cfg.HistoryCompactTimeout 默认 30s）独立于 step watchdog；
		// 用单独 ctx 避免压缩超时把整个 Generate 也带挂。
		if cfg.HistoryCompactor != nil && cfg.ContextWindow > 0 {
			compactCtx, compactCancel := context.WithTimeout(ctx, historyCompactTimeout)
			msgs = compactHistory(compactCtx, msgs, cfg.HistoryCompactor, cfg.ContextWindow, cfg.HistoryCompact, compactSt)
			compactCancel()
		}

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

		// 4) 没有 tool call → LLM 想直接收口
		if len(res.ToolCalls) == 0 {
			// OnNoToolCall 闸门：仍有未完成的 child（如 running striker）时拦下过早收口，
			// 把 LLM 的收口陈述 + observation 注入历史后 continue，让 LLM 重新决策。
			// out.TotalSteps 已自增，Budget.MaxSteps 仍是兜底，不会无限 keep-alive。
			if cfg.OnNoToolCall != nil {
				keepAlive, observation, hookErr := cfg.OnNoToolCall(ctx)
				if hookErr != nil {
					return out, fmt.Errorf("step %d OnNoToolCall: %w", out.TotalSteps, hookErr)
				}
				if keepAlive {
					msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: res.Content})
					msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: observation})
					continue
				}
			}
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

		// 串行处理结果：tool_result message 必须按 tool_call_id 原始顺序追加，
		// Anthropic/OpenAI 协议都要求与 assistant 那条消息里的 tool_calls 数组顺序一致——
		// 乱序会导致 next-turn LLM Generate API 报错或行为不可预测。
		// 这里 results 数组下标=tc 原始下标，遍历即保序。
		var sawDone bool
		for _, r := range results {
			tc, tcRes, execErr := r.tc, r.res, r.err

			obs := tcRes.Output
			if execErr != nil {
				obs = []byte(fmt.Sprintf(`{"error":%q}`, execErr.Error()))
			}
			// 含图 tool result 用 ContentParts 路径——Anthropic tool_result 可含 image block；
			// OpenAI 协议族走 openai_compat strip-and-degrade，LLM 看到 [Image removed] 占位文本。
			// 纯文本（passive / 非截图 active）走老 Content 路径，与 OpenAI 协议族 100% 兼容。
			toolMsg := llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Name,
			}
			if len(tcRes.Images) > 0 {
				parts := make([]llm.ContentPart, 0, 1+len(tcRes.Images))
				parts = append(parts, llm.ContentPart{Type: "text", Text: string(obs)})
				for i := range tcRes.Images {
					parts = append(parts, llm.ContentPart{
						Type:     "image_url",
						ImageURL: &tcRes.Images[i],
					})
				}
				toolMsg.ContentParts = parts
			} else {
				toolMsg.Content = string(obs)
			}
			msgs = append(msgs, toolMsg)
			// 只信 tcRes.Done（工具 Execute 成功显式标记终止）——不再用 tc.Name=="done"
			// 兜底，否则 done 工具 PreDoneCheck 拒绝（execErr != nil + tcRes.Done=false）
			// 也会因名字匹配触发终止，让 PR3 subtask swarm "commander 等 striker" 闸形同虚设。
			if tcRes.Done {
				sawDone = true
			}

			// 喂给 Inspector 的滑动窗（仅保留最近 InspectorEverySteps*2 条，避免无限增长）。
			// FullObs 只对窗口末尾 1 条有意义（防 ObsSummary 截断丢 SUCCESS 关键字误判进度），
			// append 新条目前先把上一条的 FullObs 清空——节内存且 prompt 只读末尾。
			if n := len(window); n > 0 {
				window[n-1].FullObs = ""
			}
			window = append(window, StepRecord{
				StepIdx:    out.TotalSteps,
				ActionName: tc.Name,
				Args:       tc.Arguments,
				ObsSummary: tcRes.Summary,
				FullObs:    string(obs),
			})
			if maxLen := cfg.InspectorEverySteps * 2; len(window) > maxLen {
				window = window[len(window)-maxLen:]
			}
		}

		if sawDone {
			out.TerminateBy = "done"
			return out, nil
		}
	}
}

// compressImages 倒序遍历 msgs，保留最近 maxImages 张 image_url；更早的 image_url
// 块原地替换为 imageRemovedPlaceholder 文本占位。
//
// 设计原理：保留视觉历史的近期决策上下文，
// 阶段性卸下旧图避免 context 撑爆（每张 ~50KB base64）。in-place 修改 msgs，
// 不分配新 slice 减小 GC 压力。
func compressImages(msgs []llm.Message, maxImages int) {
	if maxImages <= 0 {
		return
	}
	imageCount := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		parts := msgs[i].ContentParts
		if len(parts) == 0 {
			continue
		}
		for j := range parts {
			if parts[j].Type != "image_url" {
				continue
			}
			if imageCount >= maxImages {
				parts[j].Type = "text"
				parts[j].Text = imageRemovedPlaceholder
				parts[j].ImageURL = nil
			} else {
				imageCount++
			}
		}
	}
}
