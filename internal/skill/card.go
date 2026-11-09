// Package skill 是技能的渐进式加载：agent 的 system prompt 只常驻技能索引
// （寻址名 + 名称 + 描述，Tier 1），正文由 read_skill 按需拉取（Tier 2）。
// 每个 agent 按其技能白名单得到独立视图。
package skill

// Card 是一个技能的加载形态。
type Card struct {
	// Key 是技能的寻址名（= 技能 code，如 bac / browser-use）。agent 技能白名单与
	// read_skill(name) 都用 Key；Name 只是给人看的显示名，不可用于寻址。
	Key         string
	Name        string
	Description string

	// Category 是技能的知识域（vuln = 漏洞知识 / tool = 工具使用知识），供管理页
	// 组织展示；不进入 LLM 侧索引（索引即 name + description）。
	Category string

	Body string
}
