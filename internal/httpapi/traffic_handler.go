// Package httpapi: 代理捕获流量（proxy_traffic）只读浏览 handler（前端流量模块）。
//
// proxy_traffic 是「先于 task、按 host 归属」的真实用户流量——被分析的输入。此处提供
// 跨全部 host 的全局分页浏览 + 单条详情（含 body），不涉及写入/消费（那是 ingestor / passive 链路的职责）。
package httpapi

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/traffic"
)

// TrafficAPI 是流量浏览 handler 依赖的窄接口；*traffic.ProxyStore 自动满足。
type TrafficAPI interface {
	ListPagedGlobal(ctx context.Context, f traffic.ProxyListFilter) ([]traffic.ProxySummary, error)
	CountGlobal(ctx context.Context, f traffic.ProxyListFilter) (int, error)
	DistinctHosts(ctx context.Context) ([]string, error)
	GetByID(ctx context.Context, id int64) (traffic.ProxyTraffic, error)
}

// 流量列表分页默认值/上限。
const (
	defaultTrafficPageSize = 50
	maxTrafficPageSize     = 200
)

// parseTrafficFilter 从 query 解析筛选 + 分页。
//
//	host=<等值> method=<等值,自动upper> path=<glob '*'> status_min= status_max= page= size=
//
// page 缺省/非法=1；size clamp 到 [1,maxTrafficPageSize]。offset 由 (page-1)*size 派生。
func parseTrafficFilter(c *gin.Context) (traffic.ProxyListFilter, int, int) {
	page := atoiOr(c.Query("page"), 1)
	if page < 1 {
		page = 1
	}
	size := atoiOr(c.Query("size"), defaultTrafficPageSize)
	if size < 1 {
		size = defaultTrafficPageSize
	}
	if size > maxTrafficPageSize {
		size = maxTrafficPageSize
	}
	f := traffic.ProxyListFilter{
		Host:      strings.TrimSpace(c.Query("host")),
		Method:    strings.TrimSpace(c.Query("method")),
		Path:      strings.TrimSpace(c.Query("path")),
		StatusMin: atoiOr(c.Query("status_min"), 0),
		StatusMax: atoiOr(c.Query("status_max"), 0),
		Limit:     size,
		Offset:    (page - 1) * size,
	}
	return f, page, size
}

// listTrafficHandler 处理 GET /traffic?host=&method=&path=&status_min=&status_max=&page=&size=。
//
// 返回瘦摘要（不含 body/headers，列表页从不展示大字段）+ total（同筛选口径），供前端分页表。
//
//	{ "items": [{...summary}], "total": N, "page": P, "size": S }
func listTrafficHandler(api TrafficAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		f, page, size := parseTrafficFilter(c)

		rows, err := api.ListPagedGlobal(ctx, f)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		total, err := api.CountGlobal(ctx, f)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		items := make([]gin.H, 0, len(rows))
		for _, v := range rows {
			items = append(items, gin.H{
				"id":          v.ID,
				"host":        v.Host,
				"method":      v.Method,
				"path":        v.Path,
				"status_code": v.StatusCode,
				"duration_ms": v.DurationMs,
				"captured_at": v.CapturedAt,
			})
		}
		c.JSON(200, gin.H{"items": items, "total": total, "page": page, "size": size})
	}
}

// trafficHostsHandler 处理 GET /traffic/hosts：全表 distinct host，供筛选下拉。
func trafficHostsHandler(api TrafficAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		hosts, err := api.DistinctHosts(c.Request.Context())
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		if hosts == nil {
			hosts = []string{}
		}
		c.JSON(200, gin.H{"hosts": hosts})
	}
}

// trafficDetailHandler 处理 GET /traffic/:id：单条完整流量（含 request/response body + headers），供列表钻取。
func trafficDetailHandler(api TrafficAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			c.JSON(400, gin.H{"error": "id required"})
			return
		}
		v, err := api.GetByID(c.Request.Context(), id)
		if err != nil {
			if strings.Contains(err.Error(), "no rows in result set") {
				c.JSON(404, gin.H{"error": "traffic not found", "id": id})
				return
			}
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{
			"id":                 v.ID,
			"host":               v.Host,
			"method":             v.Method,
			"scheme":             v.Scheme,
			"url":                v.URL,
			"path":               v.Path,
			"status_code":        v.StatusCode,
			"duration_ms":        v.DurationMs,
			"consumed_by_task_id": v.ConsumedByTaskID,
			"request_headers":     json.RawMessage(rawOrEmpty(v.RequestHeaders, "{}")),
			"request_body":        string(v.RequestBody),
			"response_headers":    json.RawMessage(rawOrEmpty(v.ResponseHeaders, "{}")),
			"response_body":       string(v.ResponseBody),
			"captured_at":        v.CapturedAt,
		})
	}
}
