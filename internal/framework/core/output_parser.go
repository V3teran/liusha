package core

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// OutputParser 结构化输出解析器
// 将 LLM 的自由文本输出解析为结构化数据
type OutputParser interface {
	// Parse 解析输出文本为结构化数据
	Parse(ctx context.Context, content string) (any, error)

	// GetFormatInstructions 获取格式指令（用于注入 Prompt）
	GetFormatInstructions() string

	// Validate 验证输出是否符合预期格式
	Validate(output any) error
}

// OutputFormat 输出格式类型
type OutputFormat string

const (
	FormatJSON     OutputFormat = "json"
	FormatYAML     OutputFormat = "yaml"
	FormatMarkdown OutputFormat = "markdown"
	FormatText     OutputFormat = "text"
)

// JSONOutputParser JSON 格式输出解析器
type JSONOutputParser struct {
	schema       map[string]any // JSON Schema
	targetType   reflect.Type   // 目标类型
	strictMode   bool           // 严格模式（额外字段报错）
	formatInstr  string         // 格式指令
}

// NewJSONOutputParser 创建 JSON 输出解析器
// schema: JSON Schema 定义
// targetType: 目标结构体类型（通过 reflect.TypeOf(YourStruct{}) 传入）
func NewJSONOutputParser(schema map[string]any, targetType reflect.Type) *JSONOutputParser {
	parser := &JSONOutputParser{
		schema:     schema,
		targetType: targetType,
		strictMode: false,
	}

	// 生成格式指令
	parser.formatInstr = parser.generateFormatInstructions()

	return parser
}

// Parse 解析 JSON 输出
func (p *JSONOutputParser) Parse(ctx context.Context, content string) (any, error) {
	// 提取 JSON 部分（处理 Markdown 代码块包裹的情况）
	jsonContent := p.extractJSON(content)
	if jsonContent == "" {
		return nil, fmt.Errorf("no valid JSON found in content")
	}

	// 创建目标类型实例
	resultPtr := reflect.New(p.targetType)
	result := resultPtr.Interface()

	// 解析 JSON
	decoder := json.NewDecoder(strings.NewReader(jsonContent))
	if p.strictMode {
		decoder.DisallowUnknownFields()
	}

	if err := decoder.Decode(result); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}

	// 验证
	if err := p.Validate(result); err != nil {
		return nil, fmt.Errorf("validate output: %w", err)
	}

	// 返回解引用后的值
	return resultPtr.Elem().Interface(), nil
}

// GetFormatInstructions 获取格式指令
func (p *JSONOutputParser) GetFormatInstructions() string {
	return p.formatInstr
}

// Validate 验证输出
func (p *JSONOutputParser) Validate(output any) error {
	if p.schema == nil {
		return nil
	}

	// 基础类型检查
	outputValue := reflect.ValueOf(output)
	if outputValue.Kind() == reflect.Ptr {
		outputValue = outputValue.Elem()
	}

	if outputValue.Type() != p.targetType {
		return fmt.Errorf("type mismatch: expected %s, got %s",
			p.targetType.String(), outputValue.Type().String())
	}

	// Schema 验证（简化实现）
	return p.validateWithSchema(outputValue)
}

// SetStrictMode 设置严格模式
func (p *JSONOutputParser) SetStrictMode(strict bool) {
	p.strictMode = strict
}

// extractJSON 从文本中提取 JSON
// 处理三种情况：
// 1. 纯 JSON
// 2. Markdown 代码块包裹的 JSON
// 3. 包含其他文本的混合内容
func (p *JSONOutputParser) extractJSON(content string) string {
	content = strings.TrimSpace(content)

	// 情况1：纯 JSON
	if strings.HasPrefix(content, "{") || strings.HasPrefix(content, "[") {
		return content
	}

	// 情况2：Markdown 代码块
	if strings.Contains(content, "```json") {
		start := strings.Index(content, "```json")
		end := strings.Index(content[start+7:], "```")
		if end != -1 {
			return strings.TrimSpace(content[start+7 : start+7+end])
		}
	}

	if strings.Contains(content, "```") {
		start := strings.Index(content, "```")
		end := strings.Index(content[start+3:], "```")
		if end != -1 {
			extracted := strings.TrimSpace(content[start+3 : start+3+end])
			// 移除可能的语言标识符
			if idx := strings.Index(extracted, "\n"); idx != -1 {
				extracted = strings.TrimSpace(extracted[idx+1:])
			}
			if strings.HasPrefix(extracted, "{") || strings.HasPrefix(extracted, "[") {
				return extracted
			}
		}
	}

	// 情况3：查找 JSON 对象/数组的边界
	startIdx := strings.Index(content, "{")
	if startIdx == -1 {
		startIdx = strings.Index(content, "[")
	}
	if startIdx == -1 {
		return ""
	}

	// 简单的括号匹配
	depth := 0
	for i := startIdx; i < len(content); i++ {
		switch content[i] {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return content[startIdx : i+1]
			}
		}
	}

	return ""
}

