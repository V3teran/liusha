package einotools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

const (
	listFlowsDefaultLimit = 50
	listFlowsMaxLimit     = 200
)

// listFlowsArgs 是 list_flows 入参；task/host 注入不在此（host 默认取当前 hunter host）。
// source 过滤已随流量拆表移除：active 经 agentFlowScope 读 agent_traffic，passive 经
// proxyFlowScope 读 proxy_traffic（见 flowsource.go）——工具本身不感知来源，由注入的 FlowLister 决定。
// 全字段可选——必须带 ,omitempty，否则全被误标 required 触发 mimo 400（见 findings.go 详注）。
type listFlowsArgs struct {
	Host      string `json:"host,omitempty"       jsonschema:"description=host filter，留空则用当前 hunter host"`
	Method    string `json:"method,omitempty"     jsonschema:"description=HTTP method 过滤（自动大写）"`
	Path      string `json:"path,omitempty"       jsonschema:"description=path glob 过滤，支持 *（如 /admin/*）"`
	Identity  string `json:"identity,omitempty"   jsonschema:"description=身份名过滤（=browser_use identity / 登录账号名）"`
	Tool      string `json:"tool,omitempty"       jsonschema:"description=发起工具过滤：browser=浏览器抓的（带全凭证，抽凭证用这个）；curl/sqlmap/…"`
	StatusMin int    `json:"status_min,omitempty" jsonschema:"description=响应状态码下界（如 400 → 仅 4xx/5xx）"`
	StatusMax int    `json:"status_max,omitempty" jsonschema:"description=响应状态码上界"`
	Since     string `json:"since,omitempty"      jsonschema:"description=ISO 时间戳，仅看此后流量"`
	Limit     int    `json:"limit,omitempty"      jsonschema:"description=条数上限（默认 50，最大 200）"`
	Offset    int    `json:"offset,omitempty"     jsonschema:"description=分页偏移"`
}

// BuildListFlows 造原生 eino list_flows 工具。流量源已限定 task 范围（agent_traffic），host 默认当前 hunter host。
func BuildListFlows(src FlowLister, hunterHost string) (tool.BaseTool, error) {
	return utils.InferTool(
		"list_flows",
		"列出本次扫描范围内的历史 HTTP 流量摘要。默认按当前 host 过滤，可叠加 method/path/status/since/tool/identity。"+
			"返回 [{id, method, host, path, status, tool, identity, duration_ms, created_at}]——"+
			"其中 tool=browser（含 identity）是浏览器真实交互的高保真流量（字段值真、认证态全，replay 首选模板），"+
			"tool=katana 等爬虫流量是广度线索（值不一定真）；要看完整请求体调 view_flow。",
		func(ctx context.Context, in listFlowsArgs) (map[string]any, error) {
			host := strings.TrimSpace(in.Host)
			if host == "" {
				host = hunterHost
			}

			limit := in.Limit
			if limit <= 0 {
				limit = listFlowsDefaultLimit
			}
			if limit > listFlowsMaxLimit {
				limit = listFlowsMaxLimit
			}
			q := FlowQuery{
				Host:      host,
				Method:    strings.ToUpper(strings.TrimSpace(in.Method)),
				Path:      strings.TrimSpace(in.Path),
				Identity:  strings.TrimSpace(in.Identity),
				Tool:      strings.TrimSpace(in.Tool),
				StatusMin: in.StatusMin,
				StatusMax: in.StatusMax,
				Limit:     limit,
				Offset:    in.Offset,
			}
			if in.Since != "" {
				if t, err := time.Parse(time.RFC3339, in.Since); err == nil {
					q.Since = t
				}
			}

			rows, err := src.ListInScope(ctx, q)
			if err != nil {
				return nil, fmt.Errorf("查询 flow 列表失败: %w", err)
			}
			items := make([]map[string]any, 0, len(rows))
			for _, r := range rows {
				items = append(items, map[string]any{
					"id": r.ID, "method": r.Method, "host": r.Host, "path": r.Path,
					"status": r.StatusCode, "identity": r.Identity,
					"tool": r.Tool, "duration_ms": r.DurationMs, "created_at": r.CreatedAt,
				})
			}
			return map[string]any{"count": len(items), "flows": items}, nil
		})
}

// viewFlowArgs 是 view_flow 入参。
type viewFlowArgs struct {
	ID int64 `json:"id" jsonschema:"required,description=flow id（来自 list_flows 返回）"`
}

// BuildViewFlow 造原生 eino view_flow 工具。流量源已限定 task 范围（跨 task 访问被适配器挡下）。
func BuildViewFlow(src FlowReader) (tool.BaseTool, error) {
	return utils.InferTool(
		"view_flow",
		"拿单条历史流量的完整 raw HTTP 请求 + 响应（headers / body 全有）。"+
			"看 cookie / auth header / token / 完整 payload / 完整响应。**id 来自 list_flows**。",
		func(ctx context.Context, in viewFlowArgs) (map[string]any, error) {
			if in.ID <= 0 {
				return nil, errors.New("id 必填且 > 0")
			}
			f, ok, err := src.GetInScope(ctx, in.ID)
			if err != nil {
				return nil, fmt.Errorf("flow %d 读失败: %w", in.ID, err)
			}
			if !ok {
				return nil, fmt.Errorf("flow %d 不存在或不属于当前 task（拒绝跨 task 访问）", in.ID)
			}
			return map[string]any{
				"id":               f.ID,
				"source":           f.Source,
				"identity":         f.Identity,
				"tool":             f.Tool,
				"host":             f.Host,
				"method":           f.Method,
				"url":              f.URL,
				"path":             f.Path,
				"request_headers":  json.RawMessage(f.RequestHeaders),
				"request_body":     string(f.RequestBody),
				"status_code":      f.StatusCode,
				"response_headers": json.RawMessage(f.ResponseHeaders),
				"response_body":    string(f.ResponseBody),
				"duration_ms":      f.DurationMs,
				"created_at":       f.CreatedAt,
			}, nil
		})
}
