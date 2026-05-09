package httpapi

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/graphview"
)

// GraphAPI 是 graph 投影的窄接口，handler 只依赖它。
// *graphview.Projector 自动满足。
type GraphAPI interface {
	Project(ctx context.Context, engagementID, host string) (graphview.View, error)
}

// graphHandler 处理 GET /graph/:engagement_id?host=<optional>。
//
// 返回 Cairn 风格的图视图 JSON：origin / endpoint / parameter / finding / goal 节点
// + has_param / vulnerable_to / enables / contributes_to 边。
//
// host 缺省时由投影器内部回落到 engagement.target_host——前端常见用法是 GET /graph/:eid。
func graphHandler(api GraphAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		eid := c.Param("engagement_id")
		if eid == "" {
			c.JSON(400, gin.H{"error": "engagement_id required"})
			return
		}
		host := c.Query("host")
		view, err := api.Project(c.Request.Context(), eid, host)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, view)
	}
}
