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
	listTrafficDefaultLimit = 50
	listTrafficMaxLimit     = 200
)

// list_traffic 工具按 source 拆两套入参 schema（方案 b，见 traffic_source.go）：两表列不同，
// 共用一个入参 struct 会对 passive 撒谎（proxy_traffic 无 identity/tool 列，暴露这些过滤器 →
// agent 填了默丢弃 → 误导性 no-op，是幻觉温床）。故 active/passive 各自如实的入参 struct +
// 各自 Build 函数，但对 agent **暴露同一工具名 list_traffic**（agent 单次 run 只见其一）。
// 底层共用 TrafficLister 接口，无逻辑重复。
//
// 全字段可选——必须带 ,omitempty，否则全被误标 required 触发 mimo 400（见 findings.go 详注）。

const listTrafficName = "list_traffic"

// listAgentTrafficArgs 是 active list_traffic 入参：agent_traffic 有 identity/tool/since 列，如实暴露。
type listAgentTrafficArgs struct {
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

// listProxyTrafficArgs 是 passive list_traffic 入参：proxy_traffic 只有 host/method/path/status 列——
// 不含 identity/tool/since，故不暴露（暴露=撒谎）。
type listProxyTrafficArgs struct {
	Host      string `json:"host,omitempty"       jsonschema:"description=host filter，留空则用当前 host"`
	Method    string `json:"method,omitempty"     jsonschema:"description=HTTP method 过滤（自动大写）"`
	Path      string `json:"path,omitempty"       jsonschema:"description=path glob 过滤，支持 *（如 /admin/*）"`
	StatusMin int    `json:"status_min,omitempty" jsonschema:"description=响应状态码下界（如 400 → 仅 4xx/5xx）"`
	StatusMax int    `json:"status_max,omitempty" jsonschema:"description=响应状态码上界"`
	Limit     int    `json:"limit,omitempty"      jsonschema:"description=条数上限（默认 50，最大 200）"`
	Offset    int    `json:"offset,omitempty"     jsonschema:"description=分页偏移"`
}

func clampListLimit(limit int) int {
	if limit <= 0 {
		return listTrafficDefaultLimit
	}
	if limit > listTrafficMaxLimit {
		return listTrafficMaxLimit
	}
	return limit
}

// renderTrafficSummaries 把 TrafficSummary 列表渲染成工具返回值（active/passive 共用）。
func renderTrafficSummaries(rows []TrafficSummary) map[string]any {
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, map[string]any{
			"id": r.ID, "method": r.Method, "host": r.Host, "path": r.Path,
			"status": r.StatusCode, "identity": r.Identity,
			"tool": r.Tool, "duration_ms": r.DurationMs, "created_at": r.CreatedAt,
		})
	}
	return map[string]any{"count": len(items), "traffic": items}
}

// BuildListAgentTraffic 造 active 版 list_traffic：读 agent_traffic，支持 identity/tool/since 过滤。
func BuildListAgentTraffic(src TrafficLister, hunterHost string) (tool.BaseTool, error) {
	return utils.InferTool(
		listTrafficName,
		"列出本次扫描范围内的历史 HTTP 流量摘要。默认按当前 host 过滤，可叠加 method/path/status/since/tool/identity。"+
			"返回 [{id, method, host, path, status, tool, identity, duration_ms, created_at}]——"+
			"其中 tool=browser（含 identity）是浏览器真实交互的高保真流量（字段值真、认证态全，replay 首选模板），"+
			"tool=katana 等爬虫流量是广度线索（值不一定真）；要看完整请求体调 view_traffic。",
		func(ctx context.Context, in listAgentTrafficArgs) (map[string]any, error) {
			host := strings.TrimSpace(in.Host)
			if host == "" {
				host = hunterHost
			}
			q := TrafficQuery{
				Host:      host,
				Method:    strings.ToUpper(strings.TrimSpace(in.Method)),
				Path:      strings.TrimSpace(in.Path),
				Identity:  strings.TrimSpace(in.Identity),
				Tool:      strings.TrimSpace(in.Tool),
				StatusMin: in.StatusMin,
				StatusMax: in.StatusMax,
				Limit:     clampListLimit(in.Limit),
				Offset:    in.Offset,
			}
			if in.Since != "" {
				if t, err := time.Parse(time.RFC3339, in.Since); err == nil {
					q.Since = t
				}
			}
			rows, err := src.ListInScope(ctx, q)
			if err != nil {
				return nil, fmt.Errorf("查询流量列表失败: %w", err)
			}
			return renderTrafficSummaries(rows), nil
		})
}

// BuildListProxyTraffic 造 passive 版 list_traffic：读 proxy_traffic，仅 host/method/path/status 过滤。
// passive 流量已由 handler 全量推进 prompt，本工具是「再过滤」的可选补充，不再是发现流量的必经路径。
func BuildListProxyTraffic(src TrafficLister, hunterHost string) (tool.BaseTool, error) {
	return utils.InferTool(
		listTrafficName,
		"按条件再过滤本批待分析流量（method/path/status）。本批流量已在 prompt 全量列出，"+
			"此工具仅用于流量条数多时按维度筛选定位；要看某条完整请求/响应体调 view_traffic。"+
			"返回 [{id, method, host, path, status, duration_ms, created_at}]。",
		func(ctx context.Context, in listProxyTrafficArgs) (map[string]any, error) {
			host := strings.TrimSpace(in.Host)
			if host == "" {
				host = hunterHost
			}
			q := TrafficQuery{
				Host:      host,
				Method:    strings.ToUpper(strings.TrimSpace(in.Method)),
				Path:      strings.TrimSpace(in.Path),
				StatusMin: in.StatusMin,
				StatusMax: in.StatusMax,
				Limit:     clampListLimit(in.Limit),
				Offset:    in.Offset,
			}
			rows, err := src.ListInScope(ctx, q)
			if err != nil {
				return nil, fmt.Errorf("查询流量列表失败: %w", err)
			}
			return renderTrafficSummaries(rows), nil
		})
}

// viewTrafficArgs 是 view_traffic 入参。
type viewTrafficArgs struct {
	ID int64 `json:"id" jsonschema:"required,description=流量 id（来自 list_traffic 返回或 prompt 列出的流量清单）"`
}

// BuildViewTraffic 造原生 eino view_traffic 工具。流量源已限定 task 范围（跨 task 访问被适配器挡下）。
func BuildViewTraffic(src TrafficReader) (tool.BaseTool, error) {
	return utils.InferTool(
		"view_traffic",
		"拿单条历史流量的完整 raw HTTP 请求 + 响应（headers / body 全有）。"+
			"看 cookie / auth header / token / 完整 payload / 完整响应。**id 来自 list_traffic 或 prompt 流量清单**。",
		func(ctx context.Context, in viewTrafficArgs) (map[string]any, error) {
			if in.ID <= 0 {
				return nil, errors.New("id 必填且 > 0")
			}
			f, ok, err := src.GetInScope(ctx, in.ID)
			if err != nil {
				return nil, fmt.Errorf("流量 %d 读失败: %w", in.ID, err)
			}
			if !ok {
				return nil, fmt.Errorf("流量 %d 不存在或不属于当前 task（拒绝跨 task 访问）", in.ID)
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
