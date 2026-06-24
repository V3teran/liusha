package httpapi

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/attackgraph"
)

// AttackGraphAPI 是执行图投影的窄接口，handler 只依赖它。*attackgraph.Projector 自动满足。
type AttackGraphAPI interface {
	Project(ctx context.Context, convID, ownerType, ownerID string) (attackgraph.Graph, error)
}

// attackGraphHandler 处理 GET /attack_graph/:owner_id?type=<owner_type>&conv=<conv_id>。
//
// 返回执行图 JSON：思维链（想 / 做+得 / agent 节点 + flow 骨干边）
// + 成果链（漏洞节点 + depends_on 边）。图是 read-model 实时投影，不落表（见 docs/attack-graph-design.md）。
//   - type 缺省 active_scan；conv 缺省空（无对话则只出成果链）。
func attackGraphHandler(api AttackGraphAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		oid := c.Param("owner_id")
		if oid == "" {
			c.JSON(400, gin.H{"error": "owner_id required"})
			return
		}
		ownerType := c.DefaultQuery("type", "active_scan")
		conv := c.Query("conv")

		g, err := api.Project(c.Request.Context(), conv, ownerType, oid)
		if err != nil {
			msg := err.Error()
			// owner / 对话不存在 → 404，让前端区分"没了"与"服务器真坏"
			if strings.Contains(msg, "no rows in result set") {
				c.JSON(404, gin.H{"error": msg, "owner_id": oid})
				return
			}
			c.JSON(500, gin.H{"error": msg})
			return
		}
		c.JSON(200, g)
	}
}
