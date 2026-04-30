package bac

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/tool"
	"github.com/V3teran/liusha/internal/heuristic"
)

// defaultThreshold 是 BAC 越权判定的相似度阈值：
//   - all_below_threshold = true 表示所有身份对的 body 都"足够不同"（无越权迹象）。
//   - any pair >= threshold 表示存在身份看到了相似的内容（越权可疑）。
const defaultThreshold = 0.3

// ComputeSimilarity — BAC ReAct 第四步：对 Session.LastResponses 两两算结构相似度。
//
// 输出 N×N 对称矩阵 + all_below_threshold + max_pair；
// LLM 据此判断是否 anon/user 看到了 admin 才该看到的内容。
type ComputeSimilarity struct {
	Session *Session
}

// Name 返回动作名 "compute_similarity"。
func (a *ComputeSimilarity) Name() string { return "compute_similarity" }

// Description 给 LLM 看的简介。
func (a *ComputeSimilarity) Description() string {
	return "对上一次 replay 输出两两计算结构相似度，返回 N×N 矩阵 + max_pair；高相似度暗示越权。"
}

// ParametersJSON 给出可选 algorithm（目前仅 structural）+ threshold（默认 0.3）。
func (a *ComputeSimilarity) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties": {
    "algorithm":{"type":"string","enum":["structural"],"default":"structural","description":"目前仅支持 structural（token-level Jaccard）"},
    "threshold":{"type":"number","default":0.3,"minimum":0,"maximum":1,"description":"相似度阈值；< threshold 视为差异显著"}
  }
}`)
}

// pairScore 是矩阵之外另带的"最高分对"摘要，便于 LLM 直接定位可疑身份对。
type pairScore struct {
	A     string  `json:"a"`
	B     string  `json:"b"`
	Score float64 `json:"score"`
}

// similarityOutput 是 Result.Output 的统一结构。
type similarityOutput struct {
	Algorithm         string      `json:"algorithm"`
	Threshold         float64     `json:"threshold"`
	Identities        []string    `json:"identities"`
	Matrix            [][]float64 `json:"matrix"`
	AllBelowThreshold bool        `json:"all_below_threshold"`
	MaxPair           pairScore   `json:"max_pair"`
}

// Execute 解析 args → 取 LastResponses → 两两算结构相似度 → 返回矩阵 + 摘要。
func (a *ComputeSimilarity) Execute(_ context.Context, args json.RawMessage) (tool.Result, error) {
	var in struct {
		Algorithm string  `json:"algorithm"`
		Threshold float64 `json:"threshold"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return tool.Result{}, fmt.Errorf("解析 compute_similarity 参数失败: %w", err)
		}
	}
	if len(a.Session.LastResponses) == 0 {
		return tool.Result{}, fmt.Errorf("session.LastResponses 为空，请先调 replay_multi_identity")
	}
	if in.Threshold <= 0 {
		in.Threshold = defaultThreshold
	}
	if in.Algorithm == "" {
		in.Algorithm = "structural"
	}

	rs := a.Session.LastResponses
	n := len(rs)
	identities := make([]string, n)
	matrix := make([][]float64, n)
	for i := range matrix {
		matrix[i] = make([]float64, n)
	}

	maxPair := pairScore{Score: -1}
	allBelow := true
	for i := 0; i < n; i++ {
		identities[i] = rs[i].IdentityName
		matrix[i][i] = 1.0
		for j := i + 1; j < n; j++ {
			s := heuristic.StructuralSimilarity(string(rs[i].Body), string(rs[j].Body))
			matrix[i][j] = s
			matrix[j][i] = s
			if s >= in.Threshold {
				allBelow = false
			}
			if s > maxPair.Score {
				maxPair = pairScore{A: rs[i].IdentityName, B: rs[j].IdentityName, Score: s}
			}
		}
	}
	// 单一响应（n=1）时没有 pair：max_pair 留空，all_below_threshold 视为 true（无越权可言）。
	if n < 2 {
		maxPair = pairScore{}
	}

	out := similarityOutput{
		Algorithm:         in.Algorithm,
		Threshold:         in.Threshold,
		Identities:        identities,
		Matrix:            matrix,
		AllBelowThreshold: allBelow,
		MaxPair:           maxPair,
	}
	enc, err := json.Marshal(out)
	if err != nil {
		return tool.Result{}, fmt.Errorf("序列化 compute_similarity 输出失败: %w", err)
	}
	return tool.Result{
		Output: enc,
		Summary: fmt.Sprintf("compute_similarity n=%d max=%.2f all_below=%v",
			n, maxPair.Score, allBelow),
	}, nil
}
