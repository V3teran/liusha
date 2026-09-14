package core

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestJSONOutputParser_Parse(t *testing.T) {
	// 定义测试结构体
	type VulnReport struct {
		Name     string   `json:"name"`
		Severity string   `json:"severity"`
		Location string   `json:"location"`
		Evidence []string `json:"evidence"`
	}

	// 定义 Schema
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":     map[string]any{"type": "string"},
			"severity": map[string]any{"type": "string"},
			"location": map[string]any{"type": "string"},
			"evidence": map[string]any{"type": "array"},
		},
		"required": []any{"name", "severity"},
	}

	parser := NewJSONOutputParser(schema, reflect.TypeOf(VulnReport{}))

	tests := []struct {
		name    string
		content string
		wantErr bool
		check   func(t *testing.T, result any)
	}{
		{
			name: "纯JSON格式",
			content: `{
				"name": "SQL注入",
				"severity": "high",
				"location": "login.php:42",
				"evidence": ["payload1", "payload2"]
			}`,
			wantErr: false,
			check: func(t *testing.T, result any) {
				report, ok := result.(VulnReport)
				if !ok {
					t.Fatal("result type mismatch")
				}
				if report.Name != "SQL注入" {
					t.Errorf("name = %s, want SQL注入", report.Name)
				}
				if report.Severity != "high" {
					t.Errorf("severity = %s, want high", report.Severity)
				}
				if len(report.Evidence) != 2 {
					t.Errorf("evidence count = %d, want 2", len(report.Evidence))
				}
			},
		},
		{
			name: "Markdown代码块包裹",
			content: "这是分析结果：\n```json\n" +
				`{"name":"XSS","severity":"medium","location":"search.php","evidence":[]}` +
				"\n```\n请查收。",
			wantErr: false,
			check: func(t *testing.T, result any) {
				report := result.(VulnReport)
				if report.Name != "XSS" {
					t.Errorf("name = %s, want XSS", report.Name)
				}
			},
		},
		{
			name: "混合文本中的JSON",
			content: `根据分析，发现了以下漏洞：

			{"name":"CSRF","severity":"low","location":"api/transfer","evidence":["无Token验证"]}

			建议立即修复。`,
			wantErr: false,
			check: func(t *testing.T, result any) {
				report := result.(VulnReport)
				if report.Name != "CSRF" {
					t.Errorf("name = %s, want CSRF", report.Name)
				}
			},
		},
		{
			name:    "缺失必填字段",
			content: `{"location":"test.php"}`,
			wantErr: true,
		},
		{
			name:    "无效JSON",
			content: `这不是JSON`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parser.Parse(context.Background(), tt.content)
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.check != nil {
				tt.check(t, result)
			}
		})
	}
}

