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

// maxImagesInHistory 是 multimodal message 历史中保留的最大图片张数。
// 超过的最早 image_url 块在 LLM Generate 前被替换为 "[Previously attached image removed...]"
// 文本占位（类比 strix MemoryCompressor max_images=3，但 liusha 截图按需触发故放宽到 5）。
const maxImagesInHistory = 5

// imageRemovedPlaceholder 是 compressImages 替换被剔除图后填入的文本，提示 LLM 该位置曾有截图。
const imageRemovedPlaceholder = "[Previously attached image removed to preserve context]"

// Config 是 Run 的入参。
//
//   - LLM / Actions 必填；其余字段有默认值（见 Run）。
//   - OnAbort 用于外部主动停机（cron 任务取消、用户 Ctrl+C 等），返回 (true, nil) 即终止。
//   - Inspector 默认 NoopInspector；InspectorEverySteps 默认 5。
type Config struct {
	LLM                llm.Generator
	Actions            *toolfx.Registry
	Budget             Budget
	SystemPrompt       string
	UserPrompt         string
	OnAbort            func(ctx context.Context) (bool, error)
	Inspector           Inspector
	InspectorEverySteps int
}

// Outcome 是 Run 的产出，便于上层做埋点 / done 报告。
//
// TerminateBy 取值：done / max_steps / max_tokens / aborted / no_tool_call。
type Outcome struct {
	TerminateBy   string
	TotalSteps    int
	TotalUsage    llm.Usage
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

		// 多模态历史压缩：只保留最近 maxImagesInHistory 张图，更早的 image_url 块换文本占位。
		// 防止长 task（active 60+ 步含截图）context 被图撑爆 — 类比 strix max_images=3 设计。
		compressImages(msgs, maxImagesInHistory)

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
			// 也会因名字匹配触发终止，让 PR3 subtask swarm "父等子" 闸形同虚设。
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
// 与 strix MemoryCompressor._handle_images 等价：保留视觉历史的近期决策上下文，
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
