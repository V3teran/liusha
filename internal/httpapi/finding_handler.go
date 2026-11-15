// Package httpapi: 全局漏洞台账 handler（漏洞页）。
//
// 本组端点跨任务 / 主机全量拉取（可按任务等维度筛选），支持 triage 处置流转。

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/finding"
)

// FindingsAPI 是 handler 依赖的窄接口；*finding.Store 自动满足。
type FindingsAPI interface {
	ListAll(ctx context.Context, f finding.LedgerFilter) ([]finding.LedgerRow, error)
	StatFiltered(ctx context.Context, f finding.LedgerFilter) (finding.Stat, error)
	CountAll(ctx context.Context, f finding.LedgerFilter) (int, error)
	GetLedgerRow(ctx context.Context, id string) (finding.LedgerRow, error)
	TaskFacets(ctx context.Context, f finding.LedgerFilter, goalQuery string, limit int) ([]finding.TaskFacet, bool, error)
	UpdateTriage(ctx context.Context, id, status, severity, note string) (finding.Finding, error)
}

// 漏洞台账分页默认值/上限（与流量列表 defaultTrafficPageSize/maxTrafficPageSize 同一口径，
// 前端分页大小选择器 10/50/100 也复用这个上限）。
const (
	defaultFindingPageSize = 50
	maxFindingPageSize     = 200
)

// ledgerFilterParams 解析台账筛选参数（列表与统计共用，筛选口径不漂移）：task（任务 ID，须为
// UUID）/ host / severity / status / source / search（关键词）。非法取值写 400 并返回 ok=false。
func ledgerFilterParams(c *gin.Context) (finding.LedgerFilter, bool) {
	source, ok := sourceParam(c)
	if !ok {
		return finding.LedgerFilter{}, false
	}
	taskID, ok := uuidQuery(c, "task")
	if !ok {
		return finding.LedgerFilter{}, false
	}
	conversationID, ok := uuidQuery(c, "conversation")
	if !ok {
		return finding.LedgerFilter{}, false
	}
	return finding.LedgerFilter{
		TaskID:         taskID,
		ConversationID: conversationID,
		Host:           c.Query("host"),
		Severity:       c.Query("severity"),
		Status:         c.Query("status"),
		Source:         source,
		Search:         c.Query("search"),
	}, true
}

// 任务选项的数量：缺省 20、上限 50——选项靠搜索缩小，不一次全列出。
const (
	defaultTaskFacetLimit = 20
	maxTaskFacetLimit     = 50
)

// listFindingsHandler 处理 GET /findings?task=&conversation=&host=&severity=&status=&source=&search=&page=&size=。
//
// 全局台账：跨 task/host 列出所有漏洞，按可选维度筛选，created_at desc（最新优先）。
// 空筛选=全量。响应含来源、所属会话与 triage 处置态，供前端跳转与就地流转。
// page 缺省/非法=1；size clamp 到 [1,maxFindingPageSize]，缺省 defaultFindingPageSize。
//
// 响应结构（findingJSON 单一序列化点）：
//
//	{ "total": N, "page": P, "size": S, "findings": [{
//	    "id","seq","severity","summary","host","cwe_id","owasp_category","remediation",
//	    "target":{...},"evaluation":{...},"repro":{...},"source":"direct|proxy",
//	    "task_id","conversation_id",
//	    "status":"open|confirmed|fixed|false_positive|accepted","triage_note","triaged_at",
//	    "created_at"
//	}] }
func listFindingsHandler(api FindingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		f, ok := ledgerFilterParams(c)
		if !ok {
			return
		}
		page, size := parsePagination(c.Query("page"), c.Query("size"), defaultFindingPageSize, maxFindingPageSize)
		f.Limit, f.Offset = size, (page-1)*size

		rows, err := api.ListAll(c.Request.Context(), f)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		total, err := api.CountAll(c.Request.Context(), f)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}

		out := make([]gin.H, 0, len(rows))
		for _, r := range rows {
			out = append(out, findingJSON(r))
		}
		c.JSON(http.StatusOK, gin.H{"total": total, "page": page, "size": size, "findings": out})
	}
}

// getFindingHandler 处理 GET /findings/:id：单条漏洞，与列表行同形状（漏洞详情按 ID 打开——
// 不在当前页、刷新、后退回来时用）。不存在 404。
func getFindingHandler(api FindingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		row, err := api.GetLedgerRow(c.Request.Context(), c.Param("id"))
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, findingJSON(row))
	}
}

