// Package evaluator 实现验证层：复现晋升门 + LLM 语义判定器（ReAct 形态）。
//
// judge.go 职责：分工铁律——复现门评估者经 repro_replay 工具自主重放取证、
// 按需多次验证，最终输出结构化判定。断言命中 ≠ 主张成立（命中内容可能
// 本就是常态输出），语义终判交给评估者——这是晋升门的最后一道闸。
package evaluator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/allowlist"
	evaluatorcontract "github.com/V3teran/liusha/internal/evaluator/contract"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/reasoning"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/scenario"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/tools/manifest"
)

// ReplayFunc 是评估者可调用的机器重放（由 PromotionGate 注入，绑定本 Claim 配方）。
type ReplayFunc func(ctx context.Context) (Result, error)

// LLMJudge 是晋升门的语义判定器接口（ReAct 实现 + 测试 stub）。
type LLMJudge interface {
	Judge(ctx context.Context, hypothesis string, recipe, initialEvidence json.RawMessage, replay ReplayFunc) (Verdict, error)
}

// RouterJudge 经 ReAct runtime 跑语义判定：工具（repro_replay）+ 终判 JSON。
type RouterJudge struct {
	router             *llm.Router
	logger             zerolog.Logger
	thoughts           reasoning.Tap                     // 思考流出口（认知循环注入；nil=不流式）
	functionTools      []string                          // function_tools 白名单（nil=拒绝；[]=空集；["*"]=全量；[...]=白名单）
	extraTools         []registry.Tool                   // 装配层注入的额外工具（command_run 等白名单工具——跨包工具由 cognition 构造）
	cliManifest        *manifest.ToolManifest            // CLI 工具目录（cli_tools 过滤后；渲染进 SystemPrompt 供 command_run 调用）
	skills             []*skill.Card                     // Tier 1 skill 索引（agent.skills 声明；与 executor 同口径渲染）
	charter            string                            // 角色章程（生效值：出厂叠加用户自定义）
	scenario           string                            // 任务场景块（scenario.Render；空 = 无场景）
	complexity         llm.Complexity                    // LLM 档位（agent 生效配置的 complexity）
	maxIt              int                               // ReAct 迭代上限（agent.max_iterations；0=不设限）
	contextRatio       float64                           // 上下文历史预算占 provider.context_window 的比例（0=压缩器内置兜底）
	distiller          runtime.Distiller                 // 上下文压缩纪要蒸馏器（nil=压缩降级纯截断）
	compactionTimeouts runtime.CompactionTimeouts        // 压缩链的时间预算
	toolTailBytes      int                               // 工具输出全局尾部钳制字节（0=不钳）
	toolWrap           func(registry.Tool) registry.Tool // 装配层注入的工具包装器（共享拦截链；nil=不包装）
}

// NewRouterJudge 构造 ReAct 评估者。
func NewRouterJudge(router *llm.Router, logger zerolog.Logger) *RouterJudge {
	return &RouterJudge{
		router: router,
		logger: logger.With().Str("component", "evaluator_judge").Logger(),
	}
}

// WithFunctionTools 注入 function_tools 白名单（链式）。白名单同时约束本包工具
// （repro_replay）与 extraTools——统一口径：不在名单上的工具不装配。
func (j *RouterJudge) WithFunctionTools(names []string) *RouterJudge {
	j.functionTools = names
	return j
}

// WithCLIManifest 注入 CLI 工具目录（cli_tools 白名单过滤后；nil 不渲染）。
func (j *RouterJudge) WithCLIManifest(m *manifest.ToolManifest) *RouterJudge {
	j.cliManifest = m
	return j
}

// WithSkills 注入 Tier 1 skill 索引（agent.skills 声明的 frontmatter）。评估者与
// executor 共用同一渲染（skill.RenderIndex）——两侧对"有哪些手册可读"认知一致。
func (j *RouterJudge) WithSkills(cards []*skill.Card) *RouterJudge {
	j.skills = cards
	return j
}

// WithCharter 注入角色章程（出厂值叠加用户自定义后的生效值）。
func (j *RouterJudge) WithCharter(charter string) *RouterJudge {
	j.charter = charter
	return j
}

