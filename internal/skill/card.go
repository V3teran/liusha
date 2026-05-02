// Package skill 负责加载 SKILL.md（frontmatter + 正文），返回 Card 描述。
//
// CC 风格精简 frontmatter（v1.1 末次裁剪，4 字段）：
//   - name            机器 ID（kebab-case 与文件夹名一致，如 vuln-web-bac）
//   - description     LLM 用此描述自动发现 skill
//   - allowed-tools   白名单（替代 required_actions，与 CC 语义一致）
//   - done_validator  liusha 特有 done 校验 key
//
// 已删除字段（理由）：
//   - applies_to        信息可融入 description；catalog 注入主 prompt 已涵盖
//   - budget            硬编码于 SKILL.md 不灵活；改为代码默认 + config.yaml 覆盖
//   - cognitive_map     与 body 内容冗余、token 浪费、非业界主流
package skill

// Card 是 SKILL.md 的内存形态：frontmatter + 正文 Body。
//
// 字段语义：
//   - Description    主 LLM 用此描述自动发现 skill；进 main system prompt catalog
//   - AllowedTools   白名单（CC 风格）：启动期与 tool.Registry cross-check（缺工具 fail-fast）
//   - DoneValidator  done_validate 中间件按 key 取 validator
type Card struct {
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"`
	AllowedTools  []string `yaml:"allowed-tools"`
	DoneValidator string   `yaml:"done_validator"`
	Body          string   `yaml:"-"`
}
