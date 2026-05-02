// Package scan 提供主 ReAct 的 scan_vuln 工具：按 skill 名启动一个漏洞扫描子 ReAct（同进程同步）。
//
// CC 风格 v1.1：tool 的 Description 与 ParametersJSON 都从 Catalog 动态生成，
// 主 LLM 直接通过 frontmatter description + enum 知道有哪些 skill 可用，
// 加 SKILL.md + 注册 builder = 立即可被 LLM 发现，无需改 prompt 字符串。
package scan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/tool"
)

// ScanVuln 工具：启动一个漏洞扫描子 ReAct（同步阻塞）。
//
// 多个 scan_vuln 在主 LLM 同一轮返回时，runtime tool_calls 并行框架
// 会启 N 个 goroutine 并行跑（每个独立调 ScanVuln.Execute）。
//
// 字段：
//   - Builders   skill 名 → SubBuilder 闭包；启动期 main.go 注册
//   - Catalog    可用 skill 元数据（来自 Loader.List），驱动 Description / Parameters 动态生成
//   - SubLLM     给子 ReAct 用的 LLM Generator（已 Instrument 装饰，role=prober）
//   - Observer   注入子 ReAct 的过程判官（与主 ReAct 共享同一 observer 实例）
type ScanVuln struct {
	Builders     map[string]skill.Builder
	EngagementID string
	SubLLM       llm.Generator
	Catalog      []*skill.Card
	Observer     react.Observer
}

// Name 返回工具名 "scan_vuln"。
func (a *ScanVuln) Name() string { return "scan_vuln" }

// Description 给 LLM 的工具描述——按 Catalog 动态展开"name: description"清单。
//
// CC 风格：LLM 看一眼工具描述就知道有什么 skill 可调，省去硬编码 system prompt 列表。
func (a *ScanVuln) Description() string {
	if len(a.Catalog) == 0 {
		return "对一条流量启动一种漏洞扫描子 ReAct。子完成后返 summary。"
	}
	var b strings.Builder
	b.WriteString("对一条流量启动一种漏洞扫描子 ReAct。当前可用 skill：\n")
	cards := sortedCatalog(a.Catalog)
	for _, c := range cards {
		fmt.Fprintf(&b, "- %s: %s\n", c.Name, c.Description)
	}
	b.WriteString("根据流量特征选择合适的 skill；可一轮返回多个 scan_vuln（自动并行）。")
	return b.String()
}

// ParametersJSON：skill 字段用 enum 限定为 Catalog 中已存在的 name。
// 主 LLM 按 OpenAI tool schema 严格校验，写错 skill 名直接被拒。
func (a *ScanVuln) ParametersJSON() json.RawMessage {
	skillProp := `{"type":"string","description":"要调用的 skill 名"}`
	if len(a.Catalog) > 0 {
		cards := sortedCatalog(a.Catalog)
		names := make([]string, len(cards))
		for i, c := range cards {
			names[i] = c.Name
		}
		enum, _ := json.Marshal(names)
		skillProp = fmt.Sprintf(`{"type":"string","enum":%s,"description":"要调用的 skill 名（必须是 enum 中之一）"}`, string(enum))
	}
	return json.RawMessage(fmt.Sprintf(`{
        "type":"object",
        "properties":{
            "skill":%s,
            "flow_id":{"type":"integer"},
            "host":{"type":"string"},
            "url":{"type":"string"},
            "method":{"type":"string"}
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
func (a *ScanVuln) Execute(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	var in struct {
		Skill  string `json:"skill"`
		FlowID int64  `json:"flow_id"`
		Host   string `json:"host"`
		URL    string `json:"url"`
		Method string `json:"method"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return tool.Result{}, fmt.Errorf("decode args: %w", err)
	}
	if in.Skill == "" || in.FlowID == 0 || in.Host == "" {
		return tool.Result{}, errors.New("skill/flow_id/host 必填")
	}
	builder, ok := a.Builders[in.Skill]
	if !ok {
		return tool.Result{}, fmt.Errorf("unknown skill: %s", in.Skill)
	}

	cfg, err := builder(ctx, skill.BuilderParams{
		EngagementID: a.EngagementID,
		FlowID:       in.FlowID,
		Host:         in.Host,
		URL:          in.URL,
		Method:       in.Method,
		LLM:          a.SubLLM,
		Observer:     a.Observer,
	})
	if err != nil {
		return tool.Result{}, fmt.Errorf("build skill %s: %w", in.Skill, err)
	}

	sub, err := react.Run(ctx, cfg)
	if err != nil {
		return tool.Result{}, fmt.Errorf("sub-react %s: %w", in.Skill, err)
	}

	summary := fmt.Sprintf("skill=%s steps=%d terminate=%s usage=in:%d/out:%d",
		in.Skill, sub.TotalSteps, sub.TerminateBy,
		sub.TotalUsage.InTokens, sub.TotalUsage.OutTokens)
	return tool.Result{Summary: summary}, nil
}
