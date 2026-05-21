// vuln.go 实现 read_vuln_skill —— Progressive Disclosure 的 Tier 2（漏洞挖掘指南）：
//
// Tier 1（user prompt 常驻）：每条流量自动注入"漏洞类型索引"，
//   每个漏洞一行 name + description（极简列表，无 category 分组）。
//
// Tier 2（按需加载）：LLM 按 user_prompt 注入的"漏洞类型索引"判定方向后，调
//   read_vuln_skill(name="bac")
// 拿完整 SKILL.md body（漏洞本质 + 挖掘方向 + 判定原则 + 误报排除 + finding 红线）。
//
// 与 read_tooling_skill 同模式：常驻索引省 token，详情按需付费，加新漏洞类型
// 不动 Go（写 skills/vuln/<name>/SKILL.md 即生效）。
package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/skill"
	toolfx "github.com/V3teran/liusha/internal/toolruntime"
)

// ReadVulnSkill 读 skills/vuln/<name>/SKILL.md 完整 body 给 LLM。
//
// Loader root 应指向 skills/vuln/，与 tooling SKILL Loader
// 双方解耦（由 cmd/scanner 装配时分别构造）。
type ReadVulnSkill struct {
	Loader *skill.Loader
}

// Name 返回工具名 "read_vuln_skill"。
func (a *ReadVulnSkill) Name() string { return "read_vuln_skill" }

//
// 设计要点：**禁止在 description 里举具体 name 例子**（如 sqli/xss/ssrf）——
// 实测 LLM 会把例子当成"可用 name"瞎调（5/8 agent_run 中招），污染 Progressive Disclosure 单一来源。
// 可用 name 完全由 user prompt 段「可用漏洞挖掘指南索引」（buildVulnCatalog 渲染）提供。
func (a *ReadVulnSkill) Description() string {
	return "拉取一个漏洞类型的完整挖掘指南。" +
		"**使用场景**：判完流量方向、确定要挖哪一/哪几类漏洞 → " +
		"调本工具拿完整 SKILL.md（漏洞本质 / 挖掘方向 / 判定原则 / 误报排除 / 写 finding 红线）→ " +
		"再按指南调 run_command 实证。" +
		"**name 取值**：必须在 user prompt『可用漏洞挖掘指南索引』段列出，" +
		"**不要凭行业常识猜**（SKILL 库可能不完整，列表外的传过来直接报错）。"
}

// ParametersJSON：name 必填，运行时从 Loader.List() 拼 enum 强约束。
//
// P6-1 修复：实测仅靠 description "不要凭行业常识猜" 不够（LLM 5/8 仍按渗透圈通用名瞎调 sqli/xss/lfi）。
// schema 级 enum 让支持 strict tools 的 provider 在生成阶段就拒绝非 catalog 内 name；
// 不支持 enum 的 provider 有 Execute 错误兜底（含 available list 反馈，见 Execute）。
//
// Loader nil 或空 catalog 时不应该调到这里——cmd/scanner 装配时已用 len(List())>0 守门。
func (a *ReadVulnSkill) ParametersJSON() json.RawMessage {
	var enum []string
	if a.Loader != nil {
		for _, c := range a.Loader.List() {
			enum = append(enum, c.Name)
		}
	}
	enumBytes, _ := json.Marshal(enum) // []string Marshal 不会失败
	return json.RawMessage(fmt.Sprintf(`{
  "type":"object",
  "properties":{
    "name":{"type":"string","enum":%s,"description":"漏洞类型名（必须 enum 内）"}
  },
  "required":["name"]
}`, string(enumBytes)))
}

// vulnDocOutput 是 LLM 看到的结构化结果。
type vulnDocOutput struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

// Execute 解析 name → Loader.Load → 返回 body。
func (a *ReadVulnSkill) Execute(_ context.Context, args json.RawMessage) (toolfx.Result, error) {
	if a.Loader == nil {
		return toolfx.Result{}, errors.New("read_vuln_skill: Loader 未注入")
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 read_vuln_skill 参数失败: %w", err)
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
		return toolfx.Result{}, fmt.Errorf("漏洞类型 %q 不在 catalog（available=%v）—— 必须从 available 选，不要凭行业常识猜", in.Name, available)
	}

	out := vulnDocOutput{Name: in.Name, Body: card.Body}
	enc, err := json.Marshal(out)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("marshal output: %w", err)
	}
	summary := fmt.Sprintf("read_vuln_skill name=%s body_len=%d", in.Name, len(card.Body))
	return toolfx.Result{Output: enc, Summary: summary}, nil
}
