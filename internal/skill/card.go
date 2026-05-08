// Package skill 负责加载 SKILL.md（frontmatter + 正文），返回 Card 描述。
//
// frontmatter 字段（CC 风格，按需声明）：
//   - name                          机器 ID（kebab-case 与文件夹名一致，如 vuln/web/bac）
//   - description                   LLM 用此描述自动发现 skill；进 main system prompt catalog
//   - requires_auth                 触发元数据：true → 仅 carries_auth=true 流量可派；缺省/false 不限制
//   - applicable_param_locations    触发元数据：非空 → 流量 param_locations 必须有交集才派；缺省/空 不限制
//
// 触发元数据让主 ReAct 看 catalog + 流量 facts 自主决策，不再依赖 classify_traffic 的 required_skills。
// builder 仍是工具集 / done_validator 的真理来源，frontmatter 只描述"我何时适用"。
package skill

// Card 是 SKILL.md 的内存形态：frontmatter + 正文 Body。
type Card struct {
	Name                     string   `yaml:"name"`
	Description              string   `yaml:"description"`
	RequiresAuth             bool     `yaml:"requires_auth,omitempty"`
	ApplicableParamLocations []string `yaml:"applicable_param_locations,omitempty"`
	Body                     string   `yaml:"-"`
}
