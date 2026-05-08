// Package traffic 提供主 ReAct 的流量业务工具：classify_traffic / get_findings。
//
// 与 tools/spawn（派发器）拆分：traffic 包内是分析/查询流量的具体业务工具，
// 不持有 skill builder 类型（已迁到 internal/skill 包）。
package traffic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/flowfacts"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// classifierLog 包级 logger（与 instrument.go 同模式：避免每次 Execute 触发 logx.New）。
var classifierLog zerolog.Logger = logx.New("tools.classify_traffic")

// FlowReader 是 classify_traffic 依赖的最小 flow 读接口，由 *flow.Store 自动满足。
// 局部定义在 consumer 侧（Go idiom: accept interfaces, return structs）。
type FlowReader interface {
	GetByID(ctx context.Context, id int64) (flow.Flow, error)
}

// classifierSkillName 是本工具加载 prompt body 的 skill name。
// 该 skill 是"内部 prompt"——loader 自动 Index 它，但 main.go 装配 delegate.Catalog 时
// 必须过滤掉它，避免主 LLM 误以为它是可 delegate 的子 skill。
const classifierSkillName = "classify-traffic"

// ClassifyTraffic 让 LLM 一次性提取单条流量的"事实"：操作类型、资源范围、参数位置、
// 是否带认证、凭证位置、推理。**不再输出"该派哪些 skill"**——v1.3 改造把路由决策
// 下放回主 ReAct，由它根据 catalog 元数据 + 这些 facts 自主决定。
//
// 业务背景：主 ReAct 第 1 步先调它，工具内部按 flow_id 拉完整 flow（请求/响应都拿全），
// 智能截断后拼 prompt（来自 skills/classify-traffic/SKILL.md）发给 LLM，
// 解析返回 JSON：{operation, resource_scope, param_locations, carries_auth,
// credential_locations, reasoning}。
//
// 输出会被主 ReAct 用来：
//   - 比对 catalog 中各 skill 的 requires_auth / applicable_param_locations 元数据
//   - 自主决定调哪些 delegate(skill_name, flow_id, credential_locations)
//   - 全部不适用 → done({"reason":"no_applicable_skills"}) 短路
type ClassifyTraffic struct {
	LLM    llm.Generator
	Flows  FlowReader
	Loader *skill.Loader

	// Cfg 控制 flow 喂 LLM 前的截断阈值。caller 从 cfg.Classify 注入；
	// 零值字段会回退到 v1 上线初始值（buildClassifyInput 内 effectiveClassify 兜底）。
	Cfg config.ClassifyConfig

	// Facts 可空：装配时注入则把 LLM 输出 best-effort 落 flow_facts；
	// nil 退化为旧行为（仅透传 Output 给主 ReAct，不持久化）。
	// EngagementID 同时为空时也跳过落库（无外键归属）。
	Facts        *flowfacts.Store
	EngagementID string
}

// classifyOutput 与 skills/classify-traffic/SKILL.md 约定的 LLM 输出 JSON 一一对应。
// jsonb 字段以 RawMessage 透传，避免 LLM 出意外结构时落库失败（容错优先）。
//
// v1.3 改造：
//   - 删 RequiredSkills 字段（路由决策已下放回主 ReAct）
//   - attack_surfaces → param_locations（只是参数位置，与"攻击面"无关）
type classifyOutput struct {
	Operation           string          `json:"operation"`
	ResourceScope       string          `json:"resource_scope"`
	ParamLocations      json.RawMessage `json:"param_locations"`
	CarriesAuth         bool            `json:"carries_auth"`
	CredentialLocations json.RawMessage `json:"credential_locations"`
	Reasoning           string          `json:"reasoning"`
}

// Name 返回工具名 "classify_traffic"。
func (a *ClassifyTraffic) Name() string { return "classify_traffic" }

// Description 给 LLM 的工具描述。
func (a *ClassifyTraffic) Description() string {
	return "分析单条 HTTP 流量，输出事实 JSON: {operation, resource_scope, param_locations, " +
		"carries_auth, credential_locations, reasoning}。" +
		"工具内部按 flow_id 拉完整流量并智能截断后调 LLM；调用方只需传 flow_id。" +
		"派哪些扫描 skill 由调用方（主 ReAct）依据 catalog 元数据 + 这些事实自行决定。"
}

