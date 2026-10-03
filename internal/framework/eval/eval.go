// Package eval 提供 Agent 执行轨迹的评估框架（通用 ADK 能力，域无关）。
//
// 两个评估器维度正交，可组合：
//   - RuleEvaluator：确定性指标（迭代数/工具成功率/终止状态）——零成本、可回归
//   - LLMJudge：语义评估（目标达成度/轨迹质量）——LLM-as-judge，需注入 Provider
//
// 评估对象是 ReActResult.Trace（框架原生轨迹），业务侧在任意边界（单 Action 后/
// 任务收尾/离线批量）调用，结果不参与运行时决策——eval 是旁路观测，不是控制流。
package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// TraceInput 是一次评估的输入：目标 + 执行轨迹（来自 runtime.ReActResult）。
type TraceInput struct {
	Objective string
	// Iterations 轨迹迭代数；Actions 工具调用总数（含失败）；Failures 失败数
	Iterations, Actions, Failures int
	// Status 终止状态（success/max_iterations/error/cancelled）
	Status string
	// FinalAnswer 最终回答（可空）
	FinalAnswer string
	// ToolNames 命中过的工具名集合（去重，供 LLM judge 引用）
	ToolNames []string
}

// EvalResult 是评估产出。
type EvalResult struct {
	Score   float64 // 0-1 综合分（各维度加权）
	Passed  bool    // 达到门槛
	Reasons []string
	Details map[string]float64 // 分维度得分（rule/judge）
}

// Evaluator 是评估器统一接口。
type Evaluator interface {
	Evaluate(ctx context.Context, in TraceInput) (EvalResult, error)
	Name() string
}

// Thresholds 是 RuleEvaluator 的门槛参数（零值有安全默认）。
type Thresholds struct {
	MaxIterations int     // 迭代超此数扣分（默认 20）
	MinToolRate   float64 // 工具成功率下限（默认 0.5）
	PassScore     float64 // 综合分及格线（默认 0.6）
}

func (t Thresholds) withDefaults() Thresholds {
	if t.MaxIterations <= 0 {
		t.MaxIterations = 20
	}
	if t.MinToolRate <= 0 {
		t.MinToolRate = 0.5
	}
	if t.PassScore <= 0 {
		t.PassScore = 0.6
	}
	return t
}

// RuleEvaluator 确定性规则评估：终止状态 + 迭代效率 + 工具成功率三维度加权。
type RuleEvaluator struct {
	th Thresholds
}

// NewRuleEvaluator 构造规则评估器。
func NewRuleEvaluator(th Thresholds) *RuleEvaluator { return &RuleEvaluator{th: th.withDefaults()} }

// Name 实现 Evaluator。
func (e *RuleEvaluator) Name() string { return "rule" }

// Evaluate 实现 Evaluator：三维度加权（状态 0.5 / 迭代 0.2 / 工具 0.3）。
func (e *RuleEvaluator) Evaluate(_ context.Context, in TraceInput) (EvalResult, error) {
	res := EvalResult{Details: map[string]float64{}}

	// 维度1：终止状态
	switch in.Status {
	case "success":
		res.Details["status"] = 1
	case "max_iterations":
		res.Details["status"] = 0.4
		res.Reasons = append(res.Reasons, "达到最大迭代数（未自然收敛）")
	default:
		res.Details["status"] = 0
		res.Reasons = append(res.Reasons, fmt.Sprintf("异常终止: %s", in.Status))
	}

	// 维度2：迭代效率
	iter := float64(in.Iterations) / float64(e.th.MaxIterations)
	if iter > 1 {
		iter = 1
	}
	res.Details["iterations"] = 1 - iter // 越少越好

	// 维度3：工具成功率
	toolRate := 1.0
	if in.Actions > 0 {
		toolRate = float64(in.Actions-in.Failures) / float64(in.Actions)
	}
	if toolRate < e.th.MinToolRate {
		res.Reasons = append(res.Reasons, fmt.Sprintf("工具成功率 %.0f%% 低于门槛 %.0f%%", toolRate*100, e.th.MinToolRate*100))
	}
	res.Details["tools"] = toolRate

	res.Score = 0.5*res.Details["status"] + 0.2*res.Details["iterations"] + 0.3*res.Details["tools"]
	res.Passed = res.Score >= e.th.PassScore
	if res.Passed {
		res.Reasons = append(res.Reasons, fmt.Sprintf("综合分 %.2f ≥ 及格线 %.2f", res.Score, e.th.PassScore))
	} else {
		res.Reasons = append(res.Reasons, fmt.Sprintf("综合分 %.2f < 及格线 %.2f", res.Score, e.th.PassScore))
	}
	return res, nil
}

// JudgeProvider 是 LLM judge 依赖的最小 Provider 子集（由 framework llm.Provider 满足）。
type JudgeProvider interface {
	Complete(ctx context.Context, req JudgeRequest) (JudgeResponse, error)
}

