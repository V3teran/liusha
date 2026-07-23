// Package httpapi: 全局漏洞台账 handler（漏洞管理页）。
//
// 修复历史缺陷：漏洞管理页原走 /sitemap（仅 active），passive 漏洞（占多数）不可见。
// 本组端点跨 task/host/mode 全量拉取，支持 triage 处置流转。
package httpapi

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/finding"
)

// FindingsAPI 是 handler 依赖的窄接口；*finding.Store 自动满足。
type FindingsAPI interface {
	ListAll(ctx context.Context, f finding.LedgerFilter) ([]finding.LedgerRow, error)
	UpdateTriage(ctx context.Context, id, status, severity, note string) (finding.VulnFinding, error)
}

// listFindingsHandler 处理 GET /findings?host=&severity=&status=&mode=。
//
// 全局台账：跨 task/host 列出所有漏洞（active + passive），按可选维度筛选，created_at desc。
// 空筛选=全量。响应含 mode（active/passive）+ triage 处置态，供前端就地流转。
//
// 响应结构（findingJSON 单一序列化点）：
//
//	{ "total": N, "findings": [{
//	    "id","severity","summary","host","cwe_id","owasp_category","remediation",
//	    "target":{...},"evidence":{...},"mode":"active|passive",
//	    "status":"open|confirmed|fixed|false_positive|accepted","triage_note","triaged_at",
//	    "created_at"
//	}] }
func listFindingsHandler(api FindingsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := api.ListAll(c.Request.Context(), finding.LedgerFilter{
			Host:     c.Query("host"),
			Severity: c.Query("severity"),
			Status:   c.Query("status"),
			Mode:     c.Query("mode"),
			Limit:    1000, // 台账全量；1000 远超单实例实际漏洞量
		})
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		out := make([]gin.H, 0, len(rows))
		for _, r := range rows {
			out = append(out, findingJSON(r))
		}
		c.JSON(200, gin.H{"total": len(rows), "findings": out})
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
		// UpdateTriage 返回 VulnFinding（无 mode），补零值即可（前端改处置不依赖 mode）。
		c.JSON(200, gin.H{"ok": true, "finding": findingJSON(finding.LedgerRow{VulnFinding: updated})})
	}
}

// findingJSON 是 finding 响应的单一序列化点（列表 + PATCH 回传共用，防字段漂移）。
// evidence/target 用 json.RawMessage 原样透传（LLM 自由 jsonb，前端通用 KV 渲染）。
func findingJSON(r finding.LedgerRow) gin.H {
	return gin.H{
		"id":             r.ID,
		"severity":       r.Severity,
		"summary":        r.Summary,
		"host":           r.Host,
		"cwe_id":         r.CWEID,
		"owasp_category": r.OWASPCategory,
		"remediation":    r.Remediation,
		"target":         json.RawMessage(rawOrEmpty(r.Target, "{}")),
		"evidence":       json.RawMessage(rawOrEmpty(r.Evidence, "{}")),
		"mode":           r.Mode,
		"status":         r.Status,
		"triage_note":    r.TriageNote,
		"triaged_at":     r.TriagedAt, // *time.Time，未处置为 null
		"created_at":     r.CreatedAt,
	}
}
