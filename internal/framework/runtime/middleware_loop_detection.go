package runtime

import (
	"context"
	"fmt"
	"strings"
)

// LoopDetectionMiddleware 检测重复模式：工具调用循环、思维循环。
//
// 检测策略：
// 1. 工具循环：最近 N 次调用都是同一个工具
// 2. 思维循环：最近 N 次思考内容高度相似
//
// 业界实践：
// - AutoGPT: 检测连续 3 次相同工具调用
// - LangChain: 检测连续 5 次相同 action
// - CrewAI: 检测连续 4 次相同操作
//
// 本实现采用更宽松的阈值（可配置），避免误杀正常的重复操作。
type LoopDetectionMiddleware struct {
	// MaxConsecutiveSameTools 是触发循环检测的连续相同工具次数阈值
	MaxConsecutiveSameTools int

	// MaxConsecutiveSimilarThoughts 是触发思维循环检测的相似思考次数阈值
	MaxConsecutiveSimilarThoughts int

	// SimilarityThreshold 是判定两个思考相似的阈值（0-1）
	// 使用简单的文本相似度（共同词比例），0.8 表示 80% 相似
	SimilarityThreshold float64
}

// NewLoopDetectionMiddleware 创建循环检测中间件。
//
// 参数：
//   - maxSameTools: 连续相同工具次数阈值（推荐 8-12）
//   - maxSimilarThoughts: 连续相似思考次数阈值（推荐 6-10）
func NewLoopDetectionMiddleware(maxSameTools, maxSimilarThoughts int) *LoopDetectionMiddleware {
	if maxSameTools <= 0 {
		maxSameTools = 10 // 默认值
	}
	if maxSimilarThoughts <= 0 {
		maxSimilarThoughts = 8
	}
	return &LoopDetectionMiddleware{
		MaxConsecutiveSameTools:       maxSameTools,
		MaxConsecutiveSimilarThoughts: maxSimilarThoughts,
		SimilarityThreshold:           0.8,
	}
}

func (m *LoopDetectionMiddleware) Name() string {
	return "loop_detection"
}

func (m *LoopDetectionMiddleware) BeforeIteration(ctx context.Context, state *IterationState) (*StopSignal, error) {
	// BeforeIteration 不做检测，因为还没有新的工具调用
	return nil, nil
}

func (m *LoopDetectionMiddleware) AfterIteration(ctx context.Context, state *IterationState) (*StopSignal, error) {
	// 检测 1: 工具调用循环
	if signal := m.detectToolLoop(state); signal != nil {
		return signal, nil
	}

	// 检测 2: 思维内容循环
	if signal := m.detectThoughtLoop(state); signal != nil {
		return signal, nil
	}

	return nil, nil
}

// detectToolLoop 检测工具调用循环。
func (m *LoopDetectionMiddleware) detectToolLoop(state *IterationState) *StopSignal {
	recentTools := state.RecentToolNames
	if len(recentTools) < m.MaxConsecutiveSameTools {
		return nil // 样本不足
	}

	// 取最近 N 个工具
	lastN := recentTools[len(recentTools)-m.MaxConsecutiveSameTools:]

	// 检查是否全部相同
	firstTool := lastN[0]
	if firstTool == "" {
		return nil // 没有工具调用（可能是最终答案）
	}

	allSame := true
	for _, tool := range lastN[1:] {
		if tool != firstTool {
			allSame = false
			break
		}
	}

	if allSame {
		return &StopSignal{
			ShouldStop: true,
			Reason:     StopReasonToolLoop,
			Message: fmt.Sprintf(
				"检测到工具调用循环：连续 %d 次调用 %s，疑似陷入死循环",
				m.MaxConsecutiveSameTools,
				firstTool,
			),
		}
	}

	return nil
}

// detectThoughtLoop 检测思维内容循环。
func (m *LoopDetectionMiddleware) detectThoughtLoop(state *IterationState) *StopSignal {
	recentThoughts := state.RecentThoughts
	if len(recentThoughts) < m.MaxConsecutiveSimilarThoughts {
		return nil // 样本不足
	}

	// 取最近 N 个思考
	lastN := recentThoughts[len(recentThoughts)-m.MaxConsecutiveSimilarThoughts:]

	// 计算相似度：任意两个思考的相似度都超过阈值
	similarCount := 0
	for i := 0; i < len(lastN)-1; i++ {
		for j := i + 1; j < len(lastN); j++ {
			if m.textSimilarity(lastN[i], lastN[j]) >= m.SimilarityThreshold {
				similarCount++
			}
		}
	}

	// 如果超过一半的配对都相似，判定为循环
	totalPairs := (len(lastN) * (len(lastN) - 1)) / 2
	if float64(similarCount)/float64(totalPairs) > 0.5 {
		return &StopSignal{
			ShouldStop: true,
			Reason:     StopReasonThoughtLoop,
			Message: fmt.Sprintf(
				"检测到思维循环：最近 %d 次思考内容高度重复，疑似陷入死循环",
				m.MaxConsecutiveSimilarThoughts,
			),
		}
	}

	return nil
}

// textSimilarity 计算两个文本的相似度（0-1）。
// 使用简单的 Jaccard 相似度：共同词数 / 总词数。
func (m *LoopDetectionMiddleware) textSimilarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}

	// 分词（简单按空格分）
	wordsA := strings.Fields(strings.ToLower(a))
	wordsB := strings.Fields(strings.ToLower(b))

	// 构建集合
	setA := make(map[string]bool)
	for _, w := range wordsA {
		setA[w] = true
	}
	setB := make(map[string]bool)
	for _, w := range wordsB {
		setB[w] = true
	}

	// 计算交集和并集
	intersection := 0
	for w := range setA {
		if setB[w] {
			intersection++
		}
	}

	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}

	return float64(intersection) / float64(union)
}
