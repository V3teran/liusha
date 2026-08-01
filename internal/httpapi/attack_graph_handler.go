package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/attackgraph"
)

// AttackGraphAPI 是执行图投影的窄接口，handler 只依赖它。*attackgraph.Projector 自动满足。
type AttackGraphAPI interface {
	Project(ctx context.Context, convID, taskID string) (attackgraph.Graph, error)
	ProjectMilestones(ctx context.Context, convID, taskID string) ([]attackgraph.Milestone, error)
}

// attackGraphHandler 处理 GET /attack_graph/:task_id[?conv=<conv_id>]。
//
// 返回执行图 JSON：思维链（想 / 做+得 / agent 节点 + flow 骨干边）
// + 成果链（漏洞节点 + depends_on 边）。图是 read-model 实时投影，不落表（见 docs/attack-graph-design.md）。
//   - conv 可选：缺省时后端按 task 自解析绑定会话（前端不再需要 join 会话列表）；
//     传入则作为显式覆盖。响应内回传 conversation_id + running 供前端钻取/轮询判定。
func attackGraphHandler(api AttackGraphAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		tid := c.Param("task_id")
		if tid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "task_id required"})
			return
		}
		conv := c.Query("conv")

		g, err := api.Project(c.Request.Context(), conv, tid)
		if err != nil {
			msg := err.Error()
			// task / 会话不存在 → 404，让前端区分"没了"与"服务器真坏"。
			// 当前 Project 链路里 ResolveConvByTask 已把 pgx.ErrNoRows 吞成空 convID（不报错），
			// ListMessages/ListByTask 走 Query 而非 QueryRow，此分支理论上不会命中——
			// 保留作防御性兜底（底层实现变化时不静默降级成 500），不视为主要错误路径。
			if strings.Contains(msg, "no rows in result set") {
				c.JSON(http.StatusNotFound, gin.H{"error": msg, "task_id": tid})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": msg})
			return
		}
		c.JSON(http.StatusOK, g)
	}
}

// attackGraphMilestonesHandler 处理 GET /attack_graph/:task_id/milestones[?conv=<conv_id>]。
//
// 返回按子代理聚合的 LLM 里程碑摘要（派生层，异步算）。conv 缺省时按 task 自解析；
// 无绑定会话或未配 LLM 时 400/503。
func attackGraphMilestonesHandler(api AttackGraphAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		tid := c.Param("task_id")
		if tid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "task_id required"})
			return
		}
		conv := c.Query("conv")
		ms, err := api.ProjectMilestones(c.Request.Context(), conv, tid)
		if err != nil {
			switch {
			case errors.Is(err, attackgraph.ErrNoSummarizer):
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()}) // 服务未配 LLM
			case errors.Is(err, attackgraph.ErrNoConversation):
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			}
			return
		}
		c.JSON(http.StatusOK, gin.H{"milestones": ms})
	}
}
