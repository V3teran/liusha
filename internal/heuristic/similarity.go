package heuristic

import "strings"

// StructuralSimilarity 返回 [0,1] 区间的 token-level Jaccard 相似度：
// 把两个字符串小写后按空白切分成 token 集合，计算 |A∩B| / |A∪B|。
//
// 边界：
//   - a == b（含两空串）→ 1.0
//   - 一空一非空 → 0.0（并集非空，交集为空）
//
// 说明：
//   - 这里只用空白切分；标点保留在 token 内（plan 给的轻量实现）。
//   - 后续若 BAC skill 需要更精细的相似度，可在此包内追加 NgramSimilarity 等。
func StructuralSimilarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	at := tokenSet(a)
	bt := tokenSet(b)
	if len(at) == 0 && len(bt) == 0 {
		return 1.0
	}
	inter := 0
	for k := range at {
		if _, ok := bt[k]; ok {
			inter++
		}
	}
	union := len(at) + len(bt) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func tokenSet(s string) map[string]struct{} {
	m := map[string]struct{}{}
	for _, t := range strings.Fields(strings.ToLower(s)) {
		m[t] = struct{}{}
	}
	return m
}
