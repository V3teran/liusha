package probe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/heuristic"
	"github.com/V3teran/liusha/internal/replay"
	"github.com/V3teran/liusha/internal/toolfx"
)

// 阈值默认（可由 LLM 通过参数覆盖）。
//   - min_threshold：低阈，区分"几乎不像 baseline"和"略有重叠"。
//   - high_threshold：高阈，区分"高度像 baseline"和"模糊重叠"。
//   - lengthRatioGate：长度差距过大（< 0.3）时跳过 JSON/Jaccard 计算（5xx 错误页 vs 数据页一般差 10x）。
const (
	defaultMinThreshold  = 0.6
	defaultHighThreshold = 0.9
	lengthRatioGate      = 0.3
)

// ComputeSimilarity 是漏洞探针通用工具（agentic 路线：只输出 raw 分数，由 LLM 自决策）。
//
// 优先 baseline-centric 路径：
//   - 当 ProbeState 含原始抓包响应（LastFlow.ResponseBody）→ 以 _original_ 为锚点，
//     每个 replay 与 baseline 算一次 StructuralSimilarity，输出每对 score 让 LLM 自判。
//   - 当无原始响应 → 退回 N×N 两两比较，输出 score >= min_threshold 的可疑对让 LLM 自判。
//
// 工具不下"verdict 结论"——这是 agentic 路线核心：把数学客观值（相似度分数）交给 LLM，
// 让模型按 SKILL 决策树自己判断（如所有 replay 都不像 baseline = 无漏洞；admin 像 baseline
// 但其他都不像 = 强越权信号；所有 replay 都像 baseline = 公开接口可能等）。
type ComputeSimilarity struct {
	State *ProbeState
}

// Name 返回动作名 "compute_similarity"。
func (a *ComputeSimilarity) Name() string { return "compute_similarity" }

// Description 给 LLM 看的简介，强调"输出分数让 LLM 决策"的语义。
func (a *ComputeSimilarity) Description() string {
	return "对上一次 replay 的多身份响应算结构相似度，输出每对 score 让 LLM 自判。" +
		"优先走 baseline 模式（每个身份与原始抓包响应对比，输出 baseline_pairs[]）；" +
		"无原始响应时退回 inter_pairs 模式（输出 score >= min_threshold 的可疑两两对）。" +
		"工具不下结论——LLM 看 score 分布按 SKILL 决策树判越权 / 公开接口 / 无漏洞等。"
}

