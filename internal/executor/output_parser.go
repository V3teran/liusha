package executor

import (
	"errors"
	"strings"

	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
)

// Output 是 Executor 的标准输出格式（通用）
type Output struct {
	Status       string        `json:"status"`       // completed / failed
	Summary      string        `json:"summary"`      // 执行总结
	Observations []Observation `json:"observations"` // 观察结果列表
}

// Observation 载荷与本文件同包（observation.go）

// ParseOutput 从 ReAct 结果中解析 Executor 的终稿：先看终稿正文，再往前看对话——模型把输出
// JSON 写在中间、最后却以散文收尾时，观察不至于丢。都没有时返回空输出（没有观察结果），
// 不报错：终稿合不合格由 outputCheck 在运行时把关，走到这里的是重述也没交出 JSON 的。
func ParseOutput(result *runtime.ReActResult) *Output {
	if output, ok := outputInText(result.FinalAnswer); ok {
		return output
	}
	for i := len(result.MessageHistory) - 1; i >= 0; i-- {
		message := result.MessageHistory[i]
		if message.Role != llm.RoleAssistant || !strings.Contains(strings.ToLower(message.Content), "observations") {
			continue
		}
		if output, ok := outputInText(message.Content); ok {
			return output
		}
	}
	return &Output{Status: "completed", Summary: "执行完成，无观察结果", Observations: []Observation{}}
}

// outputCheck 是执行终稿的验收：终稿里必须取得到带 status 的输出 JSON（观察写在它的
// observations 里）。模型在 JSON 前后写的说明不影响验收——解析器本来就取得出 JSON。
// 运行时据它决定终稿是原样交付，还是带着原因请模型重述：观察不至于丢在一段散文里。
func outputCheck(answer string) error {
	if _, ok := outputInText(answer); ok {
		return nil
	}
	return errors.New("取不到带 status 的输出 JSON")
}

// outputInText 从一段正文里找输出 JSON：先找 ```json 围栏块，没有再找首个 { 到末个 }
// 之间的裸 JSON；取到且带 status 才算。
func outputInText(text string) (*Output, bool) {
	for _, block := range extractJSONBlocks(text) {
		var output Output
		// 修复式解析：模型在 JSON 字符串里写裸反斜杠（Windows 路径等）是高频噪声，
		// 坏块值得修复后再判一次，而不是直接跳过
		if err := llm.UnmarshalLoose([]byte(block), &output); err == nil && output.Status != "" {
			return &output, true
		}
	}
	return nil, false
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
