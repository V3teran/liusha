// Package skill 负责加载 SKILL.md（frontmatter + 正文），返回 Card 描述。
//
// 极简 frontmatter（仅 2 字段）：
//   - name         机器 ID（kebab-case 与文件夹名一致，如 vuln-web-bac）
//   - description  LLM 用此描述自动发现 skill；进 main system prompt catalog
//
// 已删除字段（builder 是唯一真理来源，frontmatter 重复声明已精简）：
//   - allowed-tools     builder.Register 决定运行时可用工具，frontmatter 重复
//   - done_validator    builder.NewBACValidator 直接装配实例，frontmatter 重复
//   - applies_to / budget / cognitive_map / required_actions   v1.1 早期已精简
package skill

// Card 是 SKILL.md 的内存形态：frontmatter + 正文 Body。
type Card struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Body        string `yaml:"-"`
}
