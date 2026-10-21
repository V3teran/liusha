package runtime

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

// GoalAlignmentMiddleware 检测执行是否偏离任务目标。
//
// 检测策略：
// 1. 语义偏离：最近的操作与初始目标语义不相关
// 2. 范围偏离：执行了明显超出目标范围的操作
//
// 业界实践：
// - AutoGPT: 使用嵌入模型计算目标和行动的相似度
// - CrewAI: 通过规则匹配检测偏离
// - AgentGPT: 定期用 LLM 评估当前路径是否偏离
//
// 本实现使用轻量级的关键词匹配：
// - 提取目标的关键词（动词、名词）
// - 检查最近的操作是否包含这些关键词
// - 如果连续 N 次操作都不包含，判定为偏离
//
// 未来可升级为：
// - 使用嵌入模型（需要额外的 API 调用）
// - 使用 LLM 判断（更准确但成本高）
type GoalAlignmentMiddleware struct {
	// Objective 是任务的初始目标
	Objective string

	// ObjectiveKeywords 是从目标提取的关键词
	ObjectiveKeywords []string

	// MaxConsecutiveOffTargetActions 是连续偏离目标的操作次数阈值
	MaxConsecutiveOffTargetActions int

	// consecutiveOffTarget 记录连续偏离的次数
	consecutiveOffTarget int
}

// NewGoalAlignmentMiddleware 创建目标对齐中间件。
//
// 参数：
//   - objective: 任务目标描述
//   - maxConsecutiveOffTarget: 连续偏离次数阈值（推荐 5-8）
func NewGoalAlignmentMiddleware(objective string, maxConsecutiveOffTarget int) *GoalAlignmentMiddleware {
	if maxConsecutiveOffTarget <= 0 {
		maxConsecutiveOffTarget = 6
	}

	return &GoalAlignmentMiddleware{
		Objective:                      objective,
		ObjectiveKeywords:              extractKeywords(objective),
		MaxConsecutiveOffTargetActions: maxConsecutiveOffTarget,
		consecutiveOffTarget:           0,
	}
}

// Name 返回中间件标识（日志/调试用）。
func (m *GoalAlignmentMiddleware) Name() string {
	return "goal_alignment"
}

// BeforeIteration 是 Middleware 接口的空实现（检测都在 AfterIteration）。
func (m *GoalAlignmentMiddleware) BeforeIteration(_ context.Context, _ *IterationState) (*StopSignal, error) {
	// BeforeIteration 不做检测
	return nil, nil
}

// AfterIteration 在每轮结束后检查思考/工具调用与目标的偏离度，连续越限则要求终止。
func (m *GoalAlignmentMiddleware) AfterIteration(_ context.Context, state *IterationState) (*StopSignal, error) {
	// 检查当前思考和工具调用是否与目标对齐
	currentContext := m.buildCurrentContext(state)
	isAligned := m.isAlignedWithGoal(currentContext)

	if !isAligned {
		m.consecutiveOffTarget++
	} else {
		m.consecutiveOffTarget = 0 // 对齐则重置
	}

	if m.consecutiveOffTarget >= m.MaxConsecutiveOffTargetActions {
		return &StopSignal{
			ShouldStop: true,
			Reason:     StopReasonGoalDrift,
			Message: fmt.Sprintf(
				"连续 %d 次操作偏离任务目标（%s），当前操作：%s",
				m.consecutiveOffTarget,
				m.Objective,
				truncate(currentContext, 100),
			),
		}, nil
	}

	return nil, nil
}

// buildCurrentContext 构建当前操作的上下文（用于对齐检测）。
func (m *GoalAlignmentMiddleware) buildCurrentContext(state *IterationState) string {
	var parts []string

	// 1. 当前思考
	if state.Thought != "" {
		parts = append(parts, state.Thought)
	}

	// 2. 当前工具调用
	if len(state.ToolCalls) > 0 {
		for _, tc := range state.ToolCalls {
			if tc.ToolCall.Name != "" {
				parts = append(parts, tc.ToolCall.Name)
			}
		}
	}

	// 3. 最近的观察（可选，增加上下文）
	if state.LastObservation != "" {
		parts = append(parts, state.LastObservation)
	}

	return strings.Join(parts, " ")
}

// isAlignedWithGoal 判断当前上下文是否与目标对齐。
//
// 策略：检查上下文中是否包含目标的关键词。
// 如果包含至少 30% 的关键词，认为是对齐的。
func (m *GoalAlignmentMiddleware) isAlignedWithGoal(context string) bool {
	if len(m.ObjectiveKeywords) == 0 {
		return true // 无关键词，放行
	}

	contextLower := strings.ToLower(context)
	matchCount := 0

	for _, keyword := range m.ObjectiveKeywords {
		if strings.Contains(contextLower, strings.ToLower(keyword)) {
			matchCount++
		}
	}

	// 匹配率：至少 30% 的关键词出现
	matchRate := float64(matchCount) / float64(len(m.ObjectiveKeywords))
	return matchRate >= 0.3
}

// extractKeywords 从文本中提取关键词（简化版）。
//
// 策略：
// 1. 移除停用词（的、了、是、在 等）
// 2. 保留长度 >= 2 的词
// 3. 去重
//
// 注意：这是简化实现。生产环境可使用 NLP 库（如 jieba）。
func extractKeywords(text string) []string {
	// 停用词列表（简化版）
	stopWords := map[string]bool{
		"的": true, "了": true, "是": true, "在": true, "有": true,
		"和": true, "就": true, "不": true, "人": true, "都": true,
		"一": true, "一个": true, "上": true, "也": true, "很": true,
		"到": true, "说": true, "要": true, "去": true, "你": true,
		"会": true, "着": true, "没有": true, "看": true, "好": true,
		"自己": true, "这": true, "个": true, "们": true, "能": true,
		"for": true, "the": true, "and": true, "to": true, "of": true,
		"a": true, "in": true, "is": true, "it": true, "you": true,
		"that": true, "he": true, "was": true, "with": true, "on": true,
	}

	// 简单分词（按空格和标点）
	separators := []string{" ", "，", "。", "、", "；", "：", "！", "？", ",", ".", ";", ":", "!", "?"}
	words := []string{text}
	for _, sep := range separators {
		var newWords []string
		for _, w := range words {
			newWords = append(newWords, strings.Split(w, sep)...)
		}
		words = newWords
	}

	// 过滤和去重
	keywordSet := make(map[string]bool)
	for _, word := range words {
		word = strings.TrimSpace(word)
		wordLower := strings.ToLower(word)

		// 过滤条件：长度 >= 2，非停用词
		if len(word) >= 2 && !stopWords[wordLower] {
			keywordSet[word] = true
		}
	}

	// CJK 段附加字符二元组：中文无分隔符，整段成单一"关键词"会让对齐判定
	// 退化成逐字包含（要么永不匹配、要么靠 URL 恒真）——二元组给出可匹配
	// 的语义粒度，目标对齐检测对中文目标才有意义。
	for _, word := range words {
		runes := []rune(strings.TrimSpace(word))
		cjk := 0
		for _, r := range runes {
			if unicode.Is(unicode.Han, r) {
				cjk++
			}
		}
		if cjk < 2 {
			continue
		}
		for i := 0; i+2 <= len(runes); i++ {
			bigram := string(runes[i : i+2])
			if !stopWords[bigram] {
				keywordSet[bigram] = true
			}
		}
	}

	// 转为切片
	keywords := make([]string, 0, len(keywordSet))
	for kw := range keywordSet {
		keywords = append(keywords, kw)
	}

	return keywords
}

// truncate 截断字符串到指定长度。
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