// JudgeRequest / JudgeResponse 解耦 llm 包类型，eval 包自包含。
type JudgeRequest struct{ Prompt string }
type JudgeResponse struct{ Content string }

// LLMJudge 语义评估：目标达成度与轨迹质量的 LLM-as-judge。
type LLMJudge struct {
	provider JudgeProvider
}

// NewLLMJudge 构造（provider 为 nil 时 Evaluate 报错——语义评估无降级路径）。
func NewLLMJudge(provider JudgeProvider) *LLMJudge { return &LLMJudge{provider: provider} }

// Name 实现 Evaluator。
func (j *LLMJudge) Name() string { return "llm_judge" }

// Evaluate 实现 Evaluator：单次结构化裁决 {score, passed, reason}。
func (j *LLMJudge) Evaluate(ctx context.Context, in TraceInput) (EvalResult, error) {
	if j.provider == nil {
		return EvalResult{}, fmt.Errorf("eval: LLMJudge 无 provider")
	}
	var sb strings.Builder
	sb.WriteString("评估一次 Agent 执行是否达成目标。只输出 JSON：{\"score\": 0-1, \"passed\": true|false, \"reason\": \"一句话\"}\n\n")
	sb.WriteString("## 目标\n")
	sb.WriteString(in.Objective)
	sb.WriteString(fmt.Sprintf("\n\n## 轨迹统计\n迭代 %d 次，工具调用 %d 次（失败 %d），终止状态 %s\n", in.Iterations, in.Actions, in.Failures, in.Status))
	sb.WriteString("## 最终回答\n")
	if in.FinalAnswer != "" {
		sb.WriteString(in.FinalAnswer)
	} else {
		sb.WriteString("（空）")
	}
	resp, err := j.provider.Complete(ctx, JudgeRequest{Prompt: sb.String()})
	if err != nil {
		return EvalResult{}, fmt.Errorf("eval: judge 调用失败: %w", err)
	}
	return parseJudgeJSON(resp.Content)
}

// parseJudgeJSON 从 LLM 输出提取 JSON 裁决（容忍 ```json 包裹与前后噪声）。
func parseJudgeJSON(content string) (EvalResult, error) {
	s := strings.Index(content, "{")
	e := strings.LastIndex(content, "}")
	if s == -1 || e <= s {
		return EvalResult{}, fmt.Errorf("eval: judge 输出无 JSON: %.100s", content)
	}
	var v struct {
		Score  float64 `json:"score"`
		Passed bool    `json:"passed"`
		Reason string  `json:"reason"`
	}
	if err := json.Unmarshal([]byte(content[s:e+1]), &v); err != nil {
		return EvalResult{}, fmt.Errorf("eval: judge JSON 解析失败: %w", err)
	}
	return EvalResult{
		Score:   v.Score,
		Passed:  v.Passed,
		Reasons: []string{v.Reason},
		Details: map[string]float64{"judge": v.Score},
	}, nil
}

// Composite 组合多个评估器，加权聚合（权重按传入顺序一一对应；总分归一化）。
type Composite struct {
	evaluators []Evaluator
	weights    []float64
}

// NewComposite 构造组合评估器（len(evals)==len(weights)，否则报错）。
func NewComposite(evals []Evaluator, weights []float64) (*Composite, error) {
	if len(evals) == 0 || len(evals) != len(weights) {
		return nil, fmt.Errorf("eval: 组合评估器参数不齐")
	}
	return &Composite{evaluators: evals, weights: weights}, nil
}

// Name 实现 Evaluator。
func (c *Composite) Name() string { return "composite" }

// Evaluate 实现 Evaluator：并发跑子评估器，加权平均。
func (c *Composite) Evaluate(ctx context.Context, in TraceInput) (EvalResult, error) {
	type item struct {
		res EvalResult
		err error
	}
	out := make([]item, len(c.evaluators))
	var wg sync.WaitGroup
	for i, ev := range c.evaluators {
		wg.Add(1)
		go func(i int, ev Evaluator) {
			defer wg.Done()
			r, err := ev.Evaluate(ctx, in)
			out[i] = item{res: r, err: err}
		}(i, ev)
	}
	wg.Wait()

	var total, wsum float64
	res := EvalResult{Details: map[string]float64{}}
	for i, it := range out {
		if it.err != nil {
			return EvalResult{}, fmt.Errorf("eval: 子评估器 %s 失败: %w", c.evaluators[i].Name(), it.err)
		}
		total += it.res.Score * c.weights[i]
		wsum += c.weights[i]
		res.Details[c.evaluators[i].Name()] = it.res.Score
		res.Reasons = append(res.Reasons, it.res.Reasons...)
	}
	res.Score = total / wsum
	res.Passed = res.Score >= 0.6
	return res, nil
}
