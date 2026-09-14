package core

import (
	"context"
	"strings"
	"testing"
)

func TestTemplateRender(t *testing.T) {
	ctx := context.Background()

	t.Run("基本变量替换", func(t *testing.T) {
		template := NewTemplate("test", "你好，{{.name}}！今天天气{{.weather}}。")

		result, err := template.Render(ctx, map[string]any{
			"name":    "张三",
			"weather": "晴朗",
		})

		if err != nil {
			t.Fatalf("渲染失败: %v", err)
		}

		expected := "你好，张三！今天天气晴朗。"
		if result != expected {
			t.Errorf("期望: %s, 实际: %s", expected, result)
		}
	})

	t.Run("支持无点号语法", func(t *testing.T) {
		template := NewTemplate("test", "Hello, {{name}}!")

		result, err := template.Render(ctx, map[string]any{
			"name": "Alice",
		})

		if err != nil {
			t.Fatalf("渲染失败: %v", err)
		}

		if !strings.Contains(result, "Alice") {
			t.Error("应该包含 Alice")
		}
	})

	t.Run("必填变量验证", func(t *testing.T) {
		template := NewTemplate("test", "目标: {{.target}}")
		template.WithRequired("target")

		_, err := template.Render(ctx, map[string]any{})
		if err == nil {
			t.Error("应该返回缺少必填变量的错误")
		}
	})

	t.Run("可选变量及默认值", func(t *testing.T) {
		template := NewTemplate("test", "端口: {{.port}}")
		template.WithOptional("port", 8080)

		result, err := template.Render(ctx, map[string]any{})
		if err != nil {
			t.Fatalf("渲染失败: %v", err)
		}

		if !strings.Contains(result, "8080") {
			t.Error("应该使用默认值 8080")
		}
	})

	t.Run("可选变量覆盖默认值", func(t *testing.T) {
		template := NewTemplate("test", "端口: {{.port}}")
		template.WithOptional("port", 8080)

		result, err := template.Render(ctx, map[string]any{
			"port": 3000,
		})

		if err != nil {
			t.Fatalf("渲染失败: %v", err)
		}

		if !strings.Contains(result, "3000") {
			t.Error("应该使用提供的值 3000")
		}
	})

	t.Run("少样本示例", func(t *testing.T) {
		template := NewTemplate("test", "请分析: {{.input}}")
		template.WithExample(Example{
			Input: map[string]any{
				"input": "192.168.1.1",
			},
			Output:      "这是一个内网 IP 地址",
			Description: "IP 地址分析",
		})

		result, err := template.Render(ctx, map[string]any{
			"input": "10.0.0.1",
		})

		if err != nil {
			t.Fatalf("渲染失败: %v", err)
		}

		// 应该包含示例
		if !strings.Contains(result, "示例") {
			t.Error("应该包含少样本示例")
		}

		if !strings.Contains(result, "192.168.1.1") {
			t.Error("应该包含示例输入")
		}

		if !strings.Contains(result, "内网 IP 地址") {
			t.Error("应该包含示例输出")
		}
	})
}

func TestTemplateBuilder(t *testing.T) {
	ctx := context.Background()

	t.Run("流式 API 构建模板", func(t *testing.T) {
		template := NewTemplateBuilder("exploit").
			WithContent("目标: {{.target}}, 漏洞: {{.vuln}}").
			WithRequired("target", "vuln").
			WithOptional("timeout", "30s").
			WithMetadata(TemplateMetadata{
				Version:     "1.0",
				Description: "漏洞利用模板",
				Tags:        []string{"security", "exploit"},
			}).
			Build()

		result, err := template.Render(ctx, map[string]any{
			"target": "192.168.1.100",
			"vuln":   "SQL Injection",
		})

		if err != nil {
			t.Fatalf("渲染失败: %v", err)
		}

		if !strings.Contains(result, "192.168.1.100") {
			t.Error("应该包含目标")
		}

		if !strings.Contains(result, "SQL Injection") {
			t.Error("应该包含漏洞")
		}
	})
}

func TestTemplateRegistry(t *testing.T) {
	ctx := context.Background()

	t.Run("注册和获取模板", func(t *testing.T) {
		registry := NewTemplateRegistry()

		template := NewTemplate("test", "Hello, {{.name}}!")

		err := registry.Register("greeting", template)
		if err != nil {
			t.Fatalf("注册失败: %v", err)
		}

		retrieved, err := registry.Get("greeting")
		if err != nil {
			t.Fatalf("获取失败: %v", err)
		}

		result, _ := retrieved.Render(ctx, map[string]any{"name": "World"})
		if !strings.Contains(result, "World") {
			t.Error("获取的模板应该可用")
		}
	})

	t.Run("列出所有模板", func(t *testing.T) {
		registry := NewTemplateRegistry()

		_ = registry.Register("t1", NewTemplate("t1", ""))
		_ = registry.Register("t2", NewTemplate("t2", ""))

		names := registry.List()
		if len(names) != 2 {
			t.Errorf("期望 2 个模板，实际 %d 个", len(names))
		}
	})

	t.Run("检查模板存在", func(t *testing.T) {
		registry := NewTemplateRegistry()
		_ = registry.Register("test", NewTemplate("test", ""))

		if !registry.Exists("test") {
			t.Error("模板应该存在")
		}

		if registry.Exists("nonexistent") {
			t.Error("模板不应该存在")
		}
	})

	t.Run("删除模板", func(t *testing.T) {
		registry := NewTemplateRegistry()
		_ = registry.Register("test", NewTemplate("test", ""))

		err := registry.Delete("test")
		if err != nil {
			t.Fatalf("删除失败: %v", err)
		}

		if registry.Exists("test") {
			t.Error("删除后模板不应该存在")
		}
	})

	t.Run("注册空名称模板", func(t *testing.T) {
		registry := NewTemplateRegistry()

		err := registry.Register("", NewTemplate("", ""))
		if err == nil {
			t.Error("不应该允许注册空名称模板")
		}
	})

	t.Run("注册 nil 模板", func(t *testing.T) {
		registry := NewTemplateRegistry()

		err := registry.Register("test", nil)
		if err == nil {
			t.Error("不应该允许注册 nil 模板")
		}
	})
}