// ParametersJSON：min_threshold + high_threshold（双阈值，仅作为筛选 suspicious_pairs 的标尺）。
func (a *ComputeSimilarity) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties": {
    "min_threshold":{"type":"number","default":0.6,"minimum":0,"maximum":1,"description":"低阈：suspicious_pairs 仅含 score >= 此值的对（fallback 模式）"},
    "high_threshold":{"type":"number","default":0.9,"minimum":0,"maximum":1,"description":"高阈：summary.above_high_threshold 计数用"}
  }
}`)
}

// pairScore 是单个相似度对的评分。
//   - baseline 模式：A 固定 = "_original_"，B = replay 身份名
//   - fallback 模式：A、B 均为 replay 身份名
type pairScore struct {
	A           string  `json:"a"`
	B           string  `json:"b"`
	Score       float64 `json:"score"`
	LengthRatio float64 `json:"length_ratio"`
}

// summary 是统计摘要，让 LLM 一眼看清整体分布。
type summary struct {
	TotalPairs int     `json:"total_pairs"`
	MaxScore   float64 `json:"max_score"`
	MinScore   float64 `json:"min_score"`
	AboveHigh  int     `json:"above_high_threshold"`
	AboveMin   int     `json:"above_min_threshold"`
}

// similarityOutput 是 Result.Output 的统一结构。
//   - Mode = "baseline"：BaselinePairs 含每个 replay 与 _original_ 的对比；SuspiciousPairs 为空。
//   - Mode = "inter_pairs"：SuspiciousPairs 含 score>=min 的 replay 两两对；BaselinePairs 为空。
//
// 不再含 verdict 字段——LLM 看 baseline_pairs / suspicious_pairs / summary 自决策。
type similarityOutput struct {
	Algorithm       string      `json:"algorithm"`
	Mode            string      `json:"mode"`
	Baseline        string      `json:"baseline,omitempty"`
	MinThreshold    float64     `json:"min_threshold"`
	HighThreshold   float64     `json:"high_threshold"`
	Identities      []string    `json:"identities"`
	BaselinePairs   []pairScore `json:"baseline_pairs,omitempty"`
	SuspiciousPairs []pairScore `json:"suspicious_pairs,omitempty"`
	Summary         summary     `json:"summary"`
}

// Execute 解析 args → 选 baseline 模式或 fallback → 算分 → 返回结构化 score。
func (a *ComputeSimilarity) Execute(_ context.Context, args json.RawMessage) (toolfx.Result, error) {
	in, err := parseSimilarityArgs(args)
	if err != nil {
		return toolfx.Result{}, err
	}
	if len(a.State.LastResponses) == 0 {
		return toolfx.Result{}, fmt.Errorf("state.LastResponses 为空，请先调 replay_matrix")
	}

	all := a.State.AllResponses()
	if baselineIdx := findBaselineIdx(all); baselineIdx >= 0 {
		return computeBaselineMode(all, baselineIdx, in)
	}
	return computeInterPairsMode(a.State.LastResponses, in)
}

// similarityArgs 是 ParametersJSON 解析结果，独立类型方便测试。
type similarityArgs struct {
	MinThreshold  float64 `json:"min_threshold"`
	HighThreshold float64 `json:"high_threshold"`
}

func parseSimilarityArgs(args json.RawMessage) (similarityArgs, error) {
	var in similarityArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return in, fmt.Errorf("解析 compute_similarity 参数失败: %w", err)
		}
	}
	if in.MinThreshold <= 0 {
		in.MinThreshold = defaultMinThreshold
	}
	if in.HighThreshold <= 0 {
		in.HighThreshold = defaultHighThreshold
	}
	if in.HighThreshold < in.MinThreshold {
		return in, fmt.Errorf("high_threshold (%v) 不能小于 min_threshold (%v)", in.HighThreshold, in.MinThreshold)
	}
	return in, nil
}

// findBaselineIdx 在响应列表里找到 _original_ 锚点的下标，未找到返回 -1。
func findBaselineIdx(rs []replay.Response) int {
	for i := range rs {
		if rs[i].IdentityName == replay.OriginalIdentityName {
			return i
		}
	}
	return -1
}

// computeBaselineMode 是 baseline-centric 主路径：每个非锚点响应与 baseline 算一次相似度。
func computeBaselineMode(all []replay.Response, baselineIdx int, in similarityArgs) (toolfx.Result, error) {
	baseline := all[baselineIdx]
	replays := make([]replay.Response, 0, len(all)-1)
	for i, r := range all {
		if i != baselineIdx {
			replays = append(replays, r)
		}
	}

	identities := make([]string, len(replays))
	for i := range replays {
		identities[i] = replays[i].IdentityName
	}

	out := similarityOutput{
		Algorithm:     "structural_v2",
		Mode:          "baseline",
		Baseline:      baseline.IdentityName,
		MinThreshold:  in.MinThreshold,
		HighThreshold: in.HighThreshold,
		Identities:    identities,
		BaselinePairs: []pairScore{},
	}

	// 没有 replay → 无对比；空摘要返回，LLM 看到 BaselinePairs=[] 自己判定。
	if len(replays) == 0 {
		out.Summary = summary{}
		return marshalResult(out, 1)
	}

	maxScore := -1.0
	minScore := 2.0
	aboveHigh := 0
	aboveMin := 0
	for _, r := range replays {
		la, lb := len(baseline.Body), len(r.Body)
		lr := lengthRatio(la, lb)
		var score float64
		if lr < lengthRatioGate {
			score = 0
		} else {
			score = heuristic.StructuralSimilarity(string(baseline.Body), string(r.Body))
		}
		if score > maxScore {
			maxScore = score
		}
		if score < minScore {
			minScore = score
		}
		if score >= in.HighThreshold {
			aboveHigh++
		}
		if score >= in.MinThreshold {
			aboveMin++
		}
		out.BaselinePairs = append(out.BaselinePairs, pairScore{
			A:           baseline.IdentityName,
			B:           r.IdentityName,
			Score:       score,
			LengthRatio: lr,
		})
	}

	out.Summary = summary{
		TotalPairs: len(replays),
		MaxScore:   maxScore,
		MinScore:   minScore,
		AboveHigh:  aboveHigh,
		AboveMin:   aboveMin,
	}
	return marshalResult(out, len(all))
}

// computeInterPairsMode 是无 baseline 时的 fallback：N×N 两两比较，仅输出 score >= min 的对。
func computeInterPairsMode(rs []replay.Response, in similarityArgs) (toolfx.Result, error) {
	n := len(rs)
	identities := make([]string, n)
	for i := range rs {
		identities[i] = rs[i].IdentityName
	}

	out := similarityOutput{
		Algorithm:       "structural_v2",
		Mode:            "inter_pairs",
		MinThreshold:    in.MinThreshold,
		HighThreshold:   in.HighThreshold,
		Identities:      identities,
		SuspiciousPairs: []pairScore{},
	}

	if n < 2 {
		out.Summary = summary{MinScore: 1.0}
		return marshalResult(out, n)
	}

	maxScore := -1.0
	minScore := 2.0
	aboveHigh := 0
	aboveMin := 0
	totalPairs := 0
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			totalPairs++
			la, lb := len(rs[i].Body), len(rs[j].Body)
			lr := lengthRatio(la, lb)
			var score float64
			if lr < lengthRatioGate {
				score = 0
			} else {
				score = heuristic.StructuralSimilarity(string(rs[i].Body), string(rs[j].Body))
			}
			if score > maxScore {
				maxScore = score
			}
			if score < minScore {
				minScore = score
			}
			if score >= in.HighThreshold {
				aboveHigh++
			}
			if score >= in.MinThreshold {
				aboveMin++
				out.SuspiciousPairs = append(out.SuspiciousPairs, pairScore{
					A:           rs[i].IdentityName,
					B:           rs[j].IdentityName,
					Score:       score,
					LengthRatio: lr,
				})
			}
		}
	}

	out.Summary = summary{
		TotalPairs: totalPairs,
		MaxScore:   maxScore,
		MinScore:   minScore,
		AboveHigh:  aboveHigh,
		AboveMin:   aboveMin,
	}
	return marshalResult(out, n)
}

// marshalResult 序列化输出 + 拼一行 Summary 给 react/log 看。
func marshalResult(out similarityOutput, n int) (toolfx.Result, error) {
	enc, err := json.Marshal(out)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("序列化 compute_similarity 输出失败: %w", err)
	}
	return toolfx.Result{
		Output: enc,
		Summary: fmt.Sprintf(
			"compute_similarity mode=%s n=%d max=%.2f above_min=%d above_high=%d",
			out.Mode, n, out.Summary.MaxScore, out.Summary.AboveMin, out.Summary.AboveHigh,
		),
	}, nil
}

// lengthRatio 返回 min(|a|,|b|) / max(|a|,|b|)，两端都为 0 时视为完全相同。
func lengthRatio(a, b int) float64 {
	if a == 0 && b == 0 {
		return 1.0
	}
	if a == 0 || b == 0 {
		return 0
	}
	if a < b {
		return float64(a) / float64(b)
	}
	return float64(b) / float64(a)
}
