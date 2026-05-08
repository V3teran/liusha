package probe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/heuristic"
	"github.com/V3teran/liusha/internal/replay"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// defaultHeuristicRules 是没指定 rules 时按序尝试的全部启发式规则。
// 仅保留"业务层 BAC 失败"短路三条；相似度类判定由 compute_similarity 工具承担，
// SQLi 类"有漏洞短路"已下线（误报率高且与 BAC 短路语义反向，让 LLM 直接读 body 判定）。
var defaultHeuristicRules = []string{
	"all_denied",
	"all_empty",
	"all_auth_error",
}

// HeuristicCheck — BAC ReAct 第三步：对 ProbeState.LastResponses 跑短路规则。
//
// 命中任意一条 rule 即返回 skip=true + 命中规则名 + reason，
// 上层（LLM）据此跳过昂贵的相似度计算与 finding 提交。
//
// AuthKeywords 透传给 all_auth_error 规则；nil/空时 heuristic.AllAuthError 内部回退
// 到 heuristic.DefaultAuthKeywords。caller 通常从 cfg.Heuristic.AuthKeywords 注入。
type HeuristicCheck struct {
	State        *ProbeState
	AuthKeywords []string
}

// Name 返回动作名 "check_heuristics"。
func (a *HeuristicCheck) Name() string { return "check_heuristics" }

// Description 给 LLM 看的简介。
func (a *HeuristicCheck) Description() string {
	return "对上一次 replay 输出跑短路规则；命中任一规则即返回 skip=true，省一次相似度+LLM 判定。"
}

// ParametersJSON 给出可选 rules 数组（默认全部跑）。
func (a *HeuristicCheck) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties": {
    "rules":{
      "type":"array",
      "items":{"type":"string","enum":["all_denied","all_empty","all_auth_error"]},
      "description":"按顺序尝试的启发式规则名；省略则全跑。"
    }
  }
}`)
}

// heuristicOutput 是 Result.Output 的统一结构。Skip=false 时 HitRule/Reason 留空。
type heuristicOutput struct {
	Skip    bool   `json:"skip"`
	HitRule string `json:"hit_rule,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// Execute 解析 args → 取 ProbeState.LastResponses → 按序跑 rule → 命中即返回。
func (a *HeuristicCheck) Execute(_ context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		Rules []string `json:"rules"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return toolfx.Result{}, fmt.Errorf("解析 check_heuristics 参数失败: %w", err)
		}
	}
	if len(a.State.LastResponses) == 0 {
		return toolfx.Result{}, fmt.Errorf("state.LastResponses 为空，请先调 run_replay")
	}
	if len(in.Rules) == 0 {
		in.Rules = defaultHeuristicRules
	}

	// AllResponses 把 LastFlow 抓包响应作为 _original_ 锚点注入头部；
	// 没有抓包响应时退化为原 LastResponses，baseline 类规则会自身退化为 false。
	responses := a.State.AllResponses()
	for _, name := range in.Rules {
		rule, ok := a.lookupRule(name)
		if !ok {
			// 未知规则名静默跳过；schema 已用 enum 约束，这里只是双保险。
			continue
		}
		if skip, reason := rule(responses); skip {
			return marshalHeuristic(heuristicOutput{Skip: true, HitRule: name, Reason: reason})
		}
	}
	return marshalHeuristic(heuristicOutput{Skip: false})
}

// lookupRule 把字符串名映射到 heuristic.Rule。
//
// all_auth_error 用 a.AuthKeywords（cfg.Heuristic.AuthKeywords 透传）；
// nil/空时由 heuristic.AllAuthError 内部回退 heuristic.DefaultAuthKeywords。
func (a *HeuristicCheck) lookupRule(name string) (heuristic.Rule, bool) {
	switch name {
	case "all_denied":
		return heuristic.AllDeniedByStatus, true
	case "all_empty":
		return heuristic.AllEmptyResponse, true
	case "all_auth_error":
		return func(rs []replay.Response) (bool, string) {
			return heuristic.AllAuthError(rs, a.AuthKeywords)
		}, true
	default:
		return nil, false
	}
}

func marshalHeuristic(out heuristicOutput) (toolfx.Result, error) {
	enc, err := json.Marshal(out)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("序列化 check_heuristics 输出失败: %w", err)
	}
	summary := "check_heuristics skip=false"
	if out.Skip {
		summary = fmt.Sprintf("check_heuristics skip=true rule=%s", out.HitRule)
	}
	return toolfx.Result{Output: enc, Summary: summary}, nil
}
