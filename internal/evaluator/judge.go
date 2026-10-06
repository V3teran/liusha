// Package evaluator 实现验证层：复现晋升门 + LLM 语义裁决器（ReAct 形态）。
//
// judge.go 职责：分工铁律——复现门裁决官经 replay_for_verification 工具自主重放取证、
// 按需多次验证，最终输出结构化裁决。断言命中 ≠ 漏洞成立（子串可能来自页面
// 自身文案），语义终裁交给 LLM——这是晋升门的最后一道闸。
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
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/tools/manifest"
)

// ReplayFunc 是裁决官可调用的机器重放（由 PromotionEvaluator 注入，绑定本 Attempt 配方）。
type ReplayFunc func(ctx context.Context) (Result, error)

// LLMJudge 是晋升门的语义裁决器接口（ReAct 实现 + 测试 stub）。
type LLMJudge interface {
	Judge(ctx context.Context, hypothesis string, recipe, initialEvidence json.RawMessage, replay ReplayFunc) (verdict, reasoning string, err error)
}

// RouterJudge 经 ReAct runtime 跑语义裁决：工具（replay_for_verification）+ 终裁 JSON。
type RouterJudge struct {
	router        *llm.Router
	logger        zerolog.Logger
	functionTools []string           // function_tools 白名单（nil=全量；空=空集）
	extraTools    []registry.Tool    // 装配层注入的额外工具（run_command 等白名单工具——跨包工具由 cognition 构造）
	cliManifest   *manifest.Manifest // CLI 工具目录（cli_tools 过滤后；渲染进 SystemPrompt 供 run_command 调用）
	skills        []*skill.Card      // Tier 1 skill 索引（agent.skills 声明；与 executor 同口径渲染）
	charter       string             // 角色章程（agent.system_prompt，运维可调；空=不渲染）
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

// WithMaxIterations 注入 ReAct 迭代上限（agent.max_iterations；0=不设限，
// 默认基线 8 生效）。上限语义：只能收紧不能放宽。
func (j *RouterJudge) WithMaxIterations(n int) *RouterJudge {
	j.maxIt = n
	return j
}

// WithExtraTools 注入跨包工具实例（装配层按白名单经 tools.BuildTools 构造）。
func (j *RouterJudge) WithExtraTools(ts []registry.Tool) *RouterJudge {
	j.extraTools = append(j.extraTools, ts...)
	return j
}

// capIterations 基线 8 与 agent.max_iterations 上限取小（0=不设限）。
func (j *RouterJudge) capIterations(base int) int {
	if j.maxIt > 0 && base > j.maxIt {
		return j.maxIt
	}
	return base
}

func (j *RouterJudge) allows(name string) bool {
	if j.functionTools == nil {
		return true
	}
	for _, n := range j.functionTools {
		if n == name {
			return true
		}
	}
	return false
}

// Judge 实现 LLMJudge：ReAct 循环内 LLM 自主调重放工具采证，FinalAnswer 为裁决 JSON。
func (j *RouterJudge) Judge(
	ctx context.Context,
	hypothesis string,
	recipe, initialEvidence json.RawMessage,
	replay ReplayFunc,
) (string, string, error) {
	provider, err := j.router.For(ctx, llm.ComplexityComplex)
	if err != nil {
		return "", "", fmt.Errorf("judge: 获取 provider: %w", err)
	}

	react := runtime.NewReActRuntime()
	if replay != nil && j.allows("replay_for_verification") {
		_ = react.RegisterTool(newReplayTool(replay))
	}
	for _, t := range j.extraTools {
		if j.allows(t.Name()) {
			_ = react.RegisterTool(t)
		}
	}

	ctx = llm.WithCallMeta(ctx, llm.CallMeta{Role: "evaluator"})

	result, err := react.Run(ctx, &runtime.ReActConfig{
		Objective:            j.buildObjective(hypothesis, recipe, initialEvidence),
		SystemPrompt:         j.systemPrompt(),
		LLMProvider:          provider,
		MaxIterations:        j.capIterations(8), // 自主差分实验需要轮次：复核预跑 + 基线/攻击各放 + 对比裁决
		MaxTokens:            1500,
		MessageModifierChain: runtime.NewDefaultModifierChain(10),
	})
	if err != nil {
		return "", "", fmt.Errorf("judge: ReAct 失败: %w", err)
	}
	return parseVerdict(result.FinalAnswer)
}

// buildObjective 组装裁决输入（假设 + 自包含配方 + 预跑对照摘要）。
func (j *RouterJudge) buildObjective(hypothesis string, recipe, initialEvidence json.RawMessage) string {
	var sb strings.Builder
	sb.WriteString("裁决以下漏洞假设是否坐实。\n\n## 假设\n")
	if hypothesis != "" {
		sb.WriteString(hypothesis)
	} else {
		sb.WriteString("（陈述缺失，从配方与证据推断）")
	}
	sb.WriteString("\n\n## 复现配方（自包含完整 HTTP 请求）\n")
	sb.WriteString(stringOrEmpty(recipe))
	sb.WriteString("\n\n## 配方预跑证据（机器采集，参考——须自主二次验证）\n")
	sb.WriteString(stringOrEmpty(initialEvidence))
	sb.WriteString("\n\n先用 replay_for_verification 复核机器证据；证据含 baseline_* 字段时对比基线/攻击差分（一致即无差分）；")
	sb.WriteString("证据标注 domain=generic（无机器重放通道）时改用 run_command 按 recipe 步骤自主执行取证；")
	sb.WriteString("需要独立取证时用 run_command（如 curl 重放配方 request、用正常参数做对照）。基于你亲见的证据裁决。")
	return sb.String()
}

// systemPrompt 组装裁决官 system prompt：
//
//	角色章程（agent.system_prompt——DB 事实源，种子 = agents/evaluator.md 正文，前端可调）
//	+ skill 索引（有声明且 read_skill 在白名单时渲染）
//	+ CLI 目录（有 CLI 白名单时渲染，供独立复核经 run_command 调用）
//
// 机制契约（replay 工作方式、裁决 JSON 格式、因果硬规则）由章程承载——
// 改契约 = 改 agents/evaluator.md + make reseed；正文为空时单句兜底防失能。
func (j *RouterJudge) systemPrompt() string {
	var sb strings.Builder
	if c := strings.TrimSpace(j.charter); c != "" {
		sb.WriteString(c)
	} else {
		sb.WriteString("你是渗透测试结果的质量裁决官：先用 replay_for_verification 复核机器证据（domain=generic 时改用 run_command 自主取证），对比基线/攻击差分后自主裁决，宁可保守。裁决后只输出一个 JSON：{\"verdict\": \"confirmed|refuted\", \"confidence\": 0.0-1.0, \"reasoning\": \"...\"}")
	}
	// Tier 1 与 executor 同口径：read_skill 在白名单里才宣传（宣传=事实）。
	if len(j.skills) > 0 && j.allows("read_skill") {
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

// parseVerdict 从 FinalAnswer 提取裁决 JSON。
func parseVerdict(content string) (string, string, error) {
	if content == "" {
		return "", "", fmt.Errorf("judge: 裁决输出为空")
	}
	s := strings.Index(content, "{")
	e := strings.LastIndex(content, "}")
	if s == -1 || e <= s {
		return "", "", fmt.Errorf("judge: 输出无 JSON: %.200s", content)
	}
	var v struct {
		Verdict    string  `json:"verdict"`
		Confidence float64 `json:"confidence"`
		Reasoning  string  `json:"reasoning"`
	}
	if err := json.Unmarshal([]byte(content[s:e+1]), &v); err != nil {
		return "", "", fmt.Errorf("judge: 解析裁决失败: %w", err)
	}
	switch v.Verdict {
	case VerdictConfirmed, VerdictRefuted:
		return v.Verdict, v.Reasoning, nil
	default:
		return "", "", fmt.Errorf("judge: 未知 verdict %q", v.Verdict)
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
	t.WithTimeout(60 * time.Second).WithConcurrencySafe(false) // 重放串行
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
