// Package httpapi: 外置 CLI 工具目录只读 handler（HunterAdmin cli_tools 白名单多选器）。
package httpapi

import (
	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/tools/manifest"
)

// ToolingAPI 是工具目录只读 handler 依赖的窄接口；*manifest.Manifest 自动满足。
// 只暴露渲染候选所需的最小方法，不牵连整个 manifest 包内部结构。
type ToolingAPI interface {
	// ByCategory 按 category 分桶（桶内按 name 字典序），供前端分组渲染 cli_tools 候选。
	ByCategory() map[string][]manifest.Tool
}

// toolingToolsHandler 处理 GET /tooling/tools：返回全部外置 CLI 工具（name+category+description），
// 按 category 分组扁平化为单一数组。HunterAdmin 据此渲染 cli_tools 白名单多选器候选。
// 这是配置态候选全集（tools.yaml 真值），不按 domain 过滤——过滤是运行期 buildToolingCatalog 的事。
func toolingToolsHandler(api ToolingAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		byCat := api.ByCategory()
		out := make([]gin.H, 0, 32)
		for cat, tools := range byCat {
			for _, t := range tools {
				out = append(out, gin.H{
					"name":        t.Name,
					"category":    cat,
					"description": t.Description,
				})
			}
		}
		c.JSON(200, gin.H{"tools": out})
	}
}
