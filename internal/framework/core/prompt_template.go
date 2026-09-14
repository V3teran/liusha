package core

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// PromptTemplate 提示词模板接口
type PromptTemplate interface {
	// Render 渲染模板，将变量替换为实际值
	Render(ctx context.Context, vars map[string]any) (string, error)

	// Validate 验证变量是否满足模板要求
	Validate(vars map[string]any) error

	// GetRequiredVars 获取必填变量列表
	GetRequiredVars() []string

	// GetOptionalVars 获取可选变量列表及其默认值
	GetOptionalVars() map[string]any
}

// Template 提示词模板实现
type Template struct {
	// 模板名称
	name string

	// 模板内容（使用 {{.variable}} 语法）
	template string

	// 必填变量
	required []string

	// 可选变量及默认值
	optional map[string]any

	// 少样本示例
	examples []Example

	// 模板元数据
	metadata TemplateMetadata
}

// TemplateMetadata 模板元数据
type TemplateMetadata struct {
	// 版本号
	Version string `json:"version"`

	// 描述
	Description string `json:"description"`

	// 作者
	Author string `json:"author,omitempty"`

	// 标签
	Tags []string `json:"tags,omitempty"`
}

// Example 少样本示例
type Example struct {
	// 输入变量
	Input map[string]any `json:"input"`

	// 期望输出
	Output string `json:"output"`

	// 示例描述
	Description string `json:"description,omitempty"`
}

// NewTemplate 创建提示词模板
func NewTemplate(name, template string) *Template {
	return &Template{
		name:     name,
		template: template,
		required: make([]string, 0),
		optional: make(map[string]any),
		examples: make([]Example, 0),
		metadata: TemplateMetadata{
			Version: "1.0",
		},
	}
}

// WithRequired 设置必填变量
func (t *Template) WithRequired(vars ...string) *Template {
	t.required = append(t.required, vars...)
	return t
}

// WithOptional 设置可选变量及默认值
func (t *Template) WithOptional(key string, defaultValue any) *Template {
	t.optional[key] = defaultValue
	return t
}

// WithExample 添加少样本示例
func (t *Template) WithExample(example Example) *Template {
	t.examples = append(t.examples, example)
	return t
}

// WithMetadata 设置元数据
func (t *Template) WithMetadata(metadata TemplateMetadata) *Template {
	t.metadata = metadata
	return t
}

// Render 渲染模板
func (t *Template) Render(ctx context.Context, vars map[string]any) (string, error) {
	// 验证必填变量
	if err := t.Validate(vars); err != nil {
		return "", err
	}

	// 合并可选变量的默认值
	merged := make(map[string]any)
	for k, v := range t.optional {
		merged[k] = v
	}
	for k, v := range vars {
		merged[k] = v
	}

	// 渲染模板
	result := t.template

	// 替换变量（支持 {{.variable}} 和 {{variable}} 两种语法）
	re := regexp.MustCompile(`\{\{\.?([a-zA-Z_][a-zA-Z0-9_]*)\}\}`)
	result = re.ReplaceAllStringFunc(result, func(match string) string {
		// 提取变量名
		varName := re.FindStringSubmatch(match)[1]

		// 查找变量值
		if val, ok := merged[varName]; ok {
			return fmt.Sprintf("%v", val)
		}

		// 变量未找到，保持原样
		return match
	})

	// 添加少样本示例（如果有）
	if len(t.examples) > 0 {
		examplesText := t.renderExamples()
		result = examplesText + "\n\n" + result
	}

	return result, nil
}

// Validate 验证变量
func (t *Template) Validate(vars map[string]any) error {
	// 检查必填变量
	for _, req := range t.required {
		if _, ok := vars[req]; !ok {
			return fmt.Errorf("缺少必填变量: %s", req)
		}
	}

	return nil
}

// GetRequiredVars 获取必填变量列表
func (t *Template) GetRequiredVars() []string {
	result := make([]string, len(t.required))
	copy(result, t.required)
	return result
}

// GetOptionalVars 获取可选变量及默认值
func (t *Template) GetOptionalVars() map[string]any {
	result := make(map[string]any)
	for k, v := range t.optional {
		result[k] = v
	}
	return result
}

// renderExamples 渲染少样本示例
func (t *Template) renderExamples() string {
	if len(t.examples) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("以下是一些示例：\n\n")

	for i, example := range t.examples {
		sb.WriteString(fmt.Sprintf("示例 %d:\n", i+1))

		if example.Description != "" {
			sb.WriteString(fmt.Sprintf("描述: %s\n", example.Description))
		}

		sb.WriteString("输入:\n")
		for k, v := range example.Input {
			sb.WriteString(fmt.Sprintf("  %s: %v\n", k, v))
		}

		sb.WriteString("输出:\n")
		sb.WriteString(example.Output)
		sb.WriteString("\n\n")
	}

	return sb.String()
}

// TemplateBuilder 模板构建器（流式 API）
type TemplateBuilder struct {
	template *Template
}

// NewTemplateBuilder 创建模板构建器
func NewTemplateBuilder(name string) *TemplateBuilder {
	return &TemplateBuilder{
		template: NewTemplate(name, ""),
	}
}

