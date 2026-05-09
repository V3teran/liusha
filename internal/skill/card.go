// Package skill 负责加载 SKILL.md（frontmatter + 正文），返回 Card 描述。
//
// v0024 agentic-lean：单层 hunter agent 架构——只剩 skills/hunter/SKILL.md 一个 SKILL。
// frontmatter 必填字段：
//   - name                          机器 ID（与文件夹名一致，如 hunter）
//   - description                   一句话描述（保留供未来多 skill 时进 catalog 用）
//
// RequiresAuth / ApplicableParamLocations 为旧路由元数据保留备用，hunter 单层架构不读。
package skill

// Card 是 SKILL.md 的内存形态：frontmatter + 正文 Body。
type Card struct {
	Name                     string   `yaml:"name"`
	Description              string   `yaml:"description"`
	RequiresAuth             bool     `yaml:"requires_auth,omitempty"`
	ApplicableParamLocations []string `yaml:"applicable_param_locations,omitempty"`
	Body                     string   `yaml:"-"`
}
