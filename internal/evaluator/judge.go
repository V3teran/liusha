// judge.go — LLM 语义裁决器（ReAct 形态）。
//
// 分工铁律：复现门裁决官经 replay_for_verification 工具自主重放取证、
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

// WithExtraTools 注入跨包工具实例（装配层按白名单经 tools.BuildTools 构造）。
func (j *RouterJudge) WithExtraTools(ts []registry.Tool) *RouterJudge {
	j.extraTools = append(j.extraTools, ts...)
	return j
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
		MaxIterations:        8, // 自主差分实验需要轮次：复核预跑 + 基线/攻击各放 + 对比裁决
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

// systemPrompt 基础裁决指令 + CLI 目录（有 CLI 白名单时渲染，供独立复核经 run_command 调用）。
func (j *RouterJudge) systemPrompt() string {
	s := judgeSystemPrompt
	if j.cliManifest != nil && len(j.cliManifest.Tools) > 0 {
		s += "\n**沙箱 CLI 工具**（经 run_command 调用，独立复核取证可用）:\n"
		for _, t := range j.cliManifest.Tools {
			s += "- " + t.Name + ": " + t.Description + "\n"
		}
	}
	return s
}

const judgeSystemPrompt = `你是渗透测试结果的质量裁决官（Agent-as-a-Judge：你有工具，自主设计验证实验）。

**工作方式（自主复核）**：
1. 先调 replay_for_verification 重放复现配方（域信封：domain + recipe + assert），取得机器证据（断言明细 + 响应快照）
2. 证据标注 domain=generic 时无机器重放——用 run_command 按 recipe 的步骤自主执行，基于命令输出裁决
3. 不轻信断言命中——自己二次判断：
   a. 证据含 baseline_* 字段时对比基线 vs 攻击（attack_*）：一致 → refuted（无差分即无证据）
   b. 断言特征是否页面常态？（200、登录页标题、静态文案）→ 无鉴别力即 refuted
   c. 时间盲证据看 attack_duration_ms 是否真实显著延迟
   d. 需要独立取证时用 run_command（curl 重放配方 request、正常参数对照实验）
4. 核验因果关联（硬规则）：坐实的必要条件是攻击响应出现正常请求没有的特征、且由配方 payload 导致——
   - 特征在正常响应也出现 → refuted（断言无鉴别力）
   - 攻击响应出现基线没有的报错回显/泄露数据/显著延迟 → 可 confirmed
5. 证据不足以判断时给 refuted（宁可保守）
6. 裁决后停止调用工具，**只输出一个 JSON 对象**：
{"verdict": "confirmed|refuted", "confidence": 0.0-1.0, "reasoning": "一句话裁决理由（引用你亲见的差分）"}`

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
