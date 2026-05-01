// Package mainreact 提供主 ReAct 用的元工具：classify_traffic / spawn_skill / get_findings。
//
// 包名用 mainreact 而非 main：避免与 Go main package 冲突；语义为「主 ReAct 元工具」。
package mainreact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/tool"
)

// ClassifyTraffic 让 LLM 判流量类型 + 输出可能漏洞清单。
//
// 业务背景：主 ReAct 第 1 步先调它判定流量是什么操作（订单读 / admin 登录 / ...），
// 输出 required_skills 列表，主 LLM 据此决定 spawn 哪些子 ReAct。
type ClassifyTraffic struct {
	LLM llm.Generator
}

// Name 返回工具名 "classify_traffic"。
func (a *ClassifyTraffic) Name() string { return "classify_traffic" }

// Description 给 LLM 的工具描述。
func (a *ClassifyTraffic) Description() string {
	return "用 LLM 判流量的业务类型，输出可能漏洞清单（如 bac/sqli/xss）。" +
		"返回 JSON: {operation, required_skills:[], reasoning}。"
}

// ParametersJSON 工具入参 JSON Schema。
func (a *ClassifyTraffic) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
        "type":"object",
        "properties":{
            "flow_id":{"type":"integer","description":"http_flow.id"},
            "method":{"type":"string"},
            "url":{"type":"string"},
            "headers":{"type":"object","description":"请求头摘要"},
            "response_status":{"type":"integer"},
            "response_body_excerpt":{"type":"string","description":"响应体前 500 字符"}
        },
        "required":["flow_id","method","url"]
    }`)
}

// Execute 调注入 LLM Generator 做分类，返 LLM content 当 summary。
func (a *ClassifyTraffic) Execute(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	var in struct {
		FlowID              int64             `json:"flow_id"`
		Method              string            `json:"method"`
		URL                 string            `json:"url"`
		Headers             map[string]string `json:"headers"`
		ResponseStatus      int               `json:"response_status"`
		ResponseBodyExcerpt string            `json:"response_body_excerpt"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return tool.Result{}, fmt.Errorf("decode args: %w", err)
	}
	if in.FlowID == 0 || in.Method == "" || in.URL == "" {
		return tool.Result{}, errors.New("flow_id/method/url 必填")
	}
	if a.LLM == nil {
		return tool.Result{}, errors.New("ClassifyTraffic: LLM 必填")
	}

	prompt := fmt.Sprintf(`分析以下 HTTP 流量，判断其业务操作类型并输出可能的漏洞清单。

流量信息：
- method: %s
- url: %s
- headers: %v
- response_status: %d
- response_body_excerpt: %s

输出 JSON（不要其他内容）：
{
  "operation": "<业务操作的简短描述，如 order_read / user_profile / admin_login>",
  "required_skills": ["bac", "sqli", "xss"],
  "reasoning": "<10 字以内说明为何选这些 skill>"
}`, in.Method, in.URL, in.Headers, in.ResponseStatus, truncate(in.ResponseBodyExcerpt, 500))

	res, err := a.LLM.Generate(ctx, []llm.Message{{Role: llm.RoleUser, Content: prompt}}, nil)
	if err != nil {
		return tool.Result{}, fmt.Errorf("classify_traffic LLM: %w", err)
	}
	return tool.Result{Summary: res.Content}, nil
}

// truncate 截断字符串到 n 个字符（粗截断）。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
