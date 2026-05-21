package httpapi

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/graphview"
)

// GraphAPI 是 graph 投影的窄接口，handler 只依赖它。
// *graphview.Projector 自动满足。
type GraphAPI interface {
	Project(ctx context.Context, ownerID, host string) (graphview.View, error)
}

// graphHandler 处理 GET /graph/:owner_id?host=<optional>。
//
// 返回图视图 JSON：origin / endpoint / parameter / finding / goal 节点
// + has_param / vulnerable_to / enables / contributes_to 边。
//
//  owner 可挂多 host：host 缺省时 Projector 列跨 host 的全部 finding；
// 传 ?host=xxx 时按 finding.host 过滤——前端 viewer 通常带 host 选择器调用。
func graphHandler(api GraphAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		eid := c.Param("owner_id")
		if eid == "" {
			c.JSON(400, gin.H{"error": "owner_id required"})
			return
		}
		host := c.Query("host")
		view, err := api.Project(c.Request.Context(), eid, host)
		if err != nil {
			// owner 不存在（被 truncate 或 typo）→ 404 而非 500，
			// 让前端能据状态码区分"该 owner 没了"与"服务器真坏"。
			if strings.Contains(err.Error(), "no rows in result set") {
				c.JSON(404, gin.H{"error": "owner not found", "owner_id": eid})
				return
			}
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, view)
	}
}
