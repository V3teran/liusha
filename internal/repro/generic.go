package repro

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GenericName 是中立领域的名字。
const GenericName = "generic"

// Generic 是内置的中立领域：配方是自然语言步骤，判据是自然语言描述。没有机器复现
// 通道——评估者按步骤自主执行取证、基于亲见的输出判定。
type Generic struct{}

// Name 实现 Domain。
func (Generic) Name() string { return GenericName }

// Recognizes 实现 Domain：recipe 内或顶层（笔误）写了 steps。
func (Generic) Recognizes(fields map[string]json.RawMessage) bool {
	if len(fields["steps"]) > 0 {
		return true
	}
	var recipe struct {
		Steps string `json:"steps"`
	}
	return json.Unmarshal(fields["recipe"], &recipe) == nil && recipe.Steps != ""
}

// Example 实现 Domain。
func (Generic) Example() string {
	return `{"domain": "generic", "recipe": {"steps": "1. 登录后台 2. 执行 ... 3. 观察 ..."}, "assert": {"description": "看到 X 即坐实"}}`
}

// Normalize 实现 Domain：steps 非空 + assert.description 判据非空。
func (Generic) Normalize(fields map[string]json.RawMessage) (recipe, assert json.RawMessage, err error) {
	recipe = fields["recipe"]
	if len(recipe) == 0 && len(fields["steps"]) > 0 {
		// 笔误：steps 写在顶层
		recipe, err = json.Marshal(map[string]json.RawMessage{"steps": fields["steps"]})
		if err != nil {
			return nil, nil, fmt.Errorf("repro 归一化失败: %w", err)
		}
	}
	if len(recipe) == 0 {
		return nil, nil, fmt.Errorf("generic 域 repro 缺少 recipe.steps。\n\n正确格式：%s", Generic{}.Example())
	}
	var rc struct {
		Steps string `json:"steps"`
	}
	if err := json.Unmarshal(recipe, &rc); err == nil && strings.TrimSpace(rc.Steps) == "" {
		return nil, nil, fmt.Errorf("generic 域 recipe.steps 不能为空——评估者据此自主执行，无步骤即无可复现")
	}
	assert = fields["assert"]
	if len(assert) == 0 {
		return nil, nil, fmt.Errorf("repro 缺少 assert 字段。\n\ngeneric 域判据：{\"description\": \"执行 steps 后观察到什么才算坐实\"}")
	}
	var ac struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(assert, &ac); err != nil || strings.TrimSpace(ac.Description) == "" {
		return nil, nil, fmt.Errorf("generic 域 assert.description 不能为空——判据缺失即橡皮图章。\n\n" +
			"正确格式：{\"description\": \"执行 steps 后观察到 X 且正常路径无法出现，即坐实\"}")
	}
	return recipe, assert, nil
}
