// Package scenariostore 定义场景的模型与持久化。
//
// 场景是任务所处情境的说明（名称 + 说明 + 正文），建任务时快照到任务行、整份拼在各 agent
// 系统提示词末尾（见 internal/scenario）。场景一张表（scenario），一行一个场景，与技能同一模型：
//   - 内置场景：出厂内容编译在 internal/builtin/scenarios，是种子——迁移后由出厂同步写入；
//     可编辑、停用、恢复出厂、删除。删除只记删除标记（deleted_at）：出厂同步不再补回，用户可
//     恢复成当前出厂内容；
//   - 自建场景：用户新建的数据，可增删改；删除即删行。
//
// 定时模板只引用存在且启用的场景：场景被模板引用时不可删除、停用；场景的停用、删除与模板的
// 新建都先锁场景行再检查，彼此串行。
package scenariostore

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/V3teran/liusha/internal/builtin"
	"github.com/V3teran/liusha/internal/builtinsync"
	"github.com/V3teran/liusha/internal/override"
)

// DefaultCode 是缺省场景（渗透测试）：请求不指定场景时、以及代理捕获的任务用它。它和其它场景
// 一样可停用、删除——不可用时缺省即无场景（由用户的操作决定，界面上看得到），不另设兜底场景。
const DefaultCode = "pentest"

// 名称与正文的长度上限：两者都进四个 agent 的系统提示词（名称在场景块的标签上），限长防止
// 一份场景吃掉上下文预算。说明只给人看，不进提示词。
const (
	MaxNameRunes = 64
	MaxBodyRunes = 8000
)

// Scenario 是一个场景。
type Scenario struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Enabled     bool   `json:"enabled"`
	IsBuiltin   bool   `json:"is_builtin"`
}

// Record 是表里的一行：场景本身 + 同步基线。
type Record struct {
	Scenario
	SeedHash *string `json:"seed_hash"` // 最近一次写入的出厂内容的哈希；nil = 自建或基线未知
	Deleted  bool    `json:"deleted"`   // 内置场景被用户删除（留删除标记，可恢复）
}

// Content 是场景可修改的字段。
type Content struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Enabled     bool   `json:"enabled"`
}

// 客户端可修复的冲突（接口层映射为 409）。
var (
	ErrConflict = errors.New("场景标识已存在")
	ErrCustom   = errors.New("自建场景没有出厂内容可恢复")
	ErrInUse    = errors.New("场景被定时模板引用")
)

// ErrUnavailable 标记点名的场景不能用于新任务（不存在或已停用），接口层映射为 400：
// 下发入口与定时模板据此明确拒绝，不悄悄换成别的场景。
var ErrUnavailable = errors.New("场景不可用")

// IsConflict 报告 err 是否为场景操作冲突（409）。
func IsConflict(err error) bool {
	for _, e := range []error{ErrConflict, ErrCustom, ErrInUse} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

var codePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Schema 返回场景的可修改字段表（全部为整值）。
func Schema() override.Schema {
	return override.SchemaOf(Content{})
}

// Defaults 返回内置场景的出厂内容（出厂即启用）。
func Defaults(b builtin.Scenario) Content {
	return Content{Name: b.Name, Description: b.Description, Body: b.Body, Enabled: true}
}

// SeedHash 返回内置场景出厂内容的哈希。
func SeedHash(b builtin.Scenario) string {
	return builtinsync.Hash(b.Name, b.Description, b.Body)
}

// ContentOf 取场景的可修改部分。
func ContentOf(s Scenario) Content {
	return Content{Name: s.Name, Description: s.Description, Body: s.Body, Enabled: s.Enabled}
}

// Validate 校验场景内容。
func Validate(c Content) error {
	var problems []string
	if strings.TrimSpace(c.Name) == "" {
		problems = append(problems, "名称不能为空")
	}
	if n := utf8.RuneCountInString(c.Name); n > MaxNameRunes {
		problems = append(problems, fmt.Sprintf("名称 %d 字，超过上限 %d 字", n, MaxNameRunes))
	}
	if strings.TrimSpace(c.Body) == "" {
		problems = append(problems, "正文不能为空")
	}
	if n := utf8.RuneCountInString(c.Body); n > MaxBodyRunes {
		problems = append(problems, fmt.Sprintf("正文 %d 字，超过上限 %d 字", n, MaxBodyRunes))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w：%s", override.ErrInvalid, strings.Join(problems, "；"))
	}
	return nil
}

// ValidateNew 校验新建自建场景。
func ValidateNew(s Scenario) error {
	if !codePattern.MatchString(s.Code) {
		return fmt.Errorf("%w：标识符须为小写字母、数字与连字符（1–64 位，字母或数字开头）", override.ErrInvalid)
	}
	return Validate(ContentOf(s))
}
