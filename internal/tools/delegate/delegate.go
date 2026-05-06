// Package delegate 提供主 ReAct 的 delegate 工具：
// 把一条流量委托给某个 skill 的子 ReAct 完成（同进程同步嵌套）。
//
// 命名理由（业界最佳实践对照）：
//   - CrewAI 用 `delegate` —— 角色之间委托任务
//   - LangGraph 用 `transfer_to_<agent>` —— 转移控制权
//   - OpenAI Swarm 同上
//   - Anthropic 多 agent 论文用 `dispatch_subagent`
//
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
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/reactrun"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/toolfx"
)

// FlowReader 是 delegate 拉完整 flow 详情用的最小读接口，由 *flow.Store 自动满足。
// 子 ReAct 经 BuilderParams.RequestHeaders/RequestBody 一次性看到完整流量
// （agentic 路线核心：让 LLM 自识别注入点 / 凭证位 / 响应回显模式）。
type FlowReader interface {
	GetByID(ctx context.Context, id int64) (flow.Flow, error)
}

// TaskRecorder 抽象 sub-task 生命周期写库，由 *reactrun.Store 自动满足。
//
// 让 delegate 把每次 spawn 的 sub-react 落 agent_task 一行，是为了：
//   - 让 finding.task_id (FK→agent_task) 能挂上具体 sub-task 而不是父 orchestrator task
//   - SQL 直接查"哪个子 task 用了哪个 skill 跑出什么结果"（agent_task.skill / .result）
//
// 不挂 LLM-call 的 task_id（仍走父 task）——那是另一层改造，需要重 Instrument SubLLM。
type TaskRecorder interface {
	Create(ctx context.Context, p reactrun.NewParams) (string, error)
	SetRunning(ctx context.Context, id string) error
	SetDone(ctx context.Context, id string, result json.RawMessage) error
	SetError(ctx context.Context, id string, errMsg string) error
}

// Delegate 工具：把流量委托给某个 skill 的子 ReAct（同步阻塞）。
//
// 多个 delegate 在主 LLM 同一轮返回时，runtime tool_calls 并行框架
// 会启 N 个 goroutine 并行跑（每个独立调 Delegate.Execute）。
//
// 字段：
//   - Builders     skill 名 → SubBuilder 闭包；启动期 main.go 注册
//   - Catalog      可用 skill 元数据（来自 Loader.List），驱动 Description / Parameters 动态生成
//   - SubLLM       给子 ReAct 用的 LLM Generator（已 Instrument 装饰，role=hunter）；
//     SubLLMFor 非 nil 时本字段忽略
//   - Observer     注入子 ReAct 的过程判官；ObserverFor 非 nil 时本字段忽略
//   - Tasks        sub-task 生命周期写库；nil 时退化到 ad-hoc UUID（finding.task_id 仍 NULL）
//   - SubLLMFor    可选闭包：用 sub-task uuid 现场 Instrument SubLLM，让 hunter 的
//     llm_call.task_id 挂在 sub-task 而不是父 orchestrator task
//   - ObserverFor  可选闭包：用 sub-task uuid 现场建 Observer（其内部 LLM 也 Instrument
//     到 sub-task 上，让 observer 的 llm_call.task_id 也归到 sub-task）
type Delegate struct {
	Builders     map[string]skill.Builder
	EngagementID string
	SubLLM       llm.Generator
	Catalog      []*skill.Card
	Observer     react.Observer
	Tasks        TaskRecorder
	SubLLMFor    func(taskID string) llm.Generator
	ObserverFor  func(taskID string) react.Observer

	// Flows 用于 spawn 时拉完整 flow 详情填进 BuilderParams（headers + body），
	// 让子 ReAct 在 user prompt 一次性看到完整流量。nil 时降级——子 builder 自行
	// fallback（user prompt 仅含 method/url/host）。生产路径 main.go 必传。
	Flows FlowReader
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

	// sub-task 入 agent_task 表（role=hunter）：拿真 PG UUID 作为 BuilderParams.TaskID，
	// 让 finding.task_id (FK→agent_task) 能合法引用，不再永远 NULL。
	subTaskID, err := a.recordSubTaskStart(ctx, in)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("record sub-task start: %w", err)
	}

	// 用 sub-task uuid 重 Instrument hunter LLM + observer，让它们的 llm_call.task_id
	// 挂在 sub-task 上而不是父 orchestrator task。
	// maker 为 nil（旧装配/单测）时降级到固定字段。
	subLLM := a.SubLLM
	if a.SubLLMFor != nil && subTaskID != "" {
		subLLM = a.SubLLMFor(subTaskID)
	}
	subObserver := a.Observer
	if a.ObserverFor != nil && subTaskID != "" {
		subObserver = a.ObserverFor(subTaskID)
	}

	// 拉完整 flow 详情（headers + body）填进 BuilderParams——agentic 路线下子 ReAct
	// LLM 看 user prompt 直接识别注入点 / 凭证位 / 响应回显模式，不再走代码层
	// extract_injection_points 工具。Flows == nil 时（单测）降级，BuilderParams 这两
	// 字段为空，子 builder 自行 fallback。
	var reqHeaders json.RawMessage
	var reqBody []byte
	if a.Flows != nil {
		f, ferr := a.Flows.GetByID(ctx, in.FlowID)
		if ferr != nil {
			a.recordSubTaskError(ctx, subTaskID, ferr)
			return toolfx.Result{}, fmt.Errorf("load flow %d: %w", in.FlowID, ferr)
		}
		reqHeaders = f.RequestHeaders
		reqBody = f.RequestBody
	}

	cfg, err := builder(ctx, skill.BuilderParams{
		EngagementID:        a.EngagementID,
		TaskID:              subTaskID,
		FlowID:              in.FlowID,
		Host:                in.Host,
		URL:                 in.URL,
		Method:              in.Method,
		LLM:                 subLLM,
		Observer:            subObserver,
		CredentialLocations: in.CredentialLocations,
		RequestHeaders:      reqHeaders,
		RequestBody:         reqBody,
	})
	if err != nil {
		a.recordSubTaskError(ctx, subTaskID, err)
		return toolfx.Result{}, fmt.Errorf("build skill %s: %w", in.Skill, err)
	}

	sub, err := react.Run(ctx, cfg)
	if err != nil {
		a.recordSubTaskError(ctx, subTaskID, err)
		return toolfx.Result{}, fmt.Errorf("sub-react %s: %w", in.Skill, err)
	}

	a.recordSubTaskDone(ctx, subTaskID, in.Skill, sub)

	summary := fmt.Sprintf("skill=%s steps=%d terminate=%s usage=in:%d/out:%d",
		in.Skill, sub.TotalSteps, sub.TerminateBy,
		sub.TotalUsage.InTokens, sub.TotalUsage.OutTokens)
	return toolfx.Result{Summary: summary}, nil
}

