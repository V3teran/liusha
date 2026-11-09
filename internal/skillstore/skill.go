// Package skillstore 定义技能的生效模型与持久化。
//
// 技能有两种来源：
//   - 内置技能：出厂内容编译在 internal/builtin，用户只能逐字段自定义
//     （名称、描述、正文、启用），自定义存 skill_override；
//   - 自建技能：用户新建的数据，整行存 custom_skill。
//
// 同 code 时自建技能优先，内置版本被遮蔽；新建自建技能拒绝与内置技能同名。
package skillstore

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/V3teran/liusha/internal/builtin"
	"github.com/V3teran/liusha/internal/override"
)

// Skill 是一个生效技能。
type Skill struct {
	Code        string `json:"code"`     // 寻址名：agent.skills 白名单与 read_skill 均用它
	Category    string `json:"category"` // 知识域：vuln（漏洞知识）/ tool（工具使用知识）
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Enabled     bool   `json:"enabled"`
	IsBuiltin   bool   `json:"is_builtin"`
}

// Content 是技能可修改的字段：内置技能经用户层自定义，自建技能直接修改。
type Content struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Enabled     bool   `json:"enabled"`
}

// 可修改字段名。
const (
	FieldName        = "name"
	FieldDescription = "description"
	FieldBody        = "body"
	FieldEnabled     = "enabled"
)

var codePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Schema 返回技能的可自定义字段表：由 Content 的字段推导（全部为整值替换）。
func Schema() override.Schema {
	return override.SchemaOf(Content{})
}

// Defaults 返回内置技能的出厂内容（出厂即启用）。
func Defaults(b builtin.Skill) Content {
	return Content{Name: b.Name, Description: b.Description, Body: b.Body, Enabled: true}
}

// DefaultsDocument 返回出厂内容的文档形式。
func DefaultsDocument(b builtin.Skill) override.Document {
	return mustDocument(Defaults(b))
}

// Resolve 合成内置技能的生效值：出厂内容叠加用户层。
func Resolve(b builtin.Skill, ov override.Overrides) (Skill, error) {
	var c Content
	if err := override.FromDocument(override.Apply(Schema(), DefaultsDocument(b), ov), &c); err != nil {
		return Skill{}, fmt.Errorf("技能 %s: 合成生效值: %w", b.Code, err)
	}
	return Skill{
		Code: b.Code, Category: b.Category,
		Name: c.Name, Description: c.Description, Body: c.Body, Enabled: c.Enabled,
		IsBuiltin: true,
	}, nil
}

// ContentOf 取技能的可修改部分。
func ContentOf(s Skill) Content {
	return Content{Name: s.Name, Description: s.Description, Body: s.Body, Enabled: s.Enabled}
}

// ApplyContent 把内容写回技能（不修改入参）。
func ApplyContent(s Skill, c Content) Skill {
	s.Name, s.Description, s.Body, s.Enabled = c.Name, c.Description, c.Body, c.Enabled
	return s
}

// Validate 校验技能内容。
func Validate(c Content) error {
	var problems []string
	if strings.TrimSpace(c.Name) == "" {
		problems = append(problems, "名称不能为空")
	}
	if strings.TrimSpace(c.Body) == "" {
		problems = append(problems, "正文不能为空")
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w：%s", override.ErrInvalid, strings.Join(problems, "；"))
	}
	return nil
}

// ValidateNew 校验新建自建技能的标识与分类。
func ValidateNew(s Skill) error {
	if !codePattern.MatchString(s.Code) {
		return fmt.Errorf("%w：标识符须为小写字母、数字与连字符（1–64 位，字母或数字开头）", override.ErrInvalid)
	}
	if strings.TrimSpace(s.Category) == "" {
		return fmt.Errorf("%w：分类不能为空", override.ErrInvalid)
	}
	return Validate(ContentOf(s))
}

func mustDocument(v any) override.Document {
	d, err := override.ToDocument(v)
	if err != nil {
		panic(fmt.Sprintf("skillstore: 序列化失败: %v", err))
	}
	return d
}