// WithContent 设置模板内容
func (b *TemplateBuilder) WithContent(content string) *TemplateBuilder {
	b.template.template = content
	return b
}

// WithRequired 添加必填变量
func (b *TemplateBuilder) WithRequired(vars ...string) *TemplateBuilder {
	b.template.WithRequired(vars...)
	return b
}

// WithOptional 添加可选变量
func (b *TemplateBuilder) WithOptional(key string, defaultValue any) *TemplateBuilder {
	b.template.WithOptional(key, defaultValue)
	return b
}

// WithExample 添加示例
func (b *TemplateBuilder) WithExample(example Example) *TemplateBuilder {
	b.template.WithExample(example)
	return b
}

// WithMetadata 设置元数据
func (b *TemplateBuilder) WithMetadata(metadata TemplateMetadata) *TemplateBuilder {
	b.template.WithMetadata(metadata)
	return b
}

// Build 构建模板
func (b *TemplateBuilder) Build() *Template {
	return b.template
}

// TemplateRegistry 模板注册表
type TemplateRegistry interface {
	// Register 注册模板
	Register(name string, template PromptTemplate) error

	// Get 获取模板
	Get(name string) (PromptTemplate, error)

	// List 列出所有模板名称
	List() []string

	// Exists 检查模板是否存在
	Exists(name string) bool

	// Delete 删除模板
	Delete(name string) error
}

// templateRegistry 模板注册表实现
type templateRegistry struct {
	templates map[string]PromptTemplate
}

// NewTemplateRegistry 创建模板注册表
func NewTemplateRegistry() TemplateRegistry {
	return &templateRegistry{
		templates: make(map[string]PromptTemplate),
	}
}

// Register 注册模板
func (r *templateRegistry) Register(name string, template PromptTemplate) error {
	if name == "" {
		return fmt.Errorf("模板名称不能为空")
	}

	if template == nil {
		return fmt.Errorf("模板不能为 nil")
	}

	r.templates[name] = template
	return nil
}

// Get 获取模板
func (r *templateRegistry) Get(name string) (PromptTemplate, error) {
	template, ok := r.templates[name]
	if !ok {
		return nil, fmt.Errorf("模板不存在: %s", name)
	}
	return template, nil
}

// List 列出所有模板名称
func (r *templateRegistry) List() []string {
	names := make([]string, 0, len(r.templates))
	for name := range r.templates {
		names = append(names, name)
	}
	return names
}

// Exists 检查模板是否存在
func (r *templateRegistry) Exists(name string) bool {
	_, ok := r.templates[name]
	return ok
}

// Delete 删除模板
func (r *templateRegistry) Delete(name string) error {
	if !r.Exists(name) {
		return fmt.Errorf("模板不存在: %s", name)
	}
	delete(r.templates, name)
	return nil
}

// CompositeTemplate 组合模板
// 允许将多个模板组合成一个完整的提示词
type CompositeTemplate struct {
	name      string
	templates []PromptTemplate
	separator string
}

// NewCompositeTemplate 创建组合模板
func NewCompositeTemplate(name string, separator string) *CompositeTemplate {
	if separator == "" {
		separator = "\n\n"
	}

	return &CompositeTemplate{
		name:      name,
		templates: make([]PromptTemplate, 0),
		separator: separator,
	}
}

// AddTemplate 添加子模板
func (c *CompositeTemplate) AddTemplate(template PromptTemplate) *CompositeTemplate {
	c.templates = append(c.templates, template)
	return c
}

// Render 渲染组合模板
func (c *CompositeTemplate) Render(ctx context.Context, vars map[string]any) (string, error) {
	// 验证所有子模板
	if err := c.Validate(vars); err != nil {
		return "", err
	}

	// 渲染所有子模板
	parts := make([]string, 0, len(c.templates))
	for _, template := range c.templates {
		rendered, err := template.Render(ctx, vars)
		if err != nil {
			return "", fmt.Errorf("渲染子模板失败: %w", err)
		}
		parts = append(parts, rendered)
	}

	return strings.Join(parts, c.separator), nil
}

// Validate 验证变量
func (c *CompositeTemplate) Validate(vars map[string]any) error {
	for i, template := range c.templates {
		if err := template.Validate(vars); err != nil {
			return fmt.Errorf("子模板 %d 验证失败: %w", i, err)
		}
	}
	return nil
}

// GetRequiredVars 获取所有子模板的必填变量
func (c *CompositeTemplate) GetRequiredVars() []string {
	varMap := make(map[string]bool)
	for _, template := range c.templates {
		for _, v := range template.GetRequiredVars() {
			varMap[v] = true
		}
	}

	result := make([]string, 0, len(varMap))
	for v := range varMap {
		result = append(result, v)
	}
	return result
}

// GetOptionalVars 获取所有子模板的可选变量
func (c *CompositeTemplate) GetOptionalVars() map[string]any {
	result := make(map[string]any)
	for _, template := range c.templates {
		for k, v := range template.GetOptionalVars() {
			// 后面的模板优先级更高
			result[k] = v
		}
	}
	return result
}
