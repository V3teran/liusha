// Package agent 定义四个 agent 角色的生效配置：出厂值（internal/builtin）叠加
// 用户自定义（internal/override）。
//
// 身份（code、名称）固定；Config 是可自定义的部分。章程只承载角色指导，程序依赖的
// 输出契约在各 agent 包的 contract 子包里，不在这里、也不可自定义。
package agent

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/V3teran/liusha/internal/builtin"
	"github.com/V3teran/liusha/internal/override"
)

// Config 是 agent 可自定义的字段。json 名即自定义层与接口中的字段名。
type Config struct {
	Charter       string   `json:"charter"`        // 章程：角色定位、原则、策略
	Description   string   `json:"description"`    // 调度摘要：planner 规划时读取的角色分工
	FunctionTools []string `json:"function_tools"` // 内置函数工具白名单（"*" = 全部）
	CliTools      []string `json:"cli_tools"`      // 沙箱 CLI 工具白名单（"*" = 全部）
	Skills        []string `json:"skills"`         // 技能白名单（"*" = 全部）
	MaxIterations int      `json:"max_iterations"` // ReAct 迭代上限
	Complexity    string   `json:"complexity"`     // LLM 档位：simple | medium | complex
}

// Agent 是一个 agent 的生效配置。
type Agent struct {
	Code string `json:"code"`
	Name string `json:"name"`
	Config
}

// Catalog 提供集合字段的元素全集，用于展开「全部」与校验引用。
type Catalog struct {
	FunctionTools func() []string
	CliTools      func() []string
	Skills        func() []string
}

// 可自定义字段名。
const (
	FieldCharter       = "charter"
	FieldDescription   = "description"
	FieldFunctionTools = "function_tools"
	FieldCliTools      = "cli_tools"
	FieldSkills        = "skills"
	FieldMaxIterations = "max_iterations"
	FieldComplexity    = "complexity"
)

// MaxIterationsLimit 是迭代上限的可设置上界（防误填导致单步 ReAct 失控）。
const MaxIterationsLimit = 1000

var complexities = []string{"simple", "medium", "complex"}

// Schema 返回 agent 的可自定义字段表。
func Schema(c Catalog) override.Schema {
	universe := func(f func() []string) func() []json.RawMessage {
		return func() []json.RawMessage {
			if f == nil {
				return nil
			}
			return override.Strings(f())
		}
	}
	// 字段表由 Config 推导（名单字段为集合，其余为整值替换），再给名单字段挂上全集。
	schema := override.SchemaOf(Config{})
	for name, list := range map[string]func() []string{
		FieldFunctionTools: c.FunctionTools,
		FieldCliTools:      c.CliTools,
		FieldSkills:        c.Skills,
	} {
		f := schema[name]
		f.Universe = universe(list)
		schema[name] = f
	}
	return schema
}

// Defaults 返回出厂 agent 的可自定义部分。
func Defaults(b builtin.Agent) Config {
	return Config{
		Charter:       b.Charter,
		Description:   b.Description,
		FunctionTools: b.FunctionTools,
		CliTools:      b.CliTools,
		Skills:        b.Skills,
		MaxIterations: b.MaxIterations,
		Complexity:    b.Complexity,
	}
}

// DefaultsDocument 返回出厂值的文档形式（自定义引擎的输入）。
func DefaultsDocument(b builtin.Agent) override.Document {
	d, err := override.ToDocument(Defaults(b))
	if err != nil {
		panic(fmt.Sprintf("agent: 出厂配置序列化失败: %v", err))
	}
	return d
}

// Resolve 合成生效配置：出厂值叠加用户层。
func Resolve(b builtin.Agent, ov override.Overrides, c Catalog) (Agent, error) {
	var cfg Config
	if err := override.FromDocument(override.Apply(Schema(c), DefaultsDocument(b), ov), &cfg); err != nil {
		return Agent{}, fmt.Errorf("agent %s: 合成生效配置: %w", b.Code, err)
	}
	return Agent{Code: b.Code, Name: b.Name, Config: cfg}, nil
}

// Validate 校验生效配置（写入前对「应用本次修改后的结果」整体校验）。
func Validate(cfg Config) error {
	var problems []string
	if strings.TrimSpace(cfg.Charter) == "" {
		problems = append(problems, "章程不能为空")
	}
	if strings.TrimSpace(cfg.Description) == "" {
		problems = append(problems, "调度摘要不能为空")
	}
	if !slices.Contains(complexities, cfg.Complexity) {
		problems = append(problems, fmt.Sprintf("档位 %q 非法（应为 simple|medium|complex）", cfg.Complexity))
	}
	if cfg.MaxIterations < 1 || cfg.MaxIterations > MaxIterationsLimit {
		problems = append(problems, fmt.Sprintf("最大迭代次数须在 1–%d 之间", MaxIterationsLimit))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w：%s", override.ErrInvalid, strings.Join(problems, "；"))
	}
	return nil
}
