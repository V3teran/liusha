// Package delegate 提供主 ReAct 的 delegate 工具：
// 把一条流量委托给某个 skill 的子 ReAct 完成（同进程同步嵌套）。
//
// 命名理由（业界最佳实践对照）：
//   - CrewAI 用 `delegate` —— 角色之间委托任务
//   - LangGraph 用 `transfer_to_<agent>` —— 转移控制权
//   - OpenAI Swarm 同上
//   - Anthropic 多 agent 论文用 `dispatch_subagent`
// 选 `delegate` 因业界最普及，跨框架理解一致，未来加 recon / exploit / report
// 等非漏洞 skill 也合用。
//
// CC 风格：tool 的 Description 与 ParametersJSON 都从 Catalog 动态生成，
// 加 SKILL.md + 注册 builder = 立即可被 LLM 发现，无需改 prompt 字符串。
package delegate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/toolfx"
)

// Delegate 工具：把流量委托给某个 skill 的子 ReAct（同步阻塞）。
//
// 多个 delegate 在主 LLM 同一轮返回时，runtime tool_calls 并行框架
// 会启 N 个 goroutine 并行跑（每个独立调 Delegate.Execute）。
//
// 字段：
//   - Builders   skill 名 → SubBuilder 闭包；启动期 main.go 注册
//   - Catalog    可用 skill 元数据（来自 Loader.List），驱动 Description / Parameters 动态生成
//   - SubLLM     给子 ReAct 用的 LLM Generator（已 Instrument 装饰，role=hunter）
//   - Observer   注入子 ReAct 的过程判官（与主 ReAct 共享同一 observer 实例）
type Delegate struct {
	Builders     map[string]skill.Builder
	EngagementID string
	SubLLM       llm.Generator
	Catalog      []*skill.Card
	Observer     react.Observer
}

// Name 返回工具名 "delegate"。
func (a *Delegate) Name() string { return "delegate" }

// Description 给 LLM 的工具描述——按 Catalog 动态展开"name: description"清单。
//
// CC 风格：LLM 看一眼工具描述就知道有什么 skill 可调，省去硬编码 system prompt 列表。
func (a *Delegate) Description() string {
	if len(a.Catalog) == 0 {
		return "把当前流量委托给某个 skill 的子 ReAct 完成（同步阻塞）。子完成后返 summary。"
	}
	var b strings.Builder
	b.WriteString("把当前流量委托给某个 skill 的子 ReAct 完成（同步阻塞）。当前可用 skill：\n")
	cards := sortedCatalog(a.Catalog)
	for _, c := range cards {
		fmt.Fprintf(&b, "- %s: %s\n", c.Name, c.Description)
	}
	b.WriteString("根据流量特征选择合适的 skill；可一轮返回多个 delegate（自动并行）。")
	return b.String()
}

// ParametersJSON：skill 字段用 enum 限定为 Catalog 中已存在的 name。
// 主 LLM 按 OpenAI tool schema 严格校验，写错 skill 名直接被拒。
func (a *Delegate) ParametersJSON() json.RawMessage {
	skillProp := `{"type":"string","description":"要委托给的 skill 名"}`
	if len(a.Catalog) > 0 {
		cards := sortedCatalog(a.Catalog)
		names := make([]string, len(cards))
		for i, c := range cards {
			names[i] = c.Name
		}
		enum, _ := json.Marshal(names)
		skillProp = fmt.Sprintf(`{"type":"string","enum":%s,"description":"要委托给的 skill 名（必须是 enum 中之一）"}`, string(enum))
	}
	return json.RawMessage(fmt.Sprintf(`{
        "type":"object",
        "properties":{
            "skill":%s,
            "flow_id":{"type":"integer"},
            "host":{"type":"string"},
            "url":{"type":"string"},
            "method":{"type":"string"},
            "credential_locations":{
                "type":"array",
                "description":"上游 classify_traffic 输出的 credential_locations 透传给子 ReAct（用于构造带占位 token 的 anonymous）。可选；缺失时子 ReAct 用空凭证 anonymous（旧行为）。",
                "items":{
                    "type":"object",
                    "properties":{
                        "type":{"type":"string","enum":["headers","query","body"]},
                        "key":{"type":"string"}
                    },
                    "required":["type","key"]
                }
            }
        },
        "required":["skill","flow_id","host"]
    }`, skillProp))
}

// sortedCatalog 按 name 字典序排序 catalog 副本，避免 LLM 看到不稳定顺序破坏 prompt cache。
func sortedCatalog(in []*skill.Card) []*skill.Card {
	out := make([]*skill.Card, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Execute 装配子 ReAct + 同进程同步嵌套跑 + 返 summary。
//
// 子 ReAct 复用主 ReAct 的 Observer 实例（同 engagement，每 5 步过程判官评估）。
//
// credential_locations 由主 LLM 在调 delegate 时透传（来自上游 classify_traffic
// 输出），子 ReAct 用它构造带占位 token 的 anonymous 假认证身份。
func (a *Delegate) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		Skill               string                          `json:"skill"`
		FlowID              int64                           `json:"flow_id"`
		Host                string                          `json:"host"`
		URL                 string                          `json:"url"`
		Method              string                          `json:"method"`
		CredentialLocations []credential.CredentialLocation `json:"credential_locations"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("decode args: %w", err)
	}
	if in.Skill == "" || in.FlowID == 0 || in.Host == "" {
		return toolfx.Result{}, errors.New("skill/flow_id/host 必填")
	}
	builder, ok := a.Builders[in.Skill]
	if !ok {
		return toolfx.Result{}, fmt.Errorf("unknown skill: %s", in.Skill)
	}

	cfg, err := builder(ctx, skill.BuilderParams{
		EngagementID:        a.EngagementID,
		FlowID:              in.FlowID,
		Host:                in.Host,
		URL:                 in.URL,
		Method:              in.Method,
		LLM:                 a.SubLLM,
		Observer:            a.Observer,
		CredentialLocations: in.CredentialLocations,
	})
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("build skill %s: %w", in.Skill, err)
	}

	sub, err := react.Run(ctx, cfg)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("sub-react %s: %w", in.Skill, err)
	}

	summary := fmt.Sprintf("skill=%s steps=%d terminate=%s usage=in:%d/out:%d",
		in.Skill, sub.TotalSteps, sub.TerminateBy,
		sub.TotalUsage.InTokens, sub.TotalUsage.OutTokens)
	return toolfx.Result{Summary: summary}, nil
}
