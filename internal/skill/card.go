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

// Card 是 SKILL.md 的内存形态：frontmatter + 正文 Body + 关联文件内容。
//
// CC 风格 v1.1 改造（frontmatter 全部生效，不再装饰）：
//   - Description     主 LLM 用此描述自动发现 skill；进 main system prompt catalog
//   - Budget          真正驱动子 ReAct max_steps（builder 不再硬编码）
//   - RequiredActions 启动期与 tool.Registry cross-check（缺工具立即 fail-fast）
//   - DoneValidator   done_validate 中间件按 key 取 validator
//   - CognitiveMap    仅是路径；Loader 自动读文件内容到 CognitiveMapBody
//   - CognitiveMapBody cognitive_map.md 正文，由 builder 拼到 SystemPrompt 尾部
type Card struct {
	Name             string         `yaml:"name"`
	Description      string         `yaml:"description"`
	AppliesTo        []AppliesEntry `yaml:"applies_to"`
	Budget           Budget         `yaml:"budget"`
	RequiredActions  []string       `yaml:"required_actions"`
	DoneValidator    string         `yaml:"done_validator"`
	CognitiveMap     string         `yaml:"cognitive_map"`
	Body             string         `yaml:"-"`
	CognitiveMapBody string         `yaml:"-"`
}
