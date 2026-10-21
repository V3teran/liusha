// Package evaluator 实现验证层：复现晋升门 + LLM 语义裁决器（ReAct 形态）。
//
// judge.go 职责：分工铁律——复现门裁决官经 replay_for_verification 工具自主重放取证、
// 按需多次验证，最终输出结构化裁决。断言命中 ≠ 主张成立（命中内容可能
// 本就是常态输出），语义终裁交给裁决官——这是晋升门的最后一道闸。
package evaluator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/reasoning"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/tools/manifest"
)

// ReplayFunc 是裁决官可调用的机器重放（由 PromotionEvaluator 注入，绑定本 Attempt 配方）。
type ReplayFunc func(ctx context.Context) (Result, error)

// LLMJudge 是晋升门的语义裁决器接口（ReAct 实现 + 测试 stub）。
type LLMJudge interface {
	Judge(ctx context.Context, hypothesis string, recipe, initialEvidence json.RawMessage, replay ReplayFunc) (Verdict, error)
}

// RouterJudge 经 ReAct runtime 跑语义裁决：工具（replay_for_verification）+ 终裁 JSON。
type RouterJudge struct {
	router        *llm.Router
	logger        zerolog.Logger
	thoughts      reasoning.Tap      // 思考流出口（认知循环注入；nil=不流式）
	functionTools []string           // function_tools 白名单（nil=全量；空=空集）
	extraTools    []registry.Tool    // 装配层注入的额外工具（run_command 等白名单工具——跨包工具由 cognition 构造）
	cliManifest   *manifest.Manifest // CLI 工具目录（cli_tools 过滤后；渲染进 SystemPrompt 供 run_command 调用）
	skills        []*skill.Card      // Tier 1 skill 索引（agent.skills 声明；与 executor 同口径渲染）
	charter       string             // 角色章程（agent.system_prompt，运维可调；空=不渲染）
	complexity    llm.Complexity     // LLM 档位（agent.complexity，文档 complexity 种子 → 三级缓存读）
	maxIt         int                // ReAct 迭代上限（agent.max_iterations；0=不设限）
}

// NewRouterJudge 构造 ReAct 裁决官。
func NewRouterJudge(router *llm.Router, logger zerolog.Logger) *RouterJudge {
	return &RouterJudge{router: router, logger: logger.With().Str("component", "evaluator_judge").Logger()}
}

// WithFunctionTools 注入 function_tools 白名单（链式）。白名单同时约束本包工具
// （replay_for_verification）与 extraTools——统一口径：不在名单上的工具不装配。
func (j *RouterJudge) WithFunctionTools(names []string) *RouterJudge {
	j.functionTools = names
	return j
}

// WithCLIManifest 注入 CLI 工具目录（cli_tools 白名单过滤后；nil 不渲染）。
func (j *RouterJudge) WithCLIManifest(m *manifest.Manifest) *RouterJudge {
	j.cliManifest = m
	return j
}

// WithSkills 注入 Tier 1 skill 索引（agent.skills 声明的 frontmatter）。裁决官与
// executor 共用同一渲染（skill.RenderIndex）——两侧对"有哪些手册可读"认知一致。
func (j *RouterJudge) WithSkills(cards []*skill.Card) *RouterJudge {
	j.skills = cards
	return j
}

// WithSystemPrompt 注入角色章程（agent.system_prompt 正文，前端可编辑）。
func (j *RouterJudge) WithSystemPrompt(charter string) *RouterJudge {
	j.charter = charter
	return j
}

// WithThoughts 注入思考流出口（nil=不流式）。
func (j *RouterJudge) WithThoughts(tap reasoning.Tap) *RouterJudge {
	j.thoughts = tap
	return j
}

// WithMaxIterations 注入 ReAct 迭代上限（agent.max_iterations；0=不设限）。
func (j *RouterJudge) WithMaxIterations(n int) *RouterJudge {
	j.maxIt = n
	return j
}

// WithComplexity 注入 LLM 档位（agent.complexity，文档 complexity 种子经三级缓存读；
// 空回退 medium）。
func (j *RouterJudge) WithComplexity(complexity string) *RouterJudge {
	j.complexity = llm.ParseComplexity(complexity)
	return j
}

// WithExtraTools 注入跨包工具实例（装配层按白名单经 tools.BuildTools 构造）。
func (j *RouterJudge) WithExtraTools(ts []registry.Tool) *RouterJudge {
	j.extraTools = append(j.extraTools, ts...)
	return j
}

