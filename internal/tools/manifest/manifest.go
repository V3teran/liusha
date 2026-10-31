// Package manifest 解析 deployments/tool-images/pentools/tools.yaml。
//
// 设计哲学：**工具是否存在于沙箱**与**工具是否有详细手册**独立。
//   - 本 ToolManifest 是"工具是否存在"的真实来源——和 Dockerfile 装的 binary 严格对应
//   - skills/tool/<name>/SKILL.md 是"是否有详细手册"——独立可选
//
// agent 在每个 turn 把本 ToolManifest 渲染成 tooling_catalog 段塞进 SystemPrompt（Tier 1 索引），
// LLM 看到全集就知道"沙箱有哪些工具"。详细手册仍走 read_skill(name) 按需读 SKILL.md。
package manifest

import (
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
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

// Load 从 yaml 文件加载 ToolManifest；返回 error 时调用方应 fail-fast——
// 工具清单缺失会让 LLM 看不到沙箱有什么工具，agent 无法 ReAct 决策。
func Load(path string) (*ToolManifest, error) {
	data, err := os.ReadFile(path) // #nosec G304 // 路径来自进程配置/种子目录，非用户输入
	if err != nil {
		return nil, fmt.Errorf("读取 tools manifest %q: %w", path, err)
	}
	var m ToolManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("解析 tools manifest %q: %w", path, err)
	}
	if len(m.Tools) == 0 {
		return nil, fmt.Errorf("tools manifest %q 不含任何工具（tools 列表为空）", path)
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

// FilterByNames 按 agent cli_tools 严格白名单过滤工具目录，返回新 ToolManifest（不改原实例——不可变）。
//   - names 为空 = 空集，该 agent 不装配任何外部 CLI 工具（严格白名单，所选即所得）；
//   - names 非空 = 只保留名字在白名单里的工具（agent 专精：只给它这几把刀）。
//
// 白名单里不存在的名字静默忽略（配置漂移不致命）。
// Categories 元数据保持不变（过滤不影响分类定义）。
func (m *ToolManifest) FilterByNames(names []string) *ToolManifest {
	if len(names) == 0 {
		return &ToolManifest{Categories: m.Categories, Tools: []Tool{}}
	}
	allow := make(map[string]struct{}, len(names))
	for _, n := range names {
		allow[n] = struct{}{}
	}
	out := make([]Tool, 0, len(names))
	for _, t := range m.Tools {
		if _, ok := allow[t.Name]; ok {
			out = append(out, t)
		}
	}
	return &ToolManifest{Categories: m.Categories, Tools: out}
}

// CategoryOrder 返回分类的渲染顺序（按 order 字段排序）。
// 未在 categories 中定义的分类会被追加到末尾（字典序）。
func (m *ToolManifest) CategoryOrder() []string {
	// 构建 code -> order 映射
	orderMap := make(map[string]int, len(m.Categories))
	for _, cat := range m.Categories {
		orderMap[cat.Code] = cat.Order
	}

	// 收集所有使用中的分类
	usedCategories := make(map[string]bool)
	for _, t := range m.Tools {
		usedCategories[t.Category] = true
	}

	// 分为两组：有序的和未定义的
	var ordered []string
	var undefined []string
	for cat := range usedCategories {
		if _, ok := orderMap[cat]; ok {
			ordered = append(ordered, cat)
		} else {
			undefined = append(undefined, cat)
		}
	}

	// 按 order 排序已定义的
	sort.Slice(ordered, func(i, j int) bool {
		return orderMap[ordered[i]] < orderMap[ordered[j]]
	})

	// 未定义的按字典序排序
	sort.Strings(undefined)

	return append(ordered, undefined...)
}

// CategoryName 返回分类的显示名称。
// 如果分类未在 categories 中定义，返回 code 本身。
func (m *ToolManifest) CategoryName(code string) string {
	for _, cat := range m.Categories {
		if cat.Code == code {
			return cat.Name
		}
	}
	return code
}
