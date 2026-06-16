// Package skill 负责加载 SKILL.md（frontmatter + 正文），返回 Card 描述。
//
// hunter 小队架构：trafficAnalysis（reconnaissance）/ orchestrator（编排者）/ exploitation（exploitation）按 (mode, 是否 orchestrator) 拼 system_prompt_{trafficAnalysis,orchestrator,exploitation}.md。
// frontmatter 必填字段：
//   - name                          机器 ID（与文件夹名一致，如 hunter）
//   - description                   一句话描述（保留供未来多 skill 时进 catalog 用）
//
// RequiresAuth / ApplicableParamLocations 为旧路由元数据保留备用，hunter 单层架构不读。
package skill

// Card 是 SKILL.md 的内存形态：frontmatter + 正文 Body。
type Card struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	// Category 是 tooling 工具的领域归属（如 recon / discovery / vulnscan /
	// injection / deserialization / auth / sast / utility），只对 tooling 类有意义；vuln 类
	// SKILL 也留空（用 frontmatter category 字段做分组只对 tooling 有意义）。
	// hunter buildToolingCatalog 按本字段分组渲染 Tier 1 工具索引段。
	// 空值落入"未分类"组，渲染顺序最后。
	Category string `yaml:"category,omitempty"`

	RequiresAuth             bool     `yaml:"requires_auth,omitempty"`
	ApplicableParamLocations []string `yaml:"applicable_param_locations,omitempty"`
	Body                     string   `yaml:"-"`
}
