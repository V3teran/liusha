package executor

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
)

// ExecutorOutput 是 Executor 的标准输出格式（通用）
type ExecutorOutput struct {
	Status       string        `json:"status"`       // completed / failed
	Summary      string        `json:"summary"`      // 执行总结
	Observations []Observation `json:"observations"` // 观察结果列表
}

// Observation 是单个观察结果的结构（通用）
type Observation struct {
	Statement  string          `json:"statement"`  // 观察陈述
	Reasoning  string          `json:"reasoning"`  // 推理过程
	TestPlan   string          `json:"test_plan"`  // 验证计划
	Confidence string          `json:"confidence"` // low/medium/high
	Severity   string          `json:"severity"`   // low/medium/high/critical
	Repro      json.RawMessage `json:"repro"`      // 复现配方
}

// ParseExecutorOutput 从 ReAct 结果中解析 Executor 的输出
func ParseExecutorOutput(result *runtime.ReActResult) (*ExecutorOutput, error) {
	// 策略 1：尝试从最后一条 assistant 消息中解析 JSON
	output, err := parseFromLastMessage(result)
	if err == nil && output != nil {
		return output, nil
	}

	// 策略 2：尝试从 FinalAnswer 中解析
	if result.FinalAnswer != "" {
		output, err = parseFromText(result.FinalAnswer)
		if err == nil && output != nil {
			return output, nil
		}
	}

	// 策略 3：扫描整个对话历史，查找 JSON 块
	output, err = parseFromHistory(result.MessageHistory)
	if err == nil && output != nil {
		return output, nil
	}

	// 策略 4：返回空输出（没有观察结果）
	return &ExecutorOutput{
		Status:       "completed",
		Summary:      "执行完成，无观察结果",
		Observations: []Observation{},
	}, nil
}

// parseFromLastMessage 从最后一条 assistant 消息中解析
func parseFromLastMessage(result *runtime.ReActResult) (*ExecutorOutput, error) {
	if len(result.MessageHistory) == 0 {
		return nil, fmt.Errorf("no history")
	}

	// 从后往前找最后一条 assistant 消息
	for i := len(result.MessageHistory) - 1; i >= 0; i-- {
		msg := result.MessageHistory[i]
		if msg.Role == "assistant" {
			return parseFromText(msg.Content)
		}
	}

	return nil, fmt.Errorf("no assistant message")
}

// parseFromText 从文本中提取 JSON 块并解析
func parseFromText(text string) (*ExecutorOutput, error) {
	// 查找 JSON 代码块
	jsonBlocks := extractJSONBlocks(text)

	for _, block := range jsonBlocks {
		var output ExecutorOutput
		if err := json.Unmarshal([]byte(block), &output); err == nil {
			// 验证是否是有效的 ExecutorOutput
			if output.Status != "" {
				return &output, nil
			}
		}
	}

	// 尝试直接解析整个文本
	var output ExecutorOutput
	if err := json.Unmarshal([]byte(text), &output); err == nil {
		if output.Status != "" {
			return &output, nil
		}
	}

	return nil, fmt.Errorf("no valid JSON found")
}

// parseFromHistory 从对话历史中查找并解析输出
func parseFromHistory(history []llm.Message) (*ExecutorOutput, error) {
	// 从后往前扫描，查找包含 "observations" 关键字的 assistant 消息
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if msg.Role == "assistant" && strings.Contains(strings.ToLower(msg.Content), "observations") {
			output, err := parseFromText(msg.Content)
			if err == nil {
				return output, nil
			}
		}
	}

	return nil, fmt.Errorf("no observations in history")
}

// extractJSONBlocks 从文本中提取所有 JSON 代码块
func extractJSONBlocks(text string) []string {
	var blocks []string

	// 查找 ```json ... ``` 块
	lines := strings.Split(text, "\n")
	var inBlock bool
	var currentBlock strings.Builder

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```json") || strings.HasPrefix(trimmed, "```JSON") {
			inBlock = true
			currentBlock.Reset()
			continue
		}

		if inBlock && strings.HasPrefix(trimmed, "```") {
			inBlock = false
			blocks = append(blocks, currentBlock.String())
			continue
		}

		if inBlock {
			currentBlock.WriteString(line)
			currentBlock.WriteString("\n")
		}
	}

	// 查找裸 JSON 对象（以 { 开头，} 结尾）
	if len(blocks) == 0 {
		startIdx := strings.Index(text, "{")
		endIdx := strings.LastIndex(text, "}")
		if startIdx != -1 && endIdx != -1 && endIdx > startIdx {
			blocks = append(blocks, text[startIdx:endIdx+1])
		}
	}

	return blocks
}
