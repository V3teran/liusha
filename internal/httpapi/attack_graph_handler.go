package httpapi

import (
	"context"
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
			c.JSON(400, gin.H{"error": "task_id required"})
			return
		}
		conv := c.Query("conv")

		g, err := api.Project(c.Request.Context(), conv, tid)
		if err != nil {
			msg := err.Error()
			// task / 会话不存在 → 404，让前端区分"没了"与"服务器真坏"
			if strings.Contains(msg, "no rows in result set") {
				c.JSON(404, gin.H{"error": msg, "task_id": tid})
				return
			}
			c.JSON(500, gin.H{"error": msg})
			return
		}
		c.JSON(200, g)
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
			c.JSON(400, gin.H{"error": "task_id required"})
			return
		}
		conv := c.Query("conv")
		ms, err := api.ProjectMilestones(c.Request.Context(), conv, tid)
		if err != nil {
			msg := err.Error()
			if strings.Contains(msg, "未配置 LLM") {
				c.JSON(503, gin.H{"error": msg}) // 服务未配 LLM
				return
			}
			if strings.Contains(msg, "无会话") {
				c.JSON(400, gin.H{"error": msg})
				return
			}
			c.JSON(500, gin.H{"error": msg})
			return
		}
		c.JSON(200, gin.H{"milestones": ms})
	}
}
