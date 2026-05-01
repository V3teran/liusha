package sniffer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/tool"
	"github.com/V3teran/liusha/internal/heuristic"
	"github.com/V3teran/liusha/internal/replay"
)

// defaultHeuristicRules 是没指定 rules 时按序尝试的全部启发式规则。
var defaultHeuristicRules = []string{"all_denied", "all_empty", "all_auth_error"}

// HeuristicCheck — BAC ReAct 第三步：对 Session.LastResponses 跑短路规则。
//
// 命中任意一条 rule 即返回 skip=true + 命中规则名 + reason，
// 上层（LLM）据此跳过昂贵的相似度计算与 finding 提交。
type HeuristicCheck struct {
	Session *Session
}

// Name 返回动作名 "heuristic_check"。
func (a *HeuristicCheck) Name() string { return "heuristic_check" }

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

// Execute 解析 args → 取 Session.LastResponses → 按序跑 rule → 命中即返回。
func (a *HeuristicCheck) Execute(_ context.Context, args json.RawMessage) (tool.Result, error) {
	var in struct {
		Rules []string `json:"rules"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return tool.Result{}, fmt.Errorf("解析 heuristic_check 参数失败: %w", err)
		}
	}
	if len(a.Session.LastResponses) == 0 {
		return tool.Result{}, fmt.Errorf("session.LastResponses 为空，请先调 replay_multi_identity")
	}
	if len(in.Rules) == 0 {
		in.Rules = defaultHeuristicRules
	}

	for _, name := range in.Rules {
		rule, ok := lookupRule(name)
		if !ok {
			// 未知规则名静默跳过；schema 已用 enum 约束，这里只是双保险。
			continue
		}
		if skip, reason := rule(a.Session.LastResponses); skip {
			return marshalHeuristic(heuristicOutput{Skip: true, HitRule: name, Reason: reason})
		}
	}
	return marshalHeuristic(heuristicOutput{Skip: false})
}

// lookupRule 把字符串名映射到 heuristic.Rule。
//
// all_auth_error 用 DefaultAuthKeywords（关键词列表 spec §7.3 已固定）；
// 后续若 BAC skill 需要自定义关键词，可在此扩展为 (rules, keywords) 形态。
func lookupRule(name string) (heuristic.Rule, bool) {
	switch name {
	case "all_denied":
		return heuristic.AllDeniedByStatus, true
	case "all_empty":
		return heuristic.AllEmptyResponse, true
	case "all_auth_error":
		return func(rs []replay.Response) (bool, string) {
			return heuristic.AllAuthError(rs, nil)
		}, true
	default:
		return nil, false
	}
}

func marshalHeuristic(out heuristicOutput) (tool.Result, error) {
	enc, err := json.Marshal(out)
	if err != nil {
		return tool.Result{}, fmt.Errorf("序列化 heuristic_check 输出失败: %w", err)
	}
	summary := "heuristic_check skip=false"
	if out.Skip {
		summary = fmt.Sprintf("heuristic_check skip=true rule=%s", out.HitRule)
	}
	return tool.Result{Output: enc, Summary: summary}, nil
}
