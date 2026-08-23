// Package httpapi: 全局漏洞台账 handler（漏洞页）。
//
// 本组端点跨 task/host/scenario 全量拉取，支持 triage 处置流转。
package httpapi

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/finding"
)

// FindingsAPI 是 handler 依赖的窄接口；*finding.Store 自动满足。
type FindingsAPI interface {
	ListAll(ctx context.Context, f finding.LedgerFilter) ([]finding.LedgerRow, error)
	CountAll(ctx context.Context, f finding.LedgerFilter) (int, error)
	DistinctHosts(ctx context.Context) ([]string, error)
	DistinctScenarios(ctx context.Context) ([]string, error)
	UpdateTriage(ctx context.Context, id, status, severity, note string) (finding.VulnFinding, error)
}

// 漏洞台账分页默认值/上限（与流量列表 defaultTrafficPageSize/maxTrafficPageSize 同一口径，
// 前端分页大小选择器 10/50/100 也复用这个上限）。
const (
	defaultFindingPageSize = 50
	maxFindingPageSize     = 200
)

// listFindingsHandler 处理 GET /findings?host=&severity=&status=&scenario_id=&source=&page=&size=。
//
// 全局台账：跨 task/host 列出所有漏洞，按可选维度筛选，created_at desc（最新优先）。
// 空筛选=全量。响应含 scenario_id + triage 处置态，供前端就地流转。
// page 缺省/非法=1；size clamp 到 [1,maxFindingPageSize]，缺省 defaultFindingPageSize。
//
// 响应结构（findingJSON 单一序列化点）：
//
//	{ "total": N, "page": P, "size": S, "findings": [{
//	    "id","seq","severity","summary","host","cwe_id","owasp_category","remediation",
//	    "target":{...},"evidence":{...},"scenario_id":"...","source":"manual|auto",
//	    "status":"open|confirmed|fixed|false_positive|accepted","triage_note","triaged_at",
//	    "created_at"
//	}] }
func listFindingsHandler(api FindingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, _ := strconv.Atoi(c.Query("page"))
		if page < 1 {
			page = 1
		}
		size, _ := strconv.Atoi(c.Query("size"))
		if size < 1 {
			size = defaultFindingPageSize
		}
		if size > maxFindingPageSize {
			size = maxFindingPageSize
		}

		f := finding.LedgerFilter{
			Host:       c.Query("host"),
			Severity:   c.Query("severity"),
			Status:     c.Query("status"),
			ScenarioID: c.Query("scenario_id"),
			Source:     c.Query("source"),
			Limit:      size,
			Offset:     (page - 1) * size,
		}

		rows, err := api.ListAll(c.Request.Context(), f)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		total, err := api.CountAll(c.Request.Context(), f)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		out := make([]gin.H, 0, len(rows))
		for _, r := range rows {
			out = append(out, findingJSON(r))
		}
		c.JSON(200, gin.H{"total": total, "page": page, "size": size, "findings": out})
	}
}

// findingHostsHandler 处理 GET /findings/hosts：全表 distinct host，供筛选下拉。
func findingHostsHandler(api FindingsAPI) gin.HandlerFunc {
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

// findingScenariosHandler 处理 GET /findings/scenarios：全表 distinct scenario_id，供筛选下拉。
func findingScenariosHandler(api FindingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		scenarios, err := api.DistinctScenarios(c.Request.Context())
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		if scenarios == nil {
			scenarios = []string{}
		}
		c.JSON(200, gin.H{"scenarios": scenarios})
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
			c.JSON(400, gin.H{"error": "id required"})
			return
		}
		var body struct {
			Status   string `json:"status"`
			Severity string `json:"severity"`
			Note     string `json:"note"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(400, gin.H{"error": "invalid body: " + err.Error()})
			return
		}
		updated, err := api.UpdateTriage(c.Request.Context(), id, body.Status, body.Severity, body.Note)
		if err != nil {
			msg := err.Error()
			switch {
			case strings.Contains(msg, "not found"):
				c.JSON(404, gin.H{"error": msg, "id": id})
			case strings.Contains(msg, "非法 status"):
				c.JSON(400, gin.H{"error": msg})
			default:
				c.JSON(500, gin.H{"error": msg})
			}
			return
		}
		// 回传更新后的行（含后端权威 triaged_at + 覆盖后的 severity）——前端据此覆盖乐观值，消除时钟偏差。
		// UpdateTriage 返回 VulnFinding（无 scenario_id），补零值即可（前端改处置不依赖 scenario_id）。
		c.JSON(200, gin.H{"ok": true, "finding": findingJSON(finding.LedgerRow{VulnFinding: updated})})
	}
}

// findingJSON 是 finding 响应的单一序列化点（列表 + PATCH 回传共用，防字段漂移）。
// evidence/target 用 json.RawMessage 原样透传（LLM 自由 jsonb，前端通用 KV 渲染）。
func findingJSON(r finding.LedgerRow) gin.H {
	return gin.H{
		"id":             r.ID,
		"seq":            r.Seq,
		"severity":       r.Severity,
		"summary":        r.Summary,
		"host":           r.Host,
		"cwe_id":         r.CWEID,
		"owasp_category": r.OWASPCategory,
		"remediation":    r.Remediation,
		"target":         json.RawMessage(rawOrEmpty(r.Target, "{}")),
		"evidence":       json.RawMessage(rawOrEmpty(r.Evidence, "{}")),
		"scenario_id":    r.ScenarioID,
		"source":         r.Source,
		"status":         r.Status,
		"triage_note":    r.TriageNote,
		"triaged_at":     r.TriagedAt, // *time.Time，未处置为 null
		"created_at":     r.CreatedAt,
	}
}
