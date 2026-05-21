// tooling.go 实现 read_tooling_skill —— Progressive Disclosure 的 Tier 2：
//
// Tier 1（user prompt 常驻）：每条流量自动注入"工具索引"，
//   每个工具一行 name + description（约 50-200 chars/工具，全集 < 1KB）。
//
// Tier 2（按需加载）：LLM 决定要用 sqlmap 时，调
//   read_tooling_skill(name="sqlmap")
// 拿完整 SKILL.md body（参数表 + 输出 grep 关键词 + 坑点 + 红线）。
//
// 这套对齐 Anthropic Claude Code skills 系统：常驻索引省 token，
// 详细文档按需付费，加新工具不动 Go（写 SKILL.md 即生效）。
package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// ReadToolingSkill 读 skills/tooling/<name>/SKILL.md 完整 body 给 LLM。
//
// Loader root 应指向 skills/tooling/，与 vuln SKILL Loader 解耦
// （由 cmd/scanner 装配时分别构造）。
type ReadToolingSkill struct {
	Loader *skill.Loader
}

// Name 返回工具名 "read_tooling_skill"。
func (a *ReadToolingSkill) Name() string { return "read_tooling_skill" }

//
// 设计要点：**禁止在 description 里举具体 name 例子**——
// 实测 LLM 会把例子当成"系统支持"的可用 name 瞎调，污染 Progressive Disclosure 单一来源。
// 可用 name 完全由 user prompt 段「可用外部工具索引」（buildToolingCatalog 渲染）提供。
func (a *ReadToolingSkill) Description() string {
	return "拉取一个外部 CLI 工具的完整使用手册。" +
		"**使用场景**：user prompt 末尾的『可用外部工具索引』看到某工具描述觉得对路 → " +
		"调本工具拿完整 SKILL.md（参数表 / 输出 grep 关键词 / 常见坑 / 写 finding 红线）→ " +
		"再调 run_command 跑命令。" +
		"**name 取值**：必须在 user prompt『可用外部工具索引』段列出，" +
		"**不要凭行业常识猜**（工具镜像不完整或未装；列表外的传过来直接报错）。"
}

// ParametersJSON：name 必填，运行时从 Loader.List() 拼 enum 强约束。
//
// P6-1 修复：仅靠 description 不够，需要 schema 级 enum + Execute 错误兜底（双保险）。
// 见 vuln.go 同名方法注释。
func (a *ReadToolingSkill) ParametersJSON() json.RawMessage {
	var enum []string
	if a.Loader != nil {
		for _, c := range a.Loader.List() {
			enum = append(enum, c.Name)
		}
	}
	enumBytes, _ := json.Marshal(enum)
	return json.RawMessage(fmt.Sprintf(`{
  "type":"object",
  "properties":{
    "name":{"type":"string","enum":%s,"description":"工具名（必须 enum 内）"}
  },
  "required":["name"]
}`, string(enumBytes)))
}

// toolingDocOutput 是 LLM 看到的结构化结果。
type toolingDocOutput struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

// Execute 解析 name → Loader.Load → 返回 body。
func (a *ReadToolingSkill) Execute(_ context.Context, args json.RawMessage) (toolfx.Result, error) {
	if a.Loader == nil {
		return toolfx.Result{}, errors.New("read_tooling_skill: Loader 未注入")
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 read_tooling_skill 参数失败: %w", err)
	}
	if in.Name == "" {
		return toolfx.Result{}, errors.New("name 必填")
	}

	card, err := a.Loader.Load(in.Name)
	if err != nil {
		// P6-1 兜底：错误消息附 available list，让不支持 enum 的 provider 在失败一次后立即学到 catalog。
		var available []string
		for _, c := range a.Loader.List() {
			available = append(available, c.Name)
		}
		return toolfx.Result{}, fmt.Errorf("工具名 %q 不在 catalog（available=%v）—— 必须从 available 选，不要凭行业常识猜", in.Name, available)
	}

	out := toolingDocOutput{Name: in.Name, Body: card.Body}
	enc, err := json.Marshal(out)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("marshal output: %w", err)
	}
	summary := fmt.Sprintf("read_tooling_skill name=%s body_len=%d", in.Name, len(card.Body))
	return toolfx.Result{Output: enc, Summary: summary}, nil
}