// generateFormatInstructions 生成格式指令
func (p *JSONOutputParser) generateFormatInstructions() string {
	if p.schema == nil {
		return "请以 JSON 格式输出结果。"
	}

	var sb strings.Builder
	sb.WriteString("请严格按照以下 JSON Schema 输出结果：\n\n")

	// 序列化 schema
	schemaJSON, _ := json.MarshalIndent(p.schema, "", "  ")
	sb.WriteString("```json\n")
	sb.Write(schemaJSON)
	sb.WriteString("\n```\n\n")

	sb.WriteString("要求：\n")
	sb.WriteString("1. 必须是有效的 JSON 格式\n")
	sb.WriteString("2. 所有必填字段必须存在\n")
	sb.WriteString("3. 字段类型必须匹配 Schema 定义\n")
	sb.WriteString("4. 可以用 Markdown 代码块包裹\n")

	return sb.String()
}

// validateWithSchema 使用 Schema 验证
func (p *JSONOutputParser) validateWithSchema(value reflect.Value) error {
	// 获取 required 字段
	required, ok := p.schema["required"].([]any)
	if !ok {
		return nil
	}

	// 检查必填字段
	for _, field := range required {
		fieldName, ok := field.(string)
		if !ok {
			continue
		}

		// 获取字段值
		fieldValue := value.FieldByName(fieldName)
		if !fieldValue.IsValid() {
			// 尝试 JSON 标签
			fieldValue = p.getFieldByJSONTag(value, fieldName)
			if !fieldValue.IsValid() {
				return fmt.Errorf("required field missing: %s", fieldName)
			}
		}

		// 检查零值
		if fieldValue.IsZero() {
			return fmt.Errorf("required field is empty: %s", fieldName)
		}
	}

	return nil
}

// getFieldByJSONTag 通过 JSON 标签获取字段
func (p *JSONOutputParser) getFieldByJSONTag(value reflect.Value, jsonTag string) reflect.Value {
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" {
			continue
		}

		// 解析标签（可能包含 omitempty 等）
		tagName := strings.Split(tag, ",")[0]
		if tagName == jsonTag {
			return value.Field(i)
		}
	}

	return reflect.Value{}
}

// RetryableParser 带重试的解析器
// 当解析失败时，可以请求 LLM 重新生成
type RetryableParser struct {
	parser     OutputParser
	llmCaller  LLMCaller // LLM 调用接口
	maxRetries int
}

// LLMCaller LLM 调用接口
type LLMCaller interface {
	Call(ctx context.Context, messages []Message) (string, error)
}

// NewRetryableParser 创建带重试的解析器
func NewRetryableParser(parser OutputParser, llmCaller LLMCaller, maxRetries int) *RetryableParser {
	return &RetryableParser{
		parser:     parser,
		llmCaller:  llmCaller,
		maxRetries: maxRetries,
	}
}

// ParseWithRetry 带重试的解析
func (p *RetryableParser) ParseWithRetry(ctx context.Context, initialOutput string) (any, error) {
	var lastErr error

	// 第一次尝试解析原始输出
	result, err := p.parser.Parse(ctx, initialOutput)
	if err == nil {
		return result, nil
	}
	lastErr = err

	// 重试
	currentOutput := initialOutput
	for i := 0; i < p.maxRetries; i++ {
		// 构造修正提示
		messages := []Message{
			*NewTextMessage(
				fmt.Sprintf("retry-%d", i),
				RoleUser,
				p.buildRetryPrompt(currentOutput, lastErr),
			),
		}

		// 请求 LLM 重新生成
		newOutput, err := p.llmCaller.Call(ctx, messages)
		if err != nil {
			return nil, fmt.Errorf("llm retry call failed: %w", err)
		}

		// 尝试解析新输出
		result, err = p.parser.Parse(ctx, newOutput)
		if err == nil {
			return result, nil
		}

		lastErr = err
		currentOutput = newOutput
	}

	return nil, fmt.Errorf("parse failed after %d retries: %w", p.maxRetries, lastErr)
}

// buildRetryPrompt 构造重试提示
func (p *RetryableParser) buildRetryPrompt(output string, err error) string {
	return fmt.Sprintf(`之前的输出格式不正确，错误信息：%s

之前的输出：
%s

%s

请重新生成符合要求的输出。`, err.Error(), output, p.parser.GetFormatInstructions())
}

// ListOutputParser 列表输出解析器
// 用于解析逗号/换行分隔的列表
type ListOutputParser struct {
	separator string
}

// NewListOutputParser 创建列表解析器
func NewListOutputParser(separator string) *ListOutputParser {
	return &ListOutputParser{
		separator: separator,
	}
}

// Parse 解析列表
func (p *ListOutputParser) Parse(ctx context.Context, content string) (any, error) {
	content = strings.TrimSpace(content)

	// 移除 Markdown 代码块
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		if len(lines) > 2 {
			content = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}

	// 分割
	items := strings.Split(content, p.separator)

	// 清理每一项
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			result = append(result, item)
		}
	}

	return result, nil
}

// GetFormatInstructions 获取格式指令
func (p *ListOutputParser) GetFormatInstructions() string {
	sep := p.separator
	if sep == "\n" {
		sep = "换行"
	}
	return fmt.Sprintf("请输出一个列表，每项用 %s 分隔。", sep)
}

// Validate 验证列表
func (p *ListOutputParser) Validate(output any) error {
	_, ok := output.([]string)
	if !ok {
		return fmt.Errorf("output is not a string slice")
	}
	return nil
}
