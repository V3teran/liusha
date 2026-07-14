package httpapi

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/sitemap"
)

// SitemapAPI 是 sitemap 投影的窄接口，handler 只依赖它。
// *sitemap.Projector 自动满足。
type SitemapAPI interface {
	Project(ctx context.Context, taskID, host string) (sitemap.View, error)
}

// sitemapHandler 处理 GET /sitemap/:task_id?host=<optional>。
//
// 返回 sitemap 树 JSON：domain → endpoint → findings（embed 在 endpoint 下，无 folder 中间层）。
// 攻击面从 agent_traffic 派生（DistinctRoutesWithRepresentative 去重），不再依赖 endpoint 表。
// **仅 active 模式**——passive task 报错（passive 流量是流水账，前端走 findings 列表视图）。
//
// task 可挂多 host：host 缺省时合并显示该 task 下的全部 host；
// 传 ?host=xxx 时按 finding.host + 派生路由 host 过滤。
func sitemapHandler(api SitemapAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		tid := c.Param("task_id")
		if tid == "" {
			c.JSON(400, gin.H{"error": "task_id required"})
			return
		}
		host := c.Query("host")
		view, err := api.Project(c.Request.Context(), tid, host)
		if err != nil {
			msg := err.Error()
			// task 不存在 / 不是 active 模式 → 404 让前端能区分"该 task 没了/类型错"与"服务器真坏"
			if strings.Contains(msg, "no rows in result set") || strings.Contains(msg, "仅支持 active 模式") {
				c.JSON(404, gin.H{"error": msg, "task_id": tid})
				return
			}
			c.JSON(500, gin.H{"error": msg})
			return
		}
		c.JSON(200, view)
	}
}
