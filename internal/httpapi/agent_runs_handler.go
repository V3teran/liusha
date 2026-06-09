// Package httpapi: agent_run 列表 handler（按 commander_id 拼任务树用）。
package httpapi

import (
	"context"
	"encoding/json"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/hunter"
)

// AgentRunsAPI 是 handler 依赖的窄接口；*hunter.Store 自动满足。
type AgentRunsAPI interface {
	ListByOwner(ctx context.Context, ownerType, ownerID string, limit int) ([]hunter.Run, error)
}

// agentRunsHandler 处理 GET /agent_runs/:owner_id。
//
// 返回该 owner 下所有 agent_run 行，按 created_at ASC 排序（orchestrator 先 spawn → exploitation 后入）。
// 前端 viewer 按 commander_id 拼任务树渲染（PR4）：根节点 commander_id="" / NULL。
//
// 响应结构：
//
//	{
//	  "owner_id": "...",
//	  "total": N,
//	  "runs": [{
//	    "id":"uuid", "commander_id":"uuid|''", "role":"traffic-analysis|orchestrator|exploitation",
//	    "status":"pending|running|done|error|aborted",
//	    "input":{...}, "result":{...},
//	    "created_at":"2026-...", "updated_at":"2026-..."
//	  }]
//	}
//
// limit 硬编码 500——单  owner 一般几十到几百 agent_run，500 远超实际需求。
func agentRunsHandler(api AgentRunsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		eid := c.Param("owner_id")
		if eid == "" {
			c.JSON(400, gin.H{"error": "owner_id required"})
			return
		}

		// ListByOwner 在 0 行时返 (空切片, nil)，不返 ErrNoRows——
		// "owner 不存在"与" owner 存在但 0 run"响应相同（total:0, runs:[]），
		// 这对 viewer 树渲染足够（前端基于 total=0 显示"无任务"）。
		runs, err := api.ListByOwner(c.Request.Context(), "", eid, 500)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		out := make([]gin.H, 0, len(runs))
		for _, r := range runs {
			out = append(out, gin.H{
				"id":           r.ID,
				"commander_id": r.CommanderID,
				"role":         r.Role,
				"status":       string(r.Status),
				"input":        json.RawMessage(rawOrEmpty(r.Input, "{}")),
				"result":       json.RawMessage(rawOrEmpty(r.Result, "{}")),
				"created_at":   r.CreatedAt,
				"updated_at":   r.UpdatedAt,
			})
		}

		c.JSON(200, gin.H{
			"owner_id": eid,
			"total":    len(runs),
			"runs":     out,
		})
	}
}