// Judge 实现 LLMJudge：ReAct 循环内 LLM 自主调重放工具采证，FinalAnswer 为裁决 JSON。
func (j *RouterJudge) Judge(
	ctx context.Context,
	hypothesis string,
	recipe, initialEvidence json.RawMessage,
	replay ReplayFunc,
) (Verdict, error) {
	provider, err := j.router.For(ctx, j.complexity)
	if err != nil {
		return Verdict{}, fmt.Errorf("judge: 获取 provider: %w", err)
	}

	react := runtime.NewReActRuntime()
	if replay != nil && registry.Allows(j.functionTools, "replay_for_verification") {
		if err := react.RegisterTool(newReplayTool(replay)); err != nil {
			return Verdict{}, fmt.Errorf("judge: 注册 replay_for_verification 失败: %w", err)
		}
	}
	for _, t := range j.extraTools {
		if registry.Allows(j.functionTools, t.Name()) {
			if err := react.RegisterTool(t); err != nil {
				return Verdict{}, fmt.Errorf("judge: 注册工具 %s 失败: %w", t.Name(), err)
			}
		}
	}

	// 审计标签（task/role/agent_run_id）由 evaluator agent 在 ctx 注入，此处继承不覆盖——
	// 覆盖会丢 TaskID（ judge 落库行 task_id 曾恒 NULL）。

	// 构建中间件链：防止验证循环、无进展
	// 注意：Evaluator 不需要 GoalAlignment（验证任务目标固定）
	middlewareChain := runtime.NewMiddlewareChain(
		runtime.NewLoopDetectionMiddleware(12, 10),    // 连续 12 次相同工具 / 10 次相似思考
		runtime.NewProgressTrackingMiddleware(20, 12), // 20 次无新输出 / 12 次连续失败
	)

	var onThoughtDelta, onThinkingDelta func(string)
	if j.thoughts != nil {
		onThoughtDelta = j.thoughts.Delta
		onThinkingDelta = j.thoughts.ThinkingDelta
	}
	result, err := react.Run(ctx, &runtime.ReActConfig{
		Objective:            j.buildObjective(hypothesis, recipe, initialEvidence),
		SystemPrompt:         j.systemPrompt(),
		LLMProvider:          provider,
		MaxIterations:        j.maxIt, // 迭代上限 = agent.max_iterations（配置即事实；0=不设限）
		OnThoughtDelta:       onThoughtDelta,
		OnThinkingDelta:      onThinkingDelta,
		MaxTokens:            1500,
		MessageModifierChain: runtime.NewDefaultModifierChain(10),
		MiddlewareChain:      middlewareChain,
	})
	if err != nil {
		return Verdict{}, fmt.Errorf("judge: ReAct 失败: %w", err)
	}
	// 终态叙述在解析边界完成：裁决协议转人读摘要作正文、协议原文随 raw 折叠
	// 可查（谁解析谁叙述，前端不解释协议）；解析失败时正文回退原文，终态帧照落。
	v, verr := parseVerdict(result.FinalAnswer)
	round := reasoning.RoundOf(result)
	if verr == nil {
		round.Summary, round.Raw = verdictNarrative(v), result.FinalAnswer
	}
	if j.thoughts != nil {
		j.thoughts.Done(round)
	}
	return v, verr
}

// verdictNarrative 把裁决协议转成时间线人读叙述：结论 + 置信度 + 理由。
func verdictNarrative(v Verdict) string {
	ruling := v.Verdict
	switch v.Verdict {
	case "confirmed":
		ruling = "坐实"
	case "refuted":
		ruling = "证伪"
	}
	head := fmt.Sprintf("裁决：%s（置信度 %.0f%%）", ruling, v.Confidence*100)
	if v.Reasoning == "" {
		return head
	}
	return head + "\n\n理由：" + v.Reasoning
}

// buildObjective 组装裁决输入（假设 + 自包含配方 + 预跑对照摘要）。
func (j *RouterJudge) buildObjective(hypothesis string, recipe, initialEvidence json.RawMessage) string {
	var sb strings.Builder
	sb.WriteString("裁决以下假设是否坐实。\n\n## 假设\n")
	if hypothesis != "" {
		sb.WriteString(hypothesis)
	} else {
		sb.WriteString("（陈述缺失，从配方与证据推断）")
	}
	sb.WriteString("\n\n## 复现配方（自包含完整 HTTP 请求）\n")
	sb.WriteString(stringOrEmpty(recipe))
	sb.WriteString("\n\n## 配方预跑证据（机器采集，参考——须自主二次验证）\n")
	sb.WriteString(stringOrEmpty(initialEvidence))
	sb.WriteString("\n\n先用 replay_for_verification 复核机器证据；证据含 baseline_* 字段时对比基线/利用请求差分（一致即无差分）；")
	sb.WriteString("证据标注 domain=generic（无机器重放通道）时改用 run_command 按 recipe 步骤自主执行取证；")
	sb.WriteString("需要独立取证时用 run_command（如 curl 重放配方 request、用正常参数做对照）。基于你亲见的证据裁决。")
	sb.WriteString("\n\n裁决对象必须是可复现验证的主张断言；非主张类断言（枚举结果、状态汇报）按章程规则处理。")
	sb.WriteString("confirmed 时按章程契约附结构化指纹（finding 字段），供系统对同一发现去重。")
	return sb.String()
}

