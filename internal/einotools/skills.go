package einotools

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/eino-contrib/jsonschema"

	"github.com/V3teran/liusha/internal/skill"
)

// SkillLoader 是 read_vuln_skill / read_tooling_skill 依赖的最小接口（*skill.Loader 自动满足）。
type SkillLoader interface {
	List() []*skill.Card
	Load(name string) (*skill.Card, error)
}

// skillNameArgs 是 read_*_skill 的入参；name 必填，运行期由 SchemaModifier 注入动态 enum。
type skillNameArgs struct {
	Name string `json:"name" jsonschema:"required,description=名称（必须在 user prompt 索引段列出，不要凭行业常识猜）"`
}

// skillDocOutput 是 LLM 看到的结构化结果。
type skillDocOutput struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

// buildSkillReader 是 read_vuln_skill / read_tooling_skill 共享装配逻辑（二者仅 name/desc/Loader root 不同）。
//
// 动态 enum（P6-1 设计）：name 取值集合来自 loader.List()，InferTool 的静态 struct tag
// 表达不了运行期 enum，故用 WithSchemaModifier 在 name 字段注入 Enum。
// 不支持 enum 的 provider 仍有 Load 失败兜底（错误带 available list）。
func buildSkillReader(loader SkillLoader, toolName, desc, kind string) (tool.BaseTool, error) {
	if loader == nil {
		return nil, fmt.Errorf("%s: Loader 未注入", toolName)
	}
	enum := make([]any, 0)
	for _, c := range loader.List() {
		enum = append(enum, c.Name)
	}

	return utils.InferTool(
		toolName, desc,
		func(_ context.Context, in skillNameArgs) (skillDocOutput, error) {
			if in.Name == "" {
				return skillDocOutput{}, errors.New("name 必填")
			}
			card, err := loader.Load(in.Name)
			if err != nil {
				// 兜底：错误附 available list，让不支持 enum 的 provider 失败一次即学到 catalog。
				var available []string
				for _, c := range loader.List() {
					available = append(available, c.Name)
				}
				return skillDocOutput{}, fmt.Errorf("%s %q 不在 catalog（available=%v）—— 必须从 available 选，不要凭行业常识猜", kind, in.Name, available)
			}
			return skillDocOutput{Name: in.Name, Body: card.Body}, nil
		},
		utils.WithSchemaModifier(func(jsonTagName string, _ reflect.Type, _ reflect.StructTag, s *jsonschema.Schema) {
			if jsonTagName == "name" {
				s.Enum = enum
			}
		}),
	)
}

// BuildReadVulnSkill 造原生 eino read_vuln_skill 工具（Loader root=skills/vuln）。
func BuildReadVulnSkill(loader SkillLoader) (tool.BaseTool, error) {
	return buildSkillReader(loader, "read_vuln_skill",
		"拉取一个漏洞类型的完整挖掘指南。"+
			"**使用场景**：判完流量方向、确定要挖哪一/哪几类漏洞 → "+
			"调本工具拿完整 SKILL.md（漏洞本质 / 挖掘方向 / 判定原则 / 误报排除 / 写 finding 红线）→ "+
			"再按指南调 run_command 实证。"+
			"**name 取值**：必须在 user prompt『可用漏洞挖掘指南索引』段列出，"+
			"**不要凭行业常识猜**（SKILL 库可能不完整，列表外的传过来直接报错）。",
		"漏洞类型")
}

// BuildReadToolingSkill 造原生 eino read_tooling_skill 工具（Loader root=skills/tooling）。
func BuildReadToolingSkill(loader SkillLoader) (tool.BaseTool, error) {
	return buildSkillReader(loader, "read_tooling_skill",
		"拉取一个外部 CLI 工具的完整使用手册。"+
			"**使用场景**：user prompt 末尾的『可用外部工具索引』看到某工具描述觉得对路 → "+
			"调本工具拿完整 SKILL.md（参数表 / 输出 grep 关键词 / 常见坑 / 写 finding 红线）→ "+
			"再调 run_command 跑命令。"+
			"**name 取值**：必须在 user prompt『可用外部工具索引』段列出，"+
			"**不要凭行业常识猜**（工具镜像不完整或未装；列表外的传过来直接报错）。",
		"工具名")
}
