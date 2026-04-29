// Package skill 负责加载 SKILL.md（frontmatter + 正文），返回 Card 描述。
//
// 黑客松借鉴：
//   - frontmatter 新增 done_validator（在 ActionRegistry 找对应 DoneValidator，找不到报错）
//   - frontmatter 新增 cognitive_map（loader 读文件，校验是否含 6 槽位标题）
package skill

// AppliesEntry 描述一条 applies_to 项，目前仅 role。
type AppliesEntry struct {
	Role string `yaml:"role"`
}

// Budget 限制 Skill 单次执行的步数与 token 总量。
type Budget struct {
	MaxSteps  int `yaml:"max_steps"`
	MaxTokens int `yaml:"max_tokens"`
}

// Card 是 SKILL.md 的内存形态：frontmatter + 正文 Body。
//
// Body 在加载时被注入 system prompt；其余字段由 runtime 在执行时使用：
//   - RequiredActions：启动校验所有 action 已注册
//   - DoneValidator：done_validate 中间件按 key 取出对应 validator
//   - CognitiveMap：用于在 system prompt 中引用 6 槽位地图
type Card struct {
	Name            string         `yaml:"name"`
	Description     string         `yaml:"description"`
	AppliesTo       []AppliesEntry `yaml:"applies_to"`
	Budget          Budget         `yaml:"budget"`
	RequiredActions []string       `yaml:"required_actions"`
	DoneValidator   string         `yaml:"done_validator"`
	CognitiveMap    string         `yaml:"cognitive_map"`
	Body            string         `yaml:"-"`
}
