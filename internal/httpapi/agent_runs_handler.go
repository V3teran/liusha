// Package httpapi: agent_run 列表 handler（按 parent_id 拼父子树用）。
package httpapi

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/agentrun"
)

// AgentRunsAPI 是 handler 依赖的窄接口；*agentrun.Store 自动满足。
type AgentRunsAPI interface {
	ListByEngagement(ctx context.Context, engagementID string, limit int) ([]agentrun.ReactRun, error)
}

// agentRunsHandler 处理 GET /agent_runs/:engagement_id。
//
// 返回该 engagement 下所有 agent_run 行，按 created_at ASC 排序（父先 spawn → 子后入）。
// 前端 viewer 按 parent_id 拼父子树渲染（PR4）：根节点 parent_id="" / NULL。
//
// 响应结构：
//
//	{
//	  "engagement_id": "...",
//	  "total": N,
//	  "runs": [{
//	    "id":"uuid", "parent_id":"uuid|''", "role":"hunter",
//	    "status":"pending|running|done|error|aborted",
//	    "input":{...}, "result":{...},
//	    "created_at":"2026-...", "updated_at":"2026-..."
//	  }]
//	}
//
// limit 硬编码 500——单 engagement 一般几十到几百 agent_run，500 远超实际需求。
func agentRunsHandler(api AgentRunsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		eid := c.Param("engagement_id")
		if eid == "" {
			c.JSON(400, gin.H{"error": "engagement_id required"})
			return
		}

		runs, err := api.ListByEngagement(c.Request.Context(), eid, 500)
		if err != nil {
			if strings.Contains(err.Error(), "no rows in result set") {
				c.JSON(404, gin.H{"error": "engagement not found", "engagement_id": eid})
				return
			}
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		out := make([]gin.H, 0, len(runs))
		for _, r := range runs {
			out = append(out, gin.H{
				"id":         r.ID,
				"parent_id":  r.ParentID,
				"role":       r.Role,
				"status":     string(r.Status),
				"input":      json.RawMessage(rawOrEmpty(r.Input, "{}")),
				"result":     json.RawMessage(rawOrEmpty(r.Result, "{}")),
				"created_at": r.CreatedAt,
				"updated_at": r.UpdatedAt,
			})
		}

		c.JSON(200, gin.H{
			"engagement_id": eid,
			"total":         len(runs),
			"runs":          out,
		})
	}
}
