// Package skill 负责加载 SKILL.md（frontmatter + 正文），返回 Card 描述。
//
// 极简 frontmatter（CC 风格，仅 2 字段）：
//   - name         机器 ID（kebab-case 与文件夹名一致，如 vuln/web/bac）
//   - description  LLM 用此描述自动发现 skill；进 main system prompt catalog
//
// 唯一真理来源：builder 决定运行时工具集与 done_validator，frontmatter 不重复声明。
package skill

// Card 是 SKILL.md 的内存形态：frontmatter + 正文 Body。
type Card struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Body        string `yaml:"-"`
}
