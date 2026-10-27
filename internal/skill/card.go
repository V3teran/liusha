// Package skill 负责加载 SKILL.md（frontmatter + 正文），返回 Card 描述。
//
// skill 是 agent 可声明加载的操作手册（agent.skills 白名单 → system prompt 技能索引，
// 正文经 read_skill 按需拉取），章程本体在 agent 表（种子 = agents/*.md）。
// frontmatter 必填字段：
//   - name                          机器 ID（与文件夹名一致，如 agent）
//   - description                   一句话描述（保留供未来多 skill 时进 catalog 用）
//
// RequiresAuth / ApplicableParamLocations 为旧路由元数据保留备用，agent 单层架构不读。
package skill

// Card 是 SKILL.md 的内存形态：frontmatter + 正文 Body。
type Card struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	// Key 是 skill 的寻址名（= SKILL.md 所在目录名，如 dom-xss / browser-use），
	// 由 Loader 回填，非 frontmatter 字段。agent.skills 声明与 read_skill(name)
	// 都用 Key；frontmatter name 只是给人看的显示名，禁止用于寻址——两者混用
	// 会让 LLM 拿显示名去读 skill 而必败。
	Key string `yaml:"-"`

	// Category 是技能的领域归类：种子技能取目录名（tooling / vuln），自建技能由
	// 录入填写。消费方两处——前端页面徽章展示、Tier 1 索引标签（RenderIndex，
	// 帮 LLM 按领域对齐任务与技能）。注：tooling 工具清单（recon / vulnscan 等）
	// 的分组是 tools manifest 的 Category，与本字段无关，勿混写。
	Category string `yaml:"category,omitempty"`

	RequiresAuth             bool     `yaml:"requires_auth,omitempty"`
	ApplicableParamLocations []string `yaml:"applicable_param_locations,omitempty"`
	Body                     string   `yaml:"-"`
}
