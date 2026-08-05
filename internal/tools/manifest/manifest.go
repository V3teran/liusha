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
	// Scenarios 是交战域标签（多值，语义为「该扫描工具在哪些交战域可见」）。当前只有 web，全部工具标 [web]。
	// 扩展 ctf/cloud/container 时：跨域复用的工具追加标签（如 [web, ctf]）。
	// buildToolingCatalog 按当次 scenario.Domain 经 FilterByDomain 过滤渲染；空标签=通用工具全域可见（见 D11）。
	Scenarios []string `yaml:"scenarios"`
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

// FilterByDomain 按交战域过滤工具目录，返回新 Manifest（不改原实例——不可变）。
// 语义（见 D11/M7）：
//   - 工具 scenarios 标签是粗粒度交战域（web/ctf/cloud…），非具体 scenario code；
//   - 空 scenarios = 通用工具，任何域可见；
//   - domain 为空（场景未配置域）= 不过滤，返回全集副本。
//
// 过滤键是 scenario.Domain 而非 scenario.Code：多个具体场景共享同一 domain，
// 新增场景无需回头给每个工具补标签（工具与场景解耦，O(域) 维护量）。
func (m *Manifest) FilterByDomain(domain string) *Manifest {
	if domain == "" {
		return &Manifest{Tools: append([]Tool(nil), m.Tools...)}
	}
	out := make([]Tool, 0, len(m.Tools))
	for _, t := range m.Tools {
		if len(t.Scenarios) == 0 || containsDomain(t.Scenarios, domain) {
			out = append(out, t)
		}
	}
	return &Manifest{Tools: out}
}

// containsDomain 报告 domains 是否含 target（多值标签任一命中即可见）。
func containsDomain(domains []string, target string) bool {
	for _, d := range domains {
		if d == target {
			return true
		}
	}
	return false
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

// FilterByNames 按猎手 cli_tools 白名单过滤工具目录，返回新 Manifest（不改原实例——不可变）。
// 这是 domain 粗过滤（FilterByDomain）之上的第二级细过滤：
//   - names 为空 = 不过滤，返回全集副本（该猎手可见其交战域内的全部工具）；
//   - names 非空 = 只保留名字在白名单里的工具（猎手专精：只给它这几把刀）。
//
// 两级过滤的顺序是先 domain 后 names：FilterByDomain(domain).FilterByNames(cliTools)。
// 未匹配到任何白名单名的工具全部剔除；白名单里不存在的名字静默忽略（配置漂移不致命）。
func (m *Manifest) FilterByNames(names []string) *Manifest {
	if len(names) == 0 {
		return &Manifest{Tools: append([]Tool(nil), m.Tools...)}
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
