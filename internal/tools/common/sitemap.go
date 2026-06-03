package common

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/sitemap"
	toolfx "github.com/V3teran/liusha/internal/toolruntime"
)

// SitemapRouteReader 是 ListSitemap 工具依赖的最小接口。
// *flow.Store 自动满足（DistinctRoutes 查 http_flow source=internal 去重路由）。
type SitemapRouteReader interface {
	DistinctRoutes(ctx context.Context, ownerID, host string) ([]flow.RouteRow, error)
}

// ListSitemap — commander/striker 读取本 (owner, host) 攻击面（从流量自动派生）。
//
// 取代旧 read_endpoints：攻击面不再靠手动 write_endpoint 转写，而是 recon 工具流量
// （mitmproxy/CDP）自动入 http_flow(source=internal) 后按 (method, templatize(path)) 去重派生。
//
// commander 用途（核心）：
//   - 持续思考阶段：对整个渗透任务有全局体感（哪些攻击面已被流量覆盖）
//   - done 前自检（**striker 全 done 后必再读一次**）：对照 list_strikers brief 历史判断哪些路由还没派 striker
//
// striker 用途：
//   - 自查 brief 是否已被 commander/同辈 striker 覆盖
//   - 看相邻路由找 chaining 线索
//
// 与 list_flows 边界：list_sitemap 是去重后的「攻击面规划视图」（distinct 路由）；
// list_flows 是原始流量浏览（含 status/时间/可按 source 过滤）。要看某路由的参数/body 用 list_flows + view_flow。
type ListSitemap struct {
	Store   SitemapRouteReader
	OwnerID string
	Host    string
}

// Name 返回工具名 "list_sitemap"。
func (a *ListSitemap) Name() string { return "list_sitemap" }

func (a *ListSitemap) Description() string {
	return "读取本 (owner, host) 范围的攻击面 sitemap（从 http_flow 流量自动派生去重，非手动维护）。" +
		"\n\n【返回】每个路由: {method, path}（path 已模板化 /user/1 → /user/:id 去重）。要看该路由的参数/请求体调 list_flows + view_flow。" +
		"\n【何时用】" +
		"\n- commander 持续思考：对攻击面有全局体感，决定 spawn 哪个 striker / 是否还有路由没覆盖" +
		"\n- commander done 前自检（**striker 全 done 后必再读一次**）：对照 list_strikers brief 历史看是否所有路由都派过 striker" +
		"\n- striker 自查：本 brief 范围是否已被 commander/同辈覆盖（dedup 防重复挖）" +
		"\n\n【设计要点】sitemap 是流量派生的去重攻击面——recon 工具走过的路由自动成图，无需手动登记。同一路由的 ID 变体（/user/1、/user/2）合并为 /user/:id。"
}

// ParametersJSON 返回空对象 schema（攻击面是 owner+host 全量派生，无参数）。
func (a *ListSitemap) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

// Execute 拉取本 (owner, host) 派生路由，TemplatizePath 去重后返回紧凑 JSON。
func (a *ListSitemap) Execute(ctx context.Context, _ json.RawMessage) (toolfx.Result, error) {
	routes, err := a.Store.DistinctRoutes(ctx, a.OwnerID, a.Host)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("读取 sitemap 失败: %w", err)
	}

	// DistinctRoutes 已 SQL 去重 (host, method, raw_path)，这里 TemplatizePath 再折叠 ID 变体后二次去重。
	type route struct {
		Method string `json:"method"`
		Path   string `json:"path"`
	}
	seen := map[string]bool{}
	out := make([]route, 0, len(routes))
	for _, r := range routes {
		method := strings.ToUpper(r.Method)
		tpath := sitemap.TemplatizePath(r.Path)
		key := method + " " + tpath
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, route{Method: method, Path: tpath})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})

	body, _ := json.Marshal(out)
	summary := fmt.Sprintf("list_sitemap count=%d", len(out))
	return toolfx.Result{Output: body, Summary: summary}, nil
}