// ParametersJSON 工具入参 JSON Schema：极简化，只需 flow_id。
//
// 完整流量数据由工具内部从 flow store 拉取，避免主 LLM 在不知道 headers/body
// 内容的情况下被迫"瞎传字段"。
func (a *ClassifyTraffic) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
        "type":"object",
        "properties":{
            "flow_id":{"type":"integer","description":"http_flow.id（int64）"}
        },
        "required":["flow_id"]
    }`)
}

// Execute 解析 args（仅 flow_id）→ 拉 flow → 智能截断 → 拼 prompt → 调 LLM → 返回原始 JSON content。
func (a *ClassifyTraffic) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		FlowID int64 `json:"flow_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("decode args: %w", err)
	}
	if in.FlowID <= 0 {
		return toolfx.Result{}, errors.New("flow_id 必填且 > 0")
	}
	if a.LLM == nil || a.Flows == nil || a.Loader == nil {
		return toolfx.Result{}, errors.New("ClassifyTraffic: LLM/Flows/Loader 都必须装配")
	}

	f, err := a.Flows.GetByID(ctx, in.FlowID)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("拉取 flow %d: %w", in.FlowID, err)
	}

	card, err := a.Loader.Load(classifierSkillName)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("加载 classify-traffic skill: %w", err)
	}

	input := buildClassifyInput(f, a.Cfg)
	inputJSON, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("序列化截断后流量: %w", err)
	}

	prompt := strings.Replace(card.Body, "$INPUT_DATA$", string(inputJSON), 1)

	res, err := a.LLM.Generate(ctx, []llm.Message{{Role: llm.RoleUser, Content: prompt}}, nil)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("classify_traffic LLM: %w", err)
	}

	// best-effort 把 LLM 输出落 flow_facts（非阻塞 Execute 返回）；
	// 失败仅 warn，行为对外不变。
	a.appendFacts(in.FlowID, res.Content)

	// 透传 LLM 原始 content 给主 ReAct LLM；主 LLM 解析其中的 facts
	// （param_locations / carries_auth / credential_locations 等），
	// 比对 catalog 元数据后自主决定下一步（短路 done 或 delegate）。
	return toolfx.Result{
		Output:  json.RawMessage(res.Content),
		Summary: fmt.Sprintf("classify_traffic flow=%d %s %s", f.ID, f.Method, f.URL),
	}, nil
}

// appendFacts 解析 LLM JSON 输出并 best-effort 落 flow_facts。
//
// 任何失败（store 未注入 / EngagementID 为空 / JSON 解析失败 / DB 写入失败）
// 都仅打 warn 日志，不返回错误。落库用 context.Background() 与业务 ctx 解耦，
// 避免业务 ctx 取消时埋点丢失（与 internal/llm/instrument.go 同样策略）。
//
// resource_scope 不属于 enum 集合时强制回退为 "unknown"（CHECK 约束兜底）。
func (a *ClassifyTraffic) appendFacts(flowID int64, content string) {
	if a.Facts == nil || a.EngagementID == "" {
		return
	}
	cleaned := stripJSONFence(content)
	var out classifyOutput
	if err := json.Unmarshal([]byte(cleaned), &out); err != nil {
		classifierLog.Warn().Err(err).Int64("flow_id", flowID).
			Msg("flow_facts: 解析 LLM JSON 失败，跳过落库")
		return
	}
	scope := flowfacts.ResourceScope(out.ResourceScope)
	switch scope {
	case flowfacts.ResourceScopePrivate,
		flowfacts.ResourceScopePublic,
		flowfacts.ResourceScopeUnknown:
	default:
		scope = flowfacts.ResourceScopeUnknown
	}
	if _, err := a.Facts.Append(context.Background(), flowfacts.Facts{
		EngagementID:        a.EngagementID,
		FlowID:              flowID,
		Operation:           out.Operation,
		ResourceScope:       scope,
		ParamLocations:      out.ParamLocations,
		CarriesAuth:         out.CarriesAuth,
		CredentialLocations: out.CredentialLocations,
		Reasoning:           out.Reasoning,
	}); err != nil {
		classifierLog.Warn().Err(err).Int64("flow_id", flowID).
			Msg("flow_facts: 写库失败（不阻塞 Execute）")
	}
}

