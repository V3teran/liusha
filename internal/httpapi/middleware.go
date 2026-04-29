// Package httpapi 提供 liusha 控制面 HTTP API：健康检查、凭证 CRUD、engagement 终止。
// 所有非 /healthz 路由强制 X-API-Key 认证，由 RequireAPIKey 中间件统一拦截。
package httpapi

import (
	"crypto/subtle"
	"strings"

	"github.com/gin-gonic/gin"
)

// RequireAPIKey 校验 X-API-Key 请求头。/healthz 路径放行（通过 FullPath 后缀匹配）。
// 比较使用 crypto/subtle.ConstantTimeCompare 避免 timing 侧信道。
func RequireAPIKey(expected string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// /healthz 直接放行；FullPath 是注册路由模板，匹配后缀即可（兼容子路径前缀场景）。
		if strings.HasSuffix(c.FullPath(), "/healthz") {
			c.Next()
			return
		}
		got := c.GetHeader("X-API-Key")
		// expected 为空属于配置错误，直接 401，避免空 key 等价于跳过认证。
		if expected == "" || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			c.AbortWithStatusJSON(401, gin.H{"error": "invalid api key"})
			return
		}
		c.Next()
	}
}
