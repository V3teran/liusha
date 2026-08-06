// Package manifest 解析 deployments/tool-images/pentools/tools.yaml。
//
// 设计哲学：**工具是否存在于沙箱**与**工具是否有详细手册**独立。
//   - 本 Manifest 是"工具是否存在"的真实来源——和 Dockerfile 装的 binary 严格对应
//   - skills/tooling/<name>/SKILL.md 是"是否有详细手册"——独立可选
//
// hunter 在每个 turn 把本 Manifest 渲染成 tooling_catalog 段塞进 SystemPrompt（Tier 1 索引），
// LLM 看到全集就知道"沙箱有哪些工具"。详细手册仍走 read_tooling_skill(name) 按需读 SKILL.md。
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
	Name        string `yaml:"name"`
	Category    string `yaml:"category"`
	Description string `yaml:"description"`
}

// Manifest 是 tools.yaml 解析后的全集。
type Manifest struct {
	Tools []Tool `yaml:"tools"`
}

// Load 从 yaml 文件加载 Manifest；返回 error 时调用方应 fail-fast——
// 工具清单缺失会让 LLM 看不到沙箱有什么工具，hunter 无法 ReAct 决策。
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 tools manifest %q: %w", path, err)
	}
	var m Manifest
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
func (m *Manifest) ByCategory() map[string][]Tool {
	out := make(map[string][]Tool, 8)
	for _, t := range m.Tools {
		out[t.Category] = append(out[t.Category], t)
	}
	for k := range out {
		sort.Slice(out[k], func(i, j int) bool { return out[k][i].Name < out[k][j].Name })
	}
	return out
}

// Names 返回所有工具名（字典序）——供 read_tooling_skill / catalog lint 等使用。
func (m *Manifest) Names() []string {
	out := make([]string, 0, len(m.Tools))
	for _, t := range m.Tools {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}

// FilterByNames 按猎手 cli_tools 严格白名单过滤工具目录，返回新 Manifest（不改原实例——不可变）。
//   - names 为空 = 空集，该猎手不装配任何外部 CLI 工具（严格白名单，所选即所得）；
//   - names 非空 = 只保留名字在白名单里的工具（猎手专精：只给它这几把刀）。
//
// 白名单里不存在的名字静默忽略（配置漂移不致命）。
func (m *Manifest) FilterByNames(names []string) *Manifest {
	if len(names) == 0 {
		return &Manifest{Tools: []Tool{}}
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
	return &Manifest{Tools: out}
}
