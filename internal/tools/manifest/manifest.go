// Package manifest 是沙盒 CLI 工具清单（tools.yaml，编进程序）。
//
// 三件事彼此独立：
//   - 本清单声明产品支持哪些 CLI 工具（名称、分类、用途），沙盒镜像按它安装
//   - 某台沙盒实际装了哪些，由沙盒按清单探测上报（二进制部署时取决于本机）
//   - 内置技能 internal/builtin/skills/tool/<name>/SKILL.md 是可选的详细手册
//
// agent 把按 cli_tools 白名单过滤后的 ToolManifest 渲染成 CLI 工具目录塞进系统提示词（Tier 1 索引），
// LLM 据此知道"沙箱里有哪些工具可用"。详细手册仍走 read_skill(name) 按需读 SKILL.md。
package manifest

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/V3teran/liusha/internal/registry"
)

// Tool 是单个沙箱预装工具的清单条目。
// 字段命名遵循 MCP / OpenAI Function Calling / OpenAPI 业界惯例（name + description）。
type Tool struct {
	Name        string   `yaml:"name"`
	Category    string   `yaml:"category"`
	Description string   `yaml:"description"`
	Examples    []string `yaml:"examples,omitempty"` // 可选：使用示例，供 agent 学习
}

// Category 是工具分类的元数据（显示名称和渲染顺序）。
type Category struct {
	Code  string `yaml:"code"`
	Name  string `yaml:"name"`
	Order int    `yaml:"order"`
}

// ToolManifest 是 tools.yaml 解析后的全集。
type ToolManifest struct {
	Categories []Category `yaml:"categories"`
	Tools      []Tool     `yaml:"tools"`
}

//go:embed tools.yaml
var defaultYAML []byte

// Default 返回内置工具清单（进程内只解析一次，调用方不得修改返回值）。
// 清单随程序发布、由单测守住合法性，解析失败属于构建缺陷，直接 panic。
var Default = sync.OnceValue(func() *ToolManifest {
	m, err := Parse(defaultYAML)
	if err != nil {
		panic(fmt.Sprintf("内置工具清单不合法: %v", err))
	}
	return m
})

// Parse 解析并校验工具清单。
func Parse(data []byte) (*ToolManifest, error) {
	var m ToolManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("解析工具清单: %w", err)
	}
	if len(m.Tools) == 0 {
		return nil, fmt.Errorf("工具清单不含任何工具（tools 列表为空）")
	}
	// 工具名是 cli_tools 名单与 run_command 的寻址键：缺名或重名都是清单错误。
	seen := make(map[string]bool, len(m.Tools))
	for i, t := range m.Tools {
		if strings.TrimSpace(t.Name) == "" {
			return nil, fmt.Errorf("工具清单第 %d 个工具缺少 name", i+1)
		}
		if seen[t.Name] {
			return nil, fmt.Errorf("工具清单中工具 %q 重复登记", t.Name)
		}
		seen[t.Name] = true
	}
	return &m, nil
}

// ByCategory 按 category 分桶；每桶内按 name 字典序——稳定 prompt 顺序，
// 利于 LLM provider 的 prompt cache 命中。
func (m *ToolManifest) ByCategory() map[string][]Tool {
	out := make(map[string][]Tool, 8)
	for _, t := range m.Tools {
		out[t.Category] = append(out[t.Category], t)
	}
	for k := range out {
		sort.Slice(out[k], func(i, j int) bool { return out[k][i].Name < out[k][j].Name })
	}
	return out
}

// CategoryByCode 返回按 code 索引的分类元数据（O(1) 查询）。
func (m *ToolManifest) CategoryByCode() map[string]Category {
	out := make(map[string]Category, len(m.Categories))
	for _, c := range m.Categories {
		out[c.Code] = c
	}
	return out
}

// OrderedCategories 返回按 order 字段排序的分类列表（用于渲染）。
func (m *ToolManifest) OrderedCategories() []Category {
	sorted := make([]Category, len(m.Categories))
	copy(sorted, m.Categories)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Order < sorted[j].Order
	})
	return sorted
}

// Names 返回所有工具名（字典序）——供 read_skill / catalog lint 等使用。
func (m *ToolManifest) Names() []string {
	out := make([]string, 0, len(m.Tools))
	for _, t := range m.Tools {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}

// FilterByNames 按 agent cli_tools 白名单过滤工具目录，返回新 ToolManifest（不改原实例——不可变）。
// 名单语义见 registry.Allows（nil / 空 = 空集，含 "*" = 全部，其余为严格白名单）；
// 白名单里不存在的名字静默忽略（配置漂移不致命）。Categories 元数据保持不变。
func (m *ToolManifest) FilterByNames(names []string) *ToolManifest {
	out := make([]Tool, 0, len(m.Tools))
	for _, t := range m.Tools {
		if registry.Allows(names, t.Name) {
			out = append(out, t)
		}
	}
	return &ToolManifest{Categories: m.Categories, Tools: out}
}
