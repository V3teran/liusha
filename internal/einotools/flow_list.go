package einotools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/V3teran/liusha/internal/flow"
)

// FlowLister 是 list_flows 依赖的最小接口（*flow.Store 自动满足）。
type FlowLister interface {
	ListByOwnerFiltered(ctx context.Context, ownerID string, f flow.ListFilter) ([]flow.FlowSummary, error)
}

const (
	listFlowsDefaultLimit = 50
	listFlowsMaxLimit     = 200
)

// listFlowsArgs 是 list_flows 入参；owner/host 注入不在此（host 默认取当前 hunter host）。
// 全字段可选——必须带 ,omitempty，否则全被误标 required 触发 mimo 400（见 findings.go 详注）。
type listFlowsArgs struct {
	Host      string `json:"host,omitempty"       jsonschema:"description=host filter，留空则用当前 hunter host"`
	Method    string `json:"method,omitempty"     jsonschema:"description=HTTP method 过滤（自动大写）"`
	Path      string `json:"path,omitempty"       jsonschema:"description=path glob 过滤，支持 *（如 /admin/*）"`
	Source    string `json:"source,omitempty"     jsonschema:"enum=external,enum=internal,description=流量来源；external=用户/Burp 抓的，internal=容器内工具抓的真实请求"`
	Identity  string `json:"identity,omitempty"   jsonschema:"description=身份名过滤（=browser_use identity / 登录账号名）"`
	Tool      string `json:"tool,omitempty"       jsonschema:"description=发起工具过滤：browser=浏览器抓的（带全凭证，抽凭证用这个）；curl/sqlmap/…"`
	StatusMin int    `json:"status_min,omitempty" jsonschema:"description=响应状态码下界（如 400 → 仅 4xx/5xx）"`
	StatusMax int    `json:"status_max,omitempty" jsonschema:"description=响应状态码上界"`
	Since     string `json:"since,omitempty"      jsonschema:"description=ISO 时间戳，仅看此后流量"`
	Limit     int    `json:"limit,omitempty"      jsonschema:"description=条数上限（默认 50，最大 200）"`
	Offset    int    `json:"offset,omitempty"     jsonschema:"description=分页偏移"`
}

// BuildListFlows 造原生 eino list_flows 工具。owner 注入（防串库），host 默认当前 hunter host。
func BuildListFlows(store FlowLister, ownerType, ownerID, hunterHost string) (tool.BaseTool, error) {
	return utils.InferTool(
		"list_flows",
		"列出 owner 范围内的历史 HTTP 流量摘要。默认按当前 host 过滤，可叠加 method/path/source/status/since/tool/identity。"+
			"返回 [{id, method, host, path, status, source, tool, identity, duration_ms, created_at}]——"+
			"其中 tool=browser（含 identity）是浏览器真实交互的高保真流量（字段值真、认证态全，replay 首选模板），"+
			"tool=katana 等爬虫流量是广度线索（值不一定真）；要看完整请求体调 view_flow。",
		func(ctx context.Context, in listFlowsArgs) (map[string]any, error) {
			if ownerID == "" {
				return nil, errors.New("list_flows: owner 注入缺失")
			}
			host := strings.TrimSpace(in.Host)
			if host == "" {
				host = hunterHost
			}
			// http_flow.host 是去端口裸 host（统一切分键）；过滤侧剥端口对齐，否则精确匹配落空。
			host = stripHostPort(host)

			limit := in.Limit
			if limit <= 0 {
				limit = listFlowsDefaultLimit
			}
			if limit > listFlowsMaxLimit {
				limit = listFlowsMaxLimit
			}
			filter := flow.ListFilter{
				Host:      host,
				Method:    strings.ToUpper(strings.TrimSpace(in.Method)),
				Path:      strings.TrimSpace(in.Path),
				Source:    strings.TrimSpace(in.Source),
				Identity:  strings.TrimSpace(in.Identity),
				Tool:      strings.TrimSpace(in.Tool),
				StatusMin: in.StatusMin,
				StatusMax: in.StatusMax,
				Limit:     limit,
				Offset:    in.Offset,
			}
			if in.Since != "" {
				if t, err := time.Parse(time.RFC3339, in.Since); err == nil {
					filter.Since = t
				}
			}

			rows, err := store.ListByOwnerFiltered(ctx, ownerID, filter)
			if err != nil {
				return nil, fmt.Errorf("查询 flow 列表失败: %w", err)
			}
			items := make([]map[string]any, 0, len(rows))
			for _, r := range rows {
				items = append(items, map[string]any{
					"id": r.ID, "method": r.Method, "host": r.Host, "path": r.Path,
					"status": r.StatusCode, "source": r.Source, "identity": r.Identity,
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

// BuildViewFlow 造原生 eino view_flow 工具。owner 注入用于跨 owner 隔离校验。
func BuildViewFlow(store FlowReader, ownerType, ownerID string) (tool.BaseTool, error) {
	return utils.InferTool(
		"view_flow",
		"拿单条历史流量的完整 raw HTTP 请求 + 响应（headers / body 全有）。"+
			"看 cookie / auth header / token / 完整 payload / 完整响应。**id 来自 list_flows**。",
		func(ctx context.Context, in viewFlowArgs) (map[string]any, error) {
			if ownerID == "" {
				return nil, errors.New("view_flow: owner 注入缺失")
			}
			if in.ID <= 0 {
				return nil, errors.New("id 必填且 > 0")
			}
			f, err := store.GetByID(ctx, in.ID)
			if err != nil {
				return nil, fmt.Errorf("flow %d 不存在或读失败: %w", in.ID, err)
			}
			if f.OwnerID != ownerID {
				return nil, fmt.Errorf("flow %d 不属于当前 owner（拒绝跨 owner 访问）", in.ID)
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

// stripHostPort 把 host:port 归一化为裸 host，与 http_flow.host 存储键（去端口）对齐。
func stripHostPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}
