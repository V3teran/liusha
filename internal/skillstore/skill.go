// Package skillstore 定义技能的模型与持久化。
//
// 技能一张表（skill），一行一个技能：
//   - 内置技能：出厂内容编译在 internal/builtin，是种子——迁移后由出厂同步（SyncBuiltins）写入；
//     用户可编辑、停用、恢复出厂、删除。seed_hash 记最近一次写入的出厂内容的哈希，当前内容的
//     哈希与它相等即未改过，升级时跟随新版（见 internal/builtinsync）。删除只记删除标记
//     （deleted_at）：出厂同步不再补回，用户可恢复成当前出厂内容。
//   - 自建技能：用户新建的数据（is_builtin = false），可增删改；删除即删行。
//
// 同 code 时只有一行：自建技能占用了某内置技能的 code 时内置版本被遮蔽；新建自建技能拒绝与
// 内置技能同名。
package skillstore

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/V3teran/liusha/internal/builtin"
	"github.com/V3teran/liusha/internal/builtinsync"
	"github.com/V3teran/liusha/internal/override"
)

// Skill 是一个技能。
type Skill struct {
	Code        string `json:"code"`     // 寻址名：agent.skills 白名单与 read_skill 均用它
	Category    string `json:"category"` // 知识域：vuln（漏洞知识）/ tool（工具使用知识）
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Enabled     bool   `json:"enabled"`
	IsBuiltin   bool   `json:"is_builtin"`
}

// Record 是表里的一行：技能本身 + 同步基线。
type Record struct {
	Skill
	SeedHash *string `json:"seed_hash"` // 最近一次写入的出厂内容的哈希；nil = 自建或基线未知
	Deleted  bool    `json:"deleted"`   // 内置技能被用户删除（留删除标记，可恢复）
}

// Content 是技能可修改的字段。
type Content struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Enabled     bool   `json:"enabled"`
}

var codePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Schema 返回技能的可修改字段表：由 Content 的字段推导（全部为整值）。
func Schema() override.Schema {
	return override.SchemaOf(Content{})
}

// Defaults 返回内置技能的出厂内容（出厂即启用）。
func Defaults(b builtin.Skill) Content {
	return Content{Name: b.Name, Description: b.Description, Body: b.Body, Enabled: true}
}

// SeedHash 返回内置技能出厂内容的哈希。
func SeedHash(b builtin.Skill) string {
	return builtinsync.Hash(b.Name, b.Description, b.Body)
}

// ContentOf 取技能的可修改部分。
func ContentOf(s Skill) Content {
	return Content{Name: s.Name, Description: s.Description, Body: s.Body, Enabled: s.Enabled}
}

// Hash 返回技能当前内容的哈希（与出厂哈希同一算式）。
func (s Skill) Hash() string {
	return builtinsync.Hash(s.Name, s.Description, s.Body)
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