// systemPrompt 组装裁决官 system prompt：
//
//	角色章程（agent.system_prompt——DB 事实源，种子 = agents/evaluator.md 正文，前端可调）
//	+ skill 索引（有声明且 read_skill 在白名单时渲染）
//	+ CLI 目录（有 CLI 白名单时渲染，供独立复核经 run_command 调用）
//
// 机制契约（replay 工作方式、裁决 JSON 格式、因果硬规则）由章程承载——
// 改契约 = 改 agents/evaluator.md + make reseed。charter 为空时报错，强制运维填写。
func (j *RouterJudge) systemPrompt() string {
	charter := strings.TrimSpace(j.charter)
	if charter == "" {
		// 返回空字符串，让调用方的 ReAct.Run 抛出更明确的错误
		return ""
	}
	var sb strings.Builder
	sb.WriteString(charter)
	// Tier 1 与 executor 同口径：read_skill 在白名单里才宣传（宣传=事实）。
	if len(j.skills) > 0 && registry.Allows(j.functionTools, "read_skill") {
		if idx := skill.RenderIndex(j.skills); idx != "" {
			sb.WriteString("\n")
			sb.WriteString(idx)
		}
	}
	if j.cliManifest != nil && len(j.cliManifest.Tools) > 0 {
		sb.WriteString("\n**沙箱 CLI 工具**（经 run_command 调用，独立复核取证可用）:\n")
		for _, t := range j.cliManifest.Tools {
			sb.WriteString("- " + t.Name + ": " + t.Description + "\n")
		}
	}
	return sb.String()
}

// parseVerdict 从 FinalAnswer 提取裁决 JSON（含坐实发现的结构化指纹）。
func parseVerdict(content string) (Verdict, error) {
	if content == "" {
		return Verdict{}, fmt.Errorf("judge: 裁决输出为空")
	}
	var v struct {
		Verdict     string          `json:"verdict"`
		Confidence  float64         `json:"confidence"`
		Reasoning   string          `json:"reasoning"`
		Fingerprint json.RawMessage `json:"finding"`
	}
	if err := json.Unmarshal([]byte(llm.ExtractJSON(content)), &v); err != nil {
		return Verdict{}, fmt.Errorf("judge: 解析裁决失败: %w (输出: %.200s)", err, content)
	}
	switch v.Verdict {
	case VerdictConfirmed, VerdictRefuted:
		return Verdict{
			Verdict:     v.Verdict,
			Confidence:  v.Confidence,
			Reasoning:   v.Reasoning,
			Fingerprint: v.Fingerprint,
		}, nil
	default:
		return Verdict{}, fmt.Errorf("judge: 未知 verdict %q", v.Verdict)
	}
}

func stringOrEmpty(b json.RawMessage) string {
	if len(b) == 0 {
		return "（无）"
	}
	return string(b)
}

// ─── replay_for_verification 工具 ────────────────────────────────────────────

// replayTool 把 ReplayFunc 包成 registry.Tool（绑定本 Attempt 的配方，LLM 可多次调用取证）。
type replayTool struct {
	registry.BaseTool
	replay ReplayFunc
}

func newReplayTool(replay ReplayFunc) *replayTool {
	t := &replayTool{replay: replay}
	t.WithTimeout(60 * time.Second)
	return t
}

func (t *replayTool) Name() string      { return "replay_for_verification" }
func (t *replayTool) ShortDesc() string { return "重放复现配方取得机器证据" }
func (t *replayTool) Desc() string {
	return "执行本假设的复现配方（重放源流量并跑机器断言），返回状态码/响应片段/断言判定明细等证据。可多次调用。"
}
func (t *replayTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"description":"无参数——重放本假设的既定配方"}`)
}

func (t *replayTool) Execute(ctx context.Context, _ json.RawMessage) (registry.ToolResult, error) {
	res, err := t.replay(ctx)
	if err != nil {
		return registry.ToolResult{Error: "重放失败: " + err.Error()}, nil
	}
	ev := res.Evaluation
	if len(ev) == 0 {
		ev = json.RawMessage("{}")
	}
	out, _ := json.Marshal(map[string]interface{}{
		"note":        "assert_check 是 executor 声明预期的核验明细（参考，非结论）；坐实与否由你裁决",
		"evidence":    ev,
		"duration_ms": res.DurationMs,
	})
	return registry.ToolResult{Output: string(out)}, nil
}