// updateFindingStatusHandler 处理 PATCH /findings/:id/status，body {status, severity, note}。
//
// 人工处置：status 五态之一（store 层校验，非法返 400）；severity 传空保留扫描原值、非空覆盖；note 可空。
// finding 不存在返 404（区分"没这条"与"服务器坏"）。
func updateFindingStatusHandler(api FindingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "id required"})
			return
		}
		var body struct {
			Status   string `json:"status"`
			Severity string `json:"severity"`
			Note     string `json:"note"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body: " + err.Error()})
			return
		}
		updated, err := api.UpdateTriage(c.Request.Context(), id, body.Status, body.Severity, body.Note)
		if err != nil {
			msg := err.Error()
			switch {
			case strings.Contains(msg, "not found"):
				c.JSON(http.StatusNotFound, gin.H{"error": msg, "id": id})
			case strings.Contains(msg, "非法 status"):
				c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": msg})
			}
			return
		}
		// 回传更新后的行（含后端权威 triaged_at + 覆盖后的 severity）——前端据此覆盖乐观值，消除时钟偏差。
		// UpdateTriage 返回 Finding（不含来源与会话），这两列补零值即可（前端改处置不依赖它们）。
		c.JSON(http.StatusOK, gin.H{"ok": true, "finding": findingJSON(finding.LedgerRow{Finding: updated})})
	}
}

// findingJSON 是 finding 响应的单一序列化点（列表 + PATCH 回传共用，防字段漂移）。
// evidence/target 用 json.RawMessage 原样透传（LLM 自由 jsonb，前端通用 KV 渲染）。
func findingJSON(r finding.LedgerRow) gin.H {
	return gin.H{
		"id":              r.ID,
		"seq":             r.Seq,
		"severity":        r.Severity,
		"summary":         r.Summary,
		"host":            r.Host,
		"cwe_id":          r.CWEID,
		"owasp_category":  r.OWASPCategory,
		"remediation":     r.Remediation,
		"target":          json.RawMessage(rawOrEmpty(r.Target, "{}")),
		"evaluation":      json.RawMessage(rawOrEmpty(r.Evaluation, "{}")),
		"repro":           json.RawMessage(rawOrEmpty(r.Repro, "{}")),
		"source":          r.Source,
		"task_id":         r.TaskID,
		"result_node_id":  r.ResultNodeID,
		"conversation_id": r.ConversationID,
		"status":          r.Status,
		"triage_note":     r.TriageNote,
		"triaged_at":      r.TriagedAt, // *time.Time，未处置为 null
		"first_seen_at":   r.FirstSeenAt,
		"created_at":      r.CreatedAt,
	}
}

// findingTaskFacetsHandler 处理 GET /findings/tasks?search=&limit=&host=&severity=&status=&source=：
// 台账「任务」筛选的选项——有漏洞的任务（目标、创建时间与漏洞数），结构化筛选照常生效。
// 这个资源是任务，search 在这里按任务目标搜索（不是台账关键词）；has_more 表示还有未列出的。
func findingTaskFacetsHandler(api FindingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		f, ok := ledgerFilterParams(c)
		if !ok {
			return
		}
		goalQuery := f.Search
		f.Search = ""
		limit := min(parseLimit(c, defaultTaskFacetLimit), maxTaskFacetLimit)
		facets, more, err := api.TaskFacets(c.Request.Context(), f, goalQuery, limit)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		out := make([]gin.H, 0, len(facets))
		for _, fc := range facets {
			out = append(out, gin.H{"id": fc.TaskID, "goal": fc.Goal, "created_at": fc.CreatedAt, "count": fc.Count})
		}
		c.JSON(http.StatusOK, gin.H{"tasks": out, "has_more": more})
	}
}

// findingStatHandler 处理 GET /findings/stat?task=&conversation=&host=&severity=&status=&source=：
// 与列表同筛选口径的 status/severity 聚合（台账头部统计 chip 数据源）。
func findingStatHandler(api FindingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		f, ok := ledgerFilterParams(c)
		if !ok {
			return
		}
		st, err := api.StatFiltered(c.Request.Context(), f)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, st)
	}
}
