// Package httpapi 提供 liusha 控制面 HTTP API：健康检查、凭证 CRUD、session 终止。
// 所有非 /healthz 路由强制 X-API-Key 认证，由 RequireAPIKey gin middleware 统一拦截。
package httpapi

import (
	"crypto/subtle"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// RequireAPIKey 校验 X-API-Key 请求头。/healthz、/dev-config.json、/favicon.ico 放行。
// 比较使用 crypto/subtle.ConstantTimeCompare 避免 timing 侧信道。
//
// /dev-config.json（dev API key 自动填充端点）按设计先于鉴权——它正是用来发 key 的，
// 且仅在 LIUSHA_DEV_AUTOFILL 开启时注册，production 不存在。真正敏感的数据接口仍受保护。
//
// LIUSHA_AUTH_DISABLED 非空时整个中间件退化为直通：所有路由（含 SSE stream）不再校验
// X-API-Key 与 stream cookie——测试期免认证开关，恢复认证 = 去掉该 env，无需改代码。
func RequireAPIKey(expected string, streamSecret []byte) gin.HandlerFunc {
	if os.Getenv("LIUSHA_AUTH_DISABLED") != "" {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		// /healthz、/dev-config.json 直接放行；FullPath 是注册路由模板。
		fp := c.FullPath()
		// FullPath 是 *已注册路由* 模板；未注册路由（如 dev autofill 关闭时的
		// /dev-config.json）会返回空串——必须用 URL.Path 兜底，否则会 401
		// 而非 404，混淆"未配置 dev"与"鉴权失败"。
		path := c.Request.URL.Path
		if strings.HasSuffix(fp, "/healthz") ||
			fp == "/dev-config.json" ||
			path == "/dev-config.json" ||
			path == "/api/dev-config.json" || // dev autofill 实际注册路径（/api 组）
			path == "/favicon.ico" {
			// favicon.ico 浏览器自动拉，没注册即 404；不走鉴权避免污染 401 日志。
			c.Next()
			return
		}
		// SSE stream：EventSource 不能带 header，改用 liusha_stream cookie 鉴权。
		// 仅此路径接受 cookie；cookie 绑 conversationID，须与 URL param 一致。
		// 用后缀匹配：路由挂在 /api 组下，FullPath 实为 /api/conversations/:id/stream——
		// 精确比较漏掉组前缀，cookie 旁路成死代码，EventSource（无法带 header）永远 401。
		if strings.HasSuffix(fp, "/conversations/:id/stream") && len(streamSecret) > 0 {
			if ck, err := c.Cookie(streamCookieName); err == nil &&
				verifyStreamToken(streamSecret, ck, c.Param("id")) {
				c.Next()
				return
			}
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