func TestJSONOutputParser_ExtractJSON(t *testing.T) {
	parser := NewJSONOutputParser(nil, reflect.TypeOf(struct{}{}))

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "纯JSON对象",
			content: `{"key":"value"}`,
			want:    `{"key":"value"}`,
		},
		{
			name:    "纯JSON数组",
			content: `[1,2,3]`,
			want:    `[1,2,3]`,
		},
		{
			name: "Markdown JSON代码块",
			content: "结果：\n```json\n" +
				`{"result":"ok"}` +
				"\n```",
			want: `{"result":"ok"}`,
		},
		{
			name: "普通代码块",
			content: "```\n" +
				`{"data":123}` +
				"\n```",
			want: `{"data":123}`,
		},
		{
			name:    "文本中的JSON",
			content: `前缀文本 {"inner":"data"} 后缀文本`,
			want:    `{"inner":"data"}`,
		},
		{
			name:    "嵌套JSON",
			content: `{"outer":{"inner":"value"}}`,
			want:    `{"outer":{"inner":"value"}}`,
		},
		{
			name:    "无JSON",
			content: `纯文本内容`,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parser.extractJSON(tt.content)
			if got != tt.want {
				t.Errorf("extractJSON() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestJSONOutputParser_Validate(t *testing.T) {
	type TestStruct struct {
		Required string `json:"required"`
		Optional string `json:"optional"`
	}

	schema := map[string]any{
		"required": []any{"required"},
	}

	parser := NewJSONOutputParser(schema, reflect.TypeOf(TestStruct{}))

	tests := []struct {
		name    string
		output  any
		wantErr bool
	}{
		{
			name: "有效输出",
			output: TestStruct{
				Required: "value",
				Optional: "optional",
			},
			wantErr: false,
		},
		{
			name: "缺少必填字段",
			output: TestStruct{
				Optional: "optional",
			},
			wantErr: true,
		},
		{
			name: "类型不匹配",
			output: struct {
				Wrong string
			}{Wrong: "value"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := parser.Validate(tt.output)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestJSONOutputParser_GetFormatInstructions(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"field": map[string]any{"type": "string"},
		},
	}

	parser := NewJSONOutputParser(schema, reflect.TypeOf(struct{}{}))
	instructions := parser.GetFormatInstructions()

	// 检查指令包含关键内容
	if !strings.Contains(instructions, "JSON Schema") {
		t.Error("instructions should contain 'JSON Schema'")
	}
	if !strings.Contains(instructions, "必填字段") {
		t.Error("instructions should contain '必填字段'")
	}
}

func TestJSONOutputParser_StrictMode(t *testing.T) {
	type StrictStruct struct {
		Field string `json:"field"`
	}

	parser := NewJSONOutputParser(nil, reflect.TypeOf(StrictStruct{}))
	parser.SetStrictMode(true)

	// 包含额外字段的 JSON
	content := `{"field":"value","extra":"should_fail"}`

	_, err := parser.Parse(context.Background(), content)
	if err == nil {
		t.Error("strict mode should reject unknown fields")
	}
}

// MockLLMCaller 模拟 LLM 调用
type MockLLMCaller struct {
	responses []string
	callCount int
}

func (m *MockLLMCaller) Call(ctx context.Context, messages []Message) (string, error) {
	if m.callCount >= len(m.responses) {
		return "", nil
	}
	response := m.responses[m.callCount]
	m.callCount++
	return response, nil
}

func TestRetryableParser_ParseWithRetry(t *testing.T) {
	type SimpleStruct struct {
		Value string `json:"value"`
	}

	schema := map[string]any{
		"required": []any{"value"},
	}

	baseParser := NewJSONOutputParser(schema, reflect.TypeOf(SimpleStruct{}))

	tests := []struct {
		name          string
		initialOutput string
		llmResponses  []string
		wantErr       bool
		wantRetries   int
	}{
		{
			name:          "首次成功",
			initialOutput: `{"value":"ok"}`,
			llmResponses:  []string{},
			wantErr:       false,
			wantRetries:   0,
		},
		{
			name:          "第一次重试成功",
			initialOutput: `invalid json`,
			llmResponses:  []string{`{"value":"fixed"}`},
			wantErr:       false,
			wantRetries:   1,
		},
		{
			name:          "重试后仍失败",
			initialOutput: `bad`,
			llmResponses:  []string{`still bad`, `{"value":""}`}, // 空值不通过required验证
			wantErr:       true,
			wantRetries:   2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockLLM := &MockLLMCaller{responses: tt.llmResponses}
			retryParser := NewRetryableParser(baseParser, mockLLM, 2)

			result, err := retryParser.ParseWithRetry(context.Background(), tt.initialOutput)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseWithRetry() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if mockLLM.callCount != tt.wantRetries {
				t.Errorf("retry count = %d, want %d", mockLLM.callCount, tt.wantRetries)
			}

			if !tt.wantErr {
				s, ok := result.(SimpleStruct)
				if !ok {
					t.Fatal("result type mismatch")
				}
				if s.Value == "" {
					t.Error("value should not be empty")
				}
			}
		})
	}
}

func TestListOutputParser_Parse(t *testing.T) {
	tests := []struct {
		name      string
		separator string
		content   string
		want      []string
		wantErr   bool
	}{
		{
			name:      "逗号分隔",
			separator: ",",
			content:   "item1,item2,item3",
			want:      []string{"item1", "item2", "item3"},
		},
		{
			name:      "换行分隔",
			separator: "\n",
			content:   "line1\nline2\nline3",
			want:      []string{"line1", "line2", "line3"},
		},
		{
			name:      "带空格",
			separator: ",",
			content:   "a , b , c ",
			want:      []string{"a", "b", "c"},
		},
		{
			name:      "Markdown代码块",
			separator: "\n",
			content:   "```\nitem1\nitem2\n```",
			want:      []string{"item1", "item2"},
		},
		{
			name:      "空项过滤",
			separator: ",",
			content:   "a,,b,,,c",
			want:      []string{"a", "b", "c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewListOutputParser(tt.separator)
			result, err := parser.Parse(context.Background(), tt.content)
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				got, ok := result.([]string)
				if !ok {
					t.Fatal("result is not []string")
				}

				if len(got) != len(tt.want) {
					t.Errorf("length = %d, want %d", len(got), len(tt.want))
					return
				}

				for i, item := range got {
					if item != tt.want[i] {
						t.Errorf("item[%d] = %s, want %s", i, item, tt.want[i])
					}
				}
			}
		})
	}
}

func TestListOutputParser_GetFormatInstructions(t *testing.T) {
	tests := []struct {
		name      string
		separator string
		want      string
	}{
		{
			name:      "逗号分隔符",
			separator: ",",
			want:      "用 , 分隔",
		},
		{
			name:      "换行分隔符",
			separator: "\n",
			want:      "用 换行 分隔",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewListOutputParser(tt.separator)
			got := parser.GetFormatInstructions()
			if !strings.Contains(got, tt.want) {
				t.Errorf("GetFormatInstructions() should contain %s", tt.want)
			}
		})
	}
}

func TestListOutputParser_Validate(t *testing.T) {
	parser := NewListOutputParser(",")

	tests := []struct {
		name    string
		output  any
		wantErr bool
	}{
		{
			name:    "有效列表",
			output:  []string{"a", "b", "c"},
			wantErr: false,
		},
		{
			name:    "类型错误",
			output:  []int{1, 2, 3},
			wantErr: true,
		},
		{
			name:    "非切片",
			output:  "string",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := parser.Validate(tt.output)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// 性能测试
func BenchmarkJSONOutputParser_Parse(b *testing.B) {
	type BenchStruct struct {
		Field1 string `json:"field1"`
		Field2 int    `json:"field2"`
		Field3 bool   `json:"field3"`
	}

	parser := NewJSONOutputParser(nil, reflect.TypeOf(BenchStruct{}))
	content := `{"field1":"value","field2":123,"field3":true}`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parser.Parse(context.Background(), content)
	}
}

func BenchmarkJSONOutputParser_ExtractJSON(b *testing.B) {
	parser := NewJSONOutputParser(nil, reflect.TypeOf(struct{}{}))
	content := "前缀文本```json\n" + `{"key":"value"}` + "\n```后缀"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = parser.extractJSON(content)
	}
}

// 集成测试：完整工作流
func TestOutputParser_Integration(t *testing.T) {
	// 定义业务结构
	type AttackResult struct {
		Success    bool     `json:"success"`
		Method     string   `json:"method"`
		Payloads   []string `json:"payloads"`
		Confidence float64  `json:"confidence"`
	}

	// 创建 Schema
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"success":    map[string]any{"type": "boolean"},
			"method":     map[string]any{"type": "string"},
			"payloads":   map[string]any{"type": "array"},
			"confidence": map[string]any{"type": "number"},
		},
		"required": []any{"success", "method"},
	}

	// 创建解析器
	parser := NewJSONOutputParser(schema, reflect.TypeOf(AttackResult{}))

	// 模拟 LLM 输出
	llmOutput := `根据对目标系统的分析，我发现了以下攻击向量：

攻击结果如下：

` + "```json" + `
{
  "success": true,
  "method": "SQL Injection",
  "payloads": [
    "' OR '1'='1",
    "admin'--",
    "1' UNION SELECT NULL--"
  ],
  "confidence": 0.95
}
` + "```" + `

建议立即修复此高危漏洞。`

	// 解析
	result, err := parser.Parse(context.Background(), llmOutput)
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	// 验证结果
	attackResult, ok := result.(AttackResult)
	if !ok {
		t.Fatal("type assertion failed")
	}

	if !attackResult.Success {
		t.Error("success should be true")
	}
	if attackResult.Method != "SQL Injection" {
		t.Errorf("method = %s, want SQL Injection", attackResult.Method)
	}
	if len(attackResult.Payloads) != 3 {
		t.Errorf("payloads count = %d, want 3", len(attackResult.Payloads))
	}
	if attackResult.Confidence != 0.95 {
		t.Errorf("confidence = %f, want 0.95", attackResult.Confidence)
	}

	// 验证格式指令生成
	instructions := parser.GetFormatInstructions()
	if instructions == "" {
		t.Error("format instructions should not be empty")
	}

	// 将指令转换为 JSON 验证
	var instructionCheck map[string]any
	if err := json.Unmarshal([]byte(llmOutput), &instructionCheck); err == nil {
		// 如果能解析，说明输出有效
		t.Log("Output is valid JSON")
	}
}
