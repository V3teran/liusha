package heuristic

import (
	"encoding/json"
	"fmt"
	"strings"
)

// JSON 加权相似度三因子（与 liusha2 一致，便于跨项目对照）：
//   - structure：字段路径集合 Jaccard，反映"同构性"
//   - value：叶子值集合 Jaccard，反映"内容重叠度"
//   - length：长度比，作为粗粒度兜底信号
//
// 三者权重相加 = 1.0；任一权重独立调整时其他需要相应缩放，否则会破坏 [0,1] 上界。
const (
	jsonWeightStructure = 0.4
	jsonWeightValue     = 0.5
	jsonWeightLength    = 0.1
)

// StructuralSimilarity 返回 [0,1] 区间的结构相似度。
//
// 路径选择：
//   - 输入两端都能 JSON 解析 → 走 jsonSimilarity（field path / leaf value / length 加权）
//   - 否则 → 退回 tokenJaccard（小写 + 空白切分，与原行为一致）
//
// 边界：
//   - a == b（含两空串）→ 1.0
//   - 一空一非空（非 JSON）→ tokenJaccard 0.0（并集非空，交集为空）
//   - 双空对象 `{}` vs `{}` → 路径/值集合都空 → 退化为长度比，结果 1.0
//
// 设计意图（borrow from liusha2）：
//   - JSON-aware 让"键序差异 / 数组顺序差异"不再被惩罚（按集合比较）
//   - 路径权重低于值权重：服务返回相同 schema 但内容不同时（公开列表/错误页），
//     valueSim 接近 0 起主导，避免被高 pathSim 抬到误判区间
func StructuralSimilarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	var ja, jb interface{}
	if json.Unmarshal([]byte(a), &ja) == nil && json.Unmarshal([]byte(b), &jb) == nil {
		return jsonSimilarity(ja, jb, len(a), len(b))
	}
	return tokenJaccard(a, b)
}

// jsonSimilarity 在两端都解析成功后做加权打分。
func jsonSimilarity(a, b interface{}, lenA, lenB int) float64 {
	pathSim := jaccardSets(extractFieldPaths(a, ""), extractFieldPaths(b, ""))
	valueSim := jaccardSets(extractLeafValues(a), extractLeafValues(b))
	lenSim := LengthRatio(lenA, lenB)
	return jsonWeightStructure*pathSim + jsonWeightValue*valueSim + jsonWeightLength*lenSim
}

// extractFieldPaths 收集 JSON 中所有字段路径（点号分隔，数组用 [] 占位忽略下标）。
// 同 liusha2 的 ignore_array_length=True 行为：列表只看第一个元素的形态。
func extractFieldPaths(v interface{}, prefix string) map[string]struct{} {
	paths := map[string]struct{}{}
	switch t := v.(type) {
	case map[string]interface{}:
		for k, val := range t {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			paths[p] = struct{}{}
			for sub := range extractFieldPaths(val, p) {
				paths[sub] = struct{}{}
			}
		}
	case []interface{}:
		if len(t) > 0 {
			for sub := range extractFieldPaths(t[0], prefix+"[]") {
				paths[sub] = struct{}{}
			}
		}
	}
	return paths
}

// extractLeafValues 收集 JSON 所有叶子值（数字/字符串/布尔），用于值集合 Jaccard。
// nil（JSON null）跳过——当作"无内容"，避免被当作有效值打分。
func extractLeafValues(v interface{}) map[string]struct{} {
	out := map[string]struct{}{}
	var walk func(x interface{})
	walk = func(x interface{}) {
		switch t := x.(type) {
		case map[string]interface{}:
			for _, val := range t {
				walk(val)
			}
		case []interface{}:
			for _, item := range t {
				walk(item)
			}
		case nil:
			// skip
		default:
			out[fmt.Sprint(t)] = struct{}{}
		}
	}
	walk(v)
	return out
}

// jaccardSets 返回两个 set 的 Jaccard：|A∩B| / |A∪B|。
// 双空集 → 1.0（同构 + 同空内容）；一空一非空 → 0。
func jaccardSets(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	inter := 0
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// LengthRatio 返回 min(|a|,|b|) / max(|a|,|b|)；两端皆 0 时视为 1.0。
// 通用工具函数：用于 jsonSimilarity 的长度因子，也用于 probe 等长度短路判定。
func LengthRatio(a, b int) float64 {
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

// tokenJaccard 是非 JSON 输入的 fallback：小写 + 空白切分后做 token Jaccard。
// 保留是为了让纯文本响应（HTML 错误页、纯字符串）仍有可比较的相似度信号。
func tokenJaccard(a, b string) float64 {
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