// WithScenario 注入任务场景块（scenario.Render；空 = 无场景），拼在系统提示词末尾。
func (j *RouterJudge) WithScenario(block string) *RouterJudge {
	j.scenario = block
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

// WithContextRatio 注入上下文历史预算占比（系统配置 compaction 组；0=压缩器内置兜底）。
func (j *RouterJudge) WithContextRatio(ratio float64) *RouterJudge {
	j.contextRatio = ratio
	return j
}

// WithDistiller 注入上下文压缩纪要蒸馏器（nil=压缩降级纯截断）。
func (j *RouterJudge) WithDistiller(d runtime.Distiller) *RouterJudge {
	j.distiller = d
	return j
}

// WithCompactionTimeouts 注入压缩链的时间预算（runtime.CompactionTimeoutsFor 由单步 LLM 上限推导）。
func (j *RouterJudge) WithCompactionTimeouts(t runtime.CompactionTimeouts) *RouterJudge {
	j.compactionTimeouts = t
	return j
}

// WithToolOutputTailBytes 注入工具输出全局尾部钳制字节（0=不钳）。
func (j *RouterJudge) WithToolOutputTailBytes(n int) *RouterJudge {
	j.toolTailBytes = n
	return j
}

// WithToolWrapper 注入共享拦截链包装器（遥测/时间线）——评估者对目标的
// 主动动作（repro_replay 重放）与 executor 同一执行面，审计无盲区。
// nil=不包装（独立部署/测试）。
func (j *RouterJudge) WithToolWrapper(fn func(registry.Tool) registry.Tool) *RouterJudge {
	j.toolWrap = fn
	return j
}

// WithComplexity 注入 LLM 档位（agent 生效配置的 complexity；
// 空回退 medium）。
func (j *RouterJudge) WithComplexity(complexity string) *RouterJudge {
	j.complexity = llm.ParseComplexity(complexity)
	return j
}

// contextBudget 解析历史 token 预算：比例 × 本角色路由 provider 的 context_window。
// 比例未配置/读取失败返回 0——压缩器内置兜底预算接管（配置问题绝不阻断判定）。
func (j *RouterJudge) contextBudget(ctx context.Context) int {
	if j.contextRatio <= 0 {
		return 0
	}
	cw, err := j.router.ContextWindow(ctx, j.complexity)
	if err != nil {
		j.logger.Warn().Err(err).Msg("读取 context_window 失败，压缩预算用内置兜底")
		return 0
	}
	return int(float64(cw) * j.contextRatio)
}

// WithExtraTools 注入跨包工具实例（装配层按白名单经 tools.BuildTools 构造）。
func (j *RouterJudge) WithExtraTools(ts []registry.Tool) *RouterJudge {
	j.extraTools = append(j.extraTools, ts...)
	return j
}

// Judge 实现 LLMJudge：ReAct 循环内 LLM 自主调重放工具采证，FinalAnswer 为判定 JSON。
func (j *RouterJudge) Judge(
	ctx context.Context,
	hypothesis string,
	recipe, initialEvidence json.RawMessage,
	replay ReplayFunc,
) (Verdict, error) {
	// 一轮 = 一条假设的验证：本轮的工具过程事件与终稿带同一个轮次标识（评估者并行验证多条假设）
	ctx = reasoning.BeginRound(ctx)
	provider, err := j.router.For(ctx, j.complexity)
	if err != nil {
		return Verdict{}, fmt.Errorf("judge: 获取 provider: %w", err)
	}

	react := runtime.NewReActRuntime()
	if replay != nil && allowlist.Allows(j.functionTools, "repro_replay") {
		replayTool := registry.Tool(newReplayTool(replay))
		if j.toolWrap != nil {
			replayTool = j.toolWrap(replayTool)
		}
		if err := react.RegisterTool(replayTool); err != nil {
			return Verdict{}, fmt.Errorf("judge: 注册 repro_replay 失败: %w", err)
		}
	}
	for _, t := range j.extraTools {
		if allowlist.Allows(j.functionTools, t.Name()) {
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
		runtime.NewLoopDetectionMiddleware(12, 10), // 连续 12 次相同工具 / 10 次相似思考
		runtime.NewProgressTrackingMiddleware(20),  // 20 轮无新输出终止（连续失败止损由 runtime 内置，阈值随下下发）
	)

	var onThoughtDelta, onThinkingDelta func(string)
	if j.thoughts != nil {
		onThoughtDelta = j.thoughts.Delta
		onThinkingDelta = j.thoughts.ThinkingDelta
	}
	result, err := react.Run(ctx, &runtime.ReActConfig{
		Objective:                  j.buildObjective(hypothesis, recipe, initialEvidence),
		SystemPrompt:               j.systemPrompt(),
		LLMProvider:                provider,
		MaxIterations:              j.maxIt, // 迭代上限 = agent.max_iterations（配置即事实；0=不设限）
		MaxConsecutiveToolFailures: 12,      // 连续工具失败止损阈值（单机制：runtime 内置，评估链路容忍更高）
		OnThoughtDelta:             onThoughtDelta,
		OnThinkingDelta:            onThinkingDelta,
		MessageModifierChain:       runtime.NewCompactionChain(j.contextBudget(ctx), "evaluator", j.distiller, j.compactionTimeouts),
		ToolOutputTailBytes:        j.toolTailBytes,
		MiddlewareChain:            middlewareChain,
		// 判定终稿的结构化输出契约（与 executor/planner 同机制）：不设防时终稿
		// 是自由散文——验证结论写在散文里（"假设坐实 confirmed 0.99"）而
		// parseVerdict 只认 JSON，实测真实主张因输出格式漂移连败三次被误判
		// invalid_recipe 出圈。终稿按判定解析器验收，取不到判定 JSON 的散文终稿
		// 由运行时带着原因请模型重述。
		OutputFormatPrompt: "你是评估者，已完成取证，现在需要输出最终结论。",
		OutputFormatSchema: evaluatorcontract.Text, // 收尾时重申输出契约（唯一一份）
		OutputCheck:        verdictOutputCheck,
	})
	if err != nil {
		return Verdict{}, fmt.Errorf("judge: ReAct 失败: %w", err)
	}
	// 终态叙述在解析边界完成：判定协议转人读摘要作正文（谁解析谁叙述，
	// 前端不解释协议；协议原文由审计面保存）；解析失败时正文回退原文，终态帧照落。
	v, verr := parseVerdict(result.FinalAnswer)
	round := reasoning.RoundOf(result)
	if verr == nil {
		round.Summary = verdictNarrative(v)
	}
	if j.thoughts != nil {
		j.thoughts.Done(ctx, round)
	}
	return v, verr
}

// verdictNarrative 把判定协议转成时间线人读叙述：结论 + 置信度 + 理由。
func verdictNarrative(v Verdict) string {
	ruling := v.Verdict
	switch v.Verdict {
	case "confirmed":
		ruling = "坐实"
	case "refuted":
		ruling = "证伪"
	}
	head := fmt.Sprintf("评估结论：%s（置信度 %.0f%%）", ruling, v.Confidence*100)
	if v.Reasoning == "" {
		return head
	}
	return head + "\n\n理由：" + v.Reasoning
}

// buildObjective 组装判定输入：只陈述被验证的事实——假设陈述、复现配方与机器预跑证据。
// 怎样取证、证据怎样读、怎样判由章程与判定契约说明。
func (j *RouterJudge) buildObjective(hypothesis string, recipe, initialEvidence json.RawMessage) string {
	var sb strings.Builder
	sb.WriteString("## 待验证的假设\n")
	if hypothesis != "" {
		sb.WriteString(hypothesis)
	} else {
		sb.WriteString("（陈述缺失，从配方与证据推断）")
	}
	sb.WriteString("\n\n## 复现配方\n")
	sb.WriteString(stringOrEmpty(recipe))
	sb.WriteString("\n\n## 机器预跑证据\n")
	sb.WriteString(stringOrEmpty(initialEvidence))
	return sb.String()
}

// systemPrompt 组装评估者 system prompt：
//
//	章程（可自定义：评估准则、验证流程）
//	+ 契约（evaluator/contract，与 parseVerdict 同改同测，不可自定义：重放通道语义、判定 JSON）
//	+ skill 索引（有声明且 skill_read 在白名单时渲染）
//	+ CLI 目录（有 CLI 白名单时渲染，供独立复核经 command_run 调用）
//	+ 任务场景块（scenario.Render，任务建立时快照；无场景时没有）
//
// 章程为空时返回空串让 ReAct.Run 报错——自定义层在写入时已拒绝空章程，此处只是兜底。
func (j *RouterJudge) systemPrompt() string {
	charter := strings.TrimSpace(j.charter)
	if charter == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(charter)
	sb.WriteString("\n\n")
	sb.WriteString(evaluatorcontract.Text)
	// Tier 1 与 executor 同口径：skill_read 在白名单里才宣传（宣传=事实）。
	if len(j.skills) > 0 && allowlist.Allows(j.functionTools, "skill_read") {
		if idx := skill.RenderIndex(j.skills); idx != "" {
			sb.WriteString("\n")
			sb.WriteString(idx)
		}
	}
	if j.cliManifest != nil && len(j.cliManifest.Tools) > 0 {
		sb.WriteString("\n**沙箱 CLI 工具**（经 command_run 调用，独立复核取证可用）:\n")
		for _, t := range j.cliManifest.Tools {
			sb.WriteString("- " + t.Name + ": " + t.Description + "\n")
		}
	}
	return scenario.Append(sb.String(), j.scenario)
}

// verdictJSON 是判定契约的形状：verdict 的取值合法与否见 verdictOutputCheck。
type verdictJSON struct {
	Verdict      string          `json:"verdict"`
	Confidence   float64         `json:"confidence"`
	Reasoning    string          `json:"reasoning"`
	RefuteReason string          `json:"refute_reason"`
	Fingerprint  json.RawMessage `json:"finding"`
}

// verdictOutputCheck 是判定终稿的验收：终稿里取得到的 JSON 必须带合法的 verdict（confirmed /
// refuted）。模型在 JSON 前后写的说明不影响验收；运行时据它决定终稿是原样交付，还是带着
// 原因请模型重述。它同时是解析的前置。
func verdictOutputCheck(content string) error {
	var v verdictJSON
	if err := llm.ParseJSON(content, &v); err != nil {
		return fmt.Errorf("取不出判定 JSON: %w", err)
	}
	switch v.Verdict {
	case VerdictConfirmed, VerdictRefuted:
		return nil
	}
	return fmt.Errorf("verdict %q 不是 %s / %s", v.Verdict, VerdictConfirmed, VerdictRefuted)
}

// parseVerdict 从终稿提取判定（含坐实发现的结构化指纹）；验收不过时带上原文片段报错。
func parseVerdict(content string) (Verdict, error) {
	if err := verdictOutputCheck(content); err != nil {
		return Verdict{}, fmt.Errorf("judge: %w (输出: %.200s)", err, content)
	}
	var v verdictJSON
	if err := llm.ParseJSON(content, &v); err != nil {
		return Verdict{}, fmt.Errorf("judge: %w (输出: %.200s)", err, content)
	}
	// 两个结构的字段一一对应：验收已经判过 verdict 的取值。
	return Verdict(v), nil
}

func stringOrEmpty(b json.RawMessage) string {
	if len(b) == 0 {
		return "（无）"
	}
	return string(b)
}

// ─── repro_replay 工具 ────────────────────────────────────────────

// replayTool 把 ReplayFunc 包成 registry.Tool（绑定本 Claim 的配方，LLM 可多次调用取证）。
type replayTool struct {
	registry.BaseTool
	replay ReplayFunc
}

func newReplayTool(replay ReplayFunc) *replayTool {
	t := &replayTool{replay: replay}
	t.WithTimeout(60 * time.Second)
	return t
}

func (t *replayTool) Name() string      { return "repro_replay" }
func (t *replayTool) ShortDesc() string { return "重放复现配方取得机器证据" }
func (t *replayTool) Desc() string {
	return "按本假设的复现配方执行一次机器复现，返回采集到的证据与断言逐条判定。可多次调用。"
}
func (t *replayTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"description":"无参数——重放本假设的既定配方"}`)
}

func (t *replayTool) Execute(ctx context.Context, _ json.RawMessage) (registry.ToolResult, error) {
	res, err := t.replay(ctx)
	if err != nil {
		return registry.ToolResult{Error: "重放失败: " + err.Error()}, nil
	}
	ev := res.Evidence
	if len(ev) == 0 {
		ev = json.RawMessage("{}")
	}
	out, _ := json.Marshal(map[string]interface{}{
		"note":        "assert_passed / assert_reasons 是配方断言的机器判定（参考，非结论）；坐实与否由你判定",
		"evidence":    ev,
		"duration_ms": res.DurationMs,
	})
	return registry.ToolResult{Output: string(out)}, nil
}
