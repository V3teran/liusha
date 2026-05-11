// Package httpapi 提供 liusha 控制面 HTTP API：健康检查、凭证 CRUD、engagement 终止。
// 所有非 /healthz 路由强制 X-API-Key 认证，由 RequireAPIKey 中间件统一拦截。
package httpapi

import (
	"crypto/subtle"
	"strings"

	"github.com/gin-gonic/gin"
)

// RequireAPIKey 校验 X-API-Key 请求头。/healthz 与 /viewer/* 路径放行。
// 比较使用 crypto/subtle.ConstantTimeCompare 避免 timing 侧信道。
//
// /viewer/* 放行原因：静态前端资产（HTML/JS/CSS）需要被浏览器作为子资源加载，
// 浏览器不会给 <script src=> / <link href=> 自动添加自定义 header。
// 真正敏感的数据接口（/graph/:eid 等）仍受保护。
func RequireAPIKey(expected string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// /healthz、/viewer/*、/viewer-config.json 直接放行；FullPath 是注册路由模板。
		// /viewer-config.json 不放在 /viewer/ 之下：Gin 路由树不允许同前缀下既有具名
		// 路径又有 StaticFS catch-all（panic: catch-all conflicts），所以放同级。
		fp := c.FullPath()
		// FullPath 是 *已注册路由* 模板；未注册路由（如 dev autofill 关闭时的
		// /viewer-config.json）会返回空串——必须用 URL.Path 兜底，否则会 401
		// 而非 404，混淆"未配置 dev"与"鉴权失败"。
		path := c.Request.URL.Path
		if strings.HasSuffix(fp, "/healthz") ||
			strings.HasPrefix(fp, "/viewer/") ||
			fp == "/viewer-config.json" ||
			path == "/viewer-config.json" ||
			path == "/favicon.ico" {
			// favicon.ico 浏览器自动拉，没注册即 404；不走鉴权避免污染 401 日志。
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
