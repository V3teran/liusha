package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// versionHandler 处理 GET /version：控制台显示的程序版本，与 liusha version 打印的是同一个值。
// 版本由入口在启动时交下来（cmd/liusha 接构建注入的值），前端不自带版本号——前端编在程序里，
// 问运行中的程序最准：升级后浏览器若还留着旧的前端，也不会把旧版本号当成现在跑的版本。
func versionHandler(version string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"version": version})
	}
}