func TestCompositeTemplate(t *testing.T) {
	ctx := context.Background()

	t.Run("组合多个模板", func(t *testing.T) {
		systemPrompt := NewTemplate("system", "你是一个{{.role}}专家。")
		taskPrompt := NewTemplate("task", "请分析目标：{{.target}}")

		composite := NewCompositeTemplate("full", "\n\n")
		composite.AddTemplate(systemPrompt)
		composite.AddTemplate(taskPrompt)

		result, err := composite.Render(ctx, map[string]any{
			"role":   "安全测试",
			"target": "192.168.1.100",
		})

		if err != nil {
			t.Fatalf("渲染失败: %v", err)
		}

		if !strings.Contains(result, "安全测试") {
			t.Error("应该包含角色")
		}

		if !strings.Contains(result, "192.168.1.100") {
			t.Error("应该包含目标")
		}
	})

	t.Run("验证组合模板的所有必填变量", func(t *testing.T) {
		t1 := NewTemplate("t1", "{{.var1}}")
		t1.WithRequired("var1")

		t2 := NewTemplate("t2", "{{.var2}}")
		t2.WithRequired("var2")

		composite := NewCompositeTemplate("comp", "\n")
		composite.AddTemplate(t1)
		composite.AddTemplate(t2)

		// 缺少 var2
		_, err := composite.Render(ctx, map[string]any{
			"var1": "value1",
		})

		if err == nil {
			t.Error("应该返回缺少必填变量的错误")
		}
	})

	t.Run("获取组合模板的必填变量", func(t *testing.T) {
		t1 := NewTemplate("t1", "{{.var1}}")
		t1.WithRequired("var1")

		t2 := NewTemplate("t2", "{{.var2}}")
		t2.WithRequired("var2")

		composite := NewCompositeTemplate("comp", "\n")
		composite.AddTemplate(t1)
		composite.AddTemplate(t2)

		required := composite.GetRequiredVars()

		if len(required) != 2 {
			t.Errorf("期望 2 个必填变量，实际 %d 个", len(required))
		}
	})
}

func TestTemplateMetadata(t *testing.T) {
	t.Run("设置和获取元数据", func(t *testing.T) {
		template := NewTemplate("test", "content")
		template.WithMetadata(TemplateMetadata{
			Version:     "2.0",
			Description: "测试模板",
			Author:      "测试",
			Tags:        []string{"test", "example"},
		})

		if template.metadata.Version != "2.0" {
			t.Error("版本应该是 2.0")
		}

		if template.metadata.Description != "测试模板" {
			t.Error("描述应该正确")
		}

		if len(template.metadata.Tags) != 2 {
			t.Error("标签数量应该是 2")
		}
	})
}

func TestComplexTemplate(t *testing.T) {
	ctx := context.Background()

	t.Run("渗透测试场景模板", func(t *testing.T) {
		template := NewTemplateBuilder("pentest").
			WithContent(`你是一个渗透测试专家。

目标系统：{{.target}}
目标端口：{{.port}}
已知漏洞：{{.vulnerability}}

请给出详细的利用步骤。`).
			WithRequired("target", "vulnerability").
			WithOptional("port", 80).
			WithExample(Example{
				Input: map[string]any{
					"target":        "192.168.1.100",
					"port":          8080,
					"vulnerability": "SQL Injection",
				},
				Output: `步骤：
1. 识别注入点
2. 测试 payload
3. 提取数据`,
				Description: "SQL 注入利用示例",
			}).
			WithMetadata(TemplateMetadata{
				Version:     "1.0",
				Description: "渗透测试提示词模板",
				Tags:        []string{"security", "pentest"},
			}).
			Build()

		result, err := template.Render(ctx, map[string]any{
			"target":        "example.com",
			"vulnerability": "XSS",
		})

		if err != nil {
			t.Fatalf("渲染失败: %v", err)
		}

		if !strings.Contains(result, "example.com") {
			t.Error("应该包含目标")
		}

		if !strings.Contains(result, "XSS") {
			t.Error("应该包含漏洞")
		}

		if !strings.Contains(result, "80") {
			t.Error("应该使用默认端口 80")
		}

		if !strings.Contains(result, "示例") {
			t.Error("应该包含少样本示例")
		}
	})
}