// subTaskInput 是 delegate 透传给 builder 的入参快照，落到 agent_task.input。
type subTaskInput struct {
	Skill               string                          `json:"skill"`
	FlowID              int64                           `json:"flow_id"`
	Host                string                          `json:"host"`
	URL                 string                          `json:"url"`
	Method              string                          `json:"method"`
	CredentialLocations []credential.CredentialLocation `json:"credential_locations,omitempty"`
}

// recordSubTaskStart 写 agent_task 行并 SetRunning。
//
// Tasks==nil 时退化：返空 string，BuilderParams.TaskID 为空，finding.task_id 仍是 NULL。
// 这是单元测试场景；生产路径 main.go 必传 Tasks。
func (a *Delegate) recordSubTaskStart(ctx context.Context, in struct {
	Skill               string                          `json:"skill"`
	FlowID              int64                           `json:"flow_id"`
	Host                string                          `json:"host"`
	URL                 string                          `json:"url"`
	Method              string                          `json:"method"`
	CredentialLocations []credential.CredentialLocation `json:"credential_locations"`
}) (string, error) {
	if a.Tasks == nil {
		return "", nil
	}
	inputJSON, err := json.Marshal(subTaskInput{
		Skill:               in.Skill,
		FlowID:              in.FlowID,
		Host:                in.Host,
		URL:                 in.URL,
		Method:              in.Method,
		CredentialLocations: in.CredentialLocations,
	})
	if err != nil {
		return "", fmt.Errorf("marshal sub-task input: %w", err)
	}
	id, err := a.Tasks.Create(ctx, reactrun.NewParams{
		EngagementID: a.EngagementID,
		Role:         "hunter",
		Skill:        in.Skill,
		Input:        inputJSON,
	})
	if err != nil {
		return "", fmt.Errorf("create sub-task: %w", err)
	}
	if err := a.Tasks.SetRunning(ctx, id); err != nil {
		return id, fmt.Errorf("set sub-task running: %w", err)
	}
	return id, nil
}

// recordSubTaskDone 把 sub-task 推进到 done，写入 result。
// 失败仅警告级别（不应阻塞 sub-react 真正的成功返回给主 LLM）。
func (a *Delegate) recordSubTaskDone(ctx context.Context, id, skillName string, sub react.Outcome) {
	if a.Tasks == nil || id == "" {
		return
	}
	result, _ := json.Marshal(map[string]any{
		"skill":        skillName,
		"steps":        sub.TotalSteps,
		"terminate_by": sub.TerminateBy,
		"in_tokens":    sub.TotalUsage.InTokens,
		"out_tokens":   sub.TotalUsage.OutTokens,
	})
	_ = a.Tasks.SetDone(ctx, id, result)
}

// recordSubTaskError 把 sub-task 推进到 error（best-effort）。
func (a *Delegate) recordSubTaskError(ctx context.Context, id string, err error) {
	if a.Tasks == nil || id == "" {
		return
	}
	_ = a.Tasks.SetError(ctx, id, err.Error())
}
