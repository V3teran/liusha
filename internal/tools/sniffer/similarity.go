package sniffer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/heuristic"
	"github.com/V3teran/liusha/internal/tool"
)

// 阈值默认（可由 LLM 通过参数覆盖）。
//   - min_threshold：低阈。所有 pair 低于此值 → verdict=all_below_threshold（无越权信号，可短路）。
//   - high_threshold：高阈。任一 pair 不低于此值 → verdict=high_similarity_pair（疑似越权，需 LLM 判定真假阳）。
//   - 中间区 [min, high) → verdict=ambiguous（让 LLM 看具体分值）。
//   - length_ratio_gate：先看响应长度比，差距过大直接判 score=0（省 Jaccard 计算）。
const (
	defaultMinThreshold  = 0.6
	defaultHighThreshold = 0.9
	lengthRatioGate      = 0.3
)

// verdict 三态：
//   - all_below_threshold：所有 pair 都低于 min_threshold → 工具层判定无越权，可直接 done(all_similar)。
//   - high_similarity_pair：至少一个 pair >= high_threshold → 疑似越权，但需 LLM 排除假阳性
//     （公开接口 /banner /health；错误页；登录页等同样会高相似）。
//   - ambiguous：所有命中都在 [min, high) 区间 → LLM 看 suspicious_pairs 分值判定。
const (
	verdictAllBelow  = "all_below_threshold"
	verdictHighSim   = "high_similarity_pair"
	verdictAmbiguous = "ambiguous"
)

// ComputeSimilarity — 漏洞探针通用工具：对 Session.LastResponses 两两算 token Jaccard 相似度。
//
// 与上一版关键差异（本轮重写）：
//   - **不再返回 N×N 矩阵交给 LLM 解读**：算法直接产出 verdict（三态），LLM 只看结论 + 可疑对。
//     这样能用算法干掉确定的负例（节省 LLM 调用），保留可疑正例让 LLM 二次确认（避免假阳性）。
//   - **加 length-ratio 短路**：长度差距 > 3.3x 的 pair 直接判 score=0，跳过 Jaccard 计算
//     （5xx 错误页 vs 数据页一般差 10x，无需算具体相似度）。
//   - **suspicious_pairs 替代矩阵**：仅返回 score >= min_threshold 的 pair，附带 length_ratio。
type ComputeSimilarity struct {
	Session *Session
}

// Name 返回动作名 "compute_similarity"。
func (a *ComputeSimilarity) Name() string { return "compute_similarity" }

// Description 给 LLM 看的简介，强调"verdict 直接定结论"的语义。
func (a *ComputeSimilarity) Description() string {
	return "对上一次 replay 的多身份响应两两算 token Jaccard 相似度，直接产出 verdict：" +
		"all_below_threshold（所有 pair 低于低阈，工具层判定无越权，可 done(all_similar)）；" +
		"high_similarity_pair（任一 pair 高于高阈，疑似越权，但需 LLM 排除公开接口/错误页等假阳性）；" +
		"ambiguous（在中间区，LLM 看 suspicious_pairs 具体分值判定）。" +
		"返回 suspicious_pairs（score >= min_threshold 的身份对）+ summary，不返回 N×N 矩阵。"
}

// ParametersJSON：min_threshold + high_threshold（双阈值，verdict 三态语义）。
func (a *ComputeSimilarity) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties": {
    "min_threshold":{"type":"number","default":0.6,"minimum":0,"maximum":1,"description":"低阈：所有 pair 低于此值 → verdict=all_below_threshold（无越权信号，可短路 done）"},
    "high_threshold":{"type":"number","default":0.9,"minimum":0,"maximum":1,"description":"高阈：任一 pair 不低于此值 → verdict=high_similarity_pair（疑似越权，需 LLM 判真假阳）"}
  }
}`)
}

// pairScore 是单个身份对的相似度评分；只在命中 min_threshold 时进入 suspicious_pairs。
type pairScore struct {
	A           string  `json:"a"`
	B           string  `json:"b"`
	Score       float64 `json:"score"`
	LengthRatio float64 `json:"length_ratio"` // min(|a|,|b|)/max(|a|,|b|)，给 LLM 参考
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
type similarityOutput struct {
	Algorithm       string      `json:"algorithm"`
	MinThreshold    float64     `json:"min_threshold"`
	HighThreshold   float64     `json:"high_threshold"`
	Identities      []string    `json:"identities"`
	Verdict         string      `json:"verdict"`
	SuspiciousPairs []pairScore `json:"suspicious_pairs"`
	Summary         summary     `json:"summary"`
}

// Execute 解析 args → 取 LastResponses → 两两算 length-gate + Jaccard → 出 verdict + suspicious_pairs。
func (a *ComputeSimilarity) Execute(_ context.Context, args json.RawMessage) (tool.Result, error) {
	var in struct {
		MinThreshold  float64 `json:"min_threshold"`
		HighThreshold float64 `json:"high_threshold"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return tool.Result{}, fmt.Errorf("解析 compute_similarity 参数失败: %w", err)
		}
	}
	if len(a.Session.LastResponses) == 0 {
		return tool.Result{}, fmt.Errorf("session.LastResponses 为空，请先调 replay_multi_identity")
	}
	if in.MinThreshold <= 0 {
		in.MinThreshold = defaultMinThreshold
	}
	if in.HighThreshold <= 0 {
		in.HighThreshold = defaultHighThreshold
	}
	if in.HighThreshold < in.MinThreshold {
		return tool.Result{}, fmt.Errorf("high_threshold (%v) 不能小于 min_threshold (%v)", in.HighThreshold, in.MinThreshold)
	}

	rs := a.Session.LastResponses
	n := len(rs)
	identities := make([]string, n)
	for i := range rs {
		identities[i] = rs[i].IdentityName
	}

	out := similarityOutput{
		Algorithm:       "jaccard_token+length_gate",
		MinThreshold:    in.MinThreshold,
		HighThreshold:   in.HighThreshold,
		Identities:      identities,
		SuspiciousPairs: []pairScore{},
	}

	// 单一身份没有 pair：当 verdict=all_below_threshold（无对比，直接判无越权信号）。
	if n < 2 {
		out.Verdict = verdictAllBelow
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
				// 长度差距太大 → 直接判不相似，跳过 Jaccard（省 CPU）。
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

	switch {
	case aboveHigh > 0:
		out.Verdict = verdictHighSim
	case aboveMin > 0:
		out.Verdict = verdictAmbiguous
	default:
		out.Verdict = verdictAllBelow
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

// marshalResult 序列化输出 + 拼一行 Summary 给 react/log 看（不进 LLM 回上下文，避免冗余）。
func marshalResult(out similarityOutput, n int) (tool.Result, error) {
	enc, err := json.Marshal(out)
	if err != nil {
		return tool.Result{}, fmt.Errorf("序列化 compute_similarity 输出失败: %w", err)
	}
	return tool.Result{
		Output: enc,
		Summary: fmt.Sprintf(
			"compute_similarity n=%d verdict=%s max=%.2f above_min=%d above_high=%d",
			n, out.Verdict, out.Summary.MaxScore, out.Summary.AboveMin, out.Summary.AboveHigh,
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
