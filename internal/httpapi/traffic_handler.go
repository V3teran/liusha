// Package httpapi: 代理捕获流量（proxy_traffic）只读浏览 handler（前端流量模块）。
//
// proxy_traffic 是「先于 task、按 host 归属」的真实用户流量——被分析的输入。此处提供
// 跨全部 host 的全局分页浏览 + 单条详情（含 body），不涉及写入/消费（那是 ingestor / 代理捕获链路的职责）。

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/V3teran/liusha/internal/traffic"
)

// TrafficAPI 是流量浏览 handler 依赖的窄接口；*traffic.ProxyStore 自动满足。
type TrafficAPI interface {
	ListPagedGlobal(ctx context.Context, f traffic.ProxyListFilter) ([]traffic.ProxySummary, error)
	CountGlobal(ctx context.Context, f traffic.ProxyListFilter) (int, error)
	DistinctContentTypes(ctx context.Context) ([]string, error)
	GetByID(ctx context.Context, id int64) (traffic.ProxyTraffic, error)
}

// 流量列表分页默认值/上限。
const (
	defaultTrafficPageSize = 50
	maxTrafficPageSize     = 200
)

// parseTrafficFilter 从 query 解析筛选 + 分页。
//
//	host=<等值> method=<等值,自动upper> path=<glob '*'> content_type=<等值>
//	search=<host|url 子串/通配> status_min= status_max= since=<RFC3339> until=<RFC3339> page= size=
//
// page 缺省/非法=1；size clamp 到 [1,maxTrafficPageSize]。offset 由 (page-1)*size 派生。
// 时间用 RFC3339（前端 Date.toISOString() 直出）。时间与状态码写错时返回错误（handler 回 400）。
func parseTrafficFilter(c *gin.Context) (traffic.ProxyListFilter, int, int, error) {
	page, size := parsePagination(c.Query("page"), c.Query("size"), defaultTrafficPageSize, maxTrafficPageSize)
	f := traffic.ProxyListFilter{
		Host:        strings.TrimSpace(c.Query("host")),
		Method:      strings.TrimSpace(c.Query("method")),
		Path:        strings.TrimSpace(c.Query("path")),
		ContentType: strings.TrimSpace(c.Query("content_type")),
		Search:      strings.TrimSpace(c.Query("search")),
		Limit:       size,
		Offset:      (page - 1) * size,
	}
	var err error
	if f.StatusMin, err = intQuery(c, "status_min"); err != nil {
		return f, page, size, err
	}
	if f.StatusMax, err = intQuery(c, "status_max"); err != nil {
		return f, page, size, err
	}
	since, err := timeQuery(c, "since")
	if err != nil {
		return f, page, size, err
	}
	until, err := timeQuery(c, "until")
	if err != nil {
		return f, page, size, err
	}
	if since != nil {
		f.Since = *since
	}
	if until != nil {
		f.Until = *until
	}
	return f, page, size, nil
}

// listTrafficHandler 处理 GET /traffic?host=&method=&path=&status_min=&status_max=&page=&size=。
//
// 返回瘦摘要（不含 body/headers，列表页从不展示大字段）+ total（同筛选口径），供前端分页表。
//
//	{ "items": [{...summary}], "total": N, "page": P, "size": S }
func listTrafficHandler(api TrafficAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		f, page, size, err := parseTrafficFilter(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		rows, err := api.ListPagedGlobal(ctx, f)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		total, err := api.CountGlobal(ctx, f)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}

		items := make([]gin.H, 0, len(rows))
		for _, v := range rows {
			items = append(items, gin.H{
				"id":           v.ID,
				"host":         v.Host,
				"method":       v.Method,
				"path":         v.Path,
				"url":          v.URL,
				"content_type": v.ContentType,
				"status_code":  v.StatusCode,
				"resp_len":     v.RespLen,
				"captured_at":  v.CapturedAt,
			})
		}
		c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "page": page, "size": size})
	}
}

// trafficContentTypesHandler 处理 GET /traffic/content-types：全表 distinct content_type，供筛选下拉。
func trafficContentTypesHandler(api TrafficAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		cts, err := api.DistinctContentTypes(c.Request.Context())
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		if cts == nil {
			cts = []string{}
		}
		c.JSON(http.StatusOK, gin.H{"content_types": cts})
	}
}

// trafficDetailHandler 处理 GET /traffic/:id：单条完整流量（Burp 式整条 raw 报文文本）+ 消费本条的
// 代理捕获 task 列表（M:N）。每个消费者带上跳转所需的来源、对话 ID（没有对话时为空）与漏洞数。
func trafficDetailHandler(api TrafficAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "id required"})
			return
		}
		v, err := api.GetByID(ctx, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				c.JSON(http.StatusNotFound, gin.H{"error": "traffic not found", "id": id})
				return
			}
			respondErr(c, http.StatusInternalServerError, err)
			return
		}

		consumers := make([]gin.H, 0, len(v.ConsumedBy))
		for _, ct := range v.ConsumedBy {
			consumers = append(consumers, gin.H{
				"task_id":         ct.TaskID,
				"host":            ct.Host,
				"status":          ct.Status,
				"source":          ct.Source,
				"conversation_id": ct.ConversationID,
				"finding_count":   ct.FindingCount,
			})
		}

		c.JSON(http.StatusOK, gin.H{
			"id":           v.ID,
			"host":         v.Host,
			"method":       v.Method,
			"url":          v.URL,
			"path":         v.Path,
			"status_code":  v.StatusCode,
			"resp_len":     v.RespLen,
			"content_type": v.ContentType,
			"request_raw":  string(v.RequestRaw),
			"response_raw": string(v.ResponseRaw),
			"consumed_by":  consumers,
			"captured_at":  v.CapturedAt,
		})
	}
}
