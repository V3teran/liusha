// Package httpapi 提供 liusha 控制面 HTTP API：健康检查、登录认证、凭证 CRUD、任务与会话等。
// 除白名单端点外所有路由凭 liusha_session 登录会话 Cookie 访问，由 RequireAuth
// gin middleware 统一拦截（含 SSE stream——同源 EventSource 自动带 Cookie）。
package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/auth"
)

// sessionCookieName 是登录会话 Cookie 名。
const sessionCookieName = "liusha_session"

// gin context 键：中间件写入、/auth 系 handler 读取。
const (
	ctxAccountID   = "auth.account_id"
	ctxAccountName = "auth.username"
)

// RequireAuth 校验 liusha_session 会话 Cookie：命中有效会话（滑动未过期 + 绝对上限内）
// 即放行，并把账号身份写进 gin context；剩余不足半程时顺带滑动续期并同步 Cookie 寿命。
//
// 白名单（无会话可达）：
//   - /healthz       探活高频路径
//   - /favicon.ico   浏览器自动拉取，未注册即 404，不进鉴权避免污染 401 日志
//   - /api/auth/login    登录前当然没有会话
//   - /api/auth/logout   清 HttpOnly Cookie 必须由服务端做，须容忍带着失效会话调用
//
// 初始建号不经 HTTP（cmd/api 启动期用 env 密码建 admin），api 全程没有
// 未认证的建号端点。
// store 为 nil 属装配错误，fail-closed：除白名单外全部 401。
func RequireAuth(store AuthStore, secure bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		// FullPath 是 *已注册路由* 模板；未注册路径返回空串——白名单判断必须用
		// URL.Path 兜底，否则白名单端点在未注册状态下会 401 而非 404/405，
		// 混淆"路由不存在"与"鉴权失败"。
		fp := c.FullPath()
		path := c.Request.URL.Path
		if strings.HasSuffix(fp, "/healthz") ||
			path == "/favicon.ico" ||
			path == "/api/auth/login" ||
			path == "/api/auth/logout" {
			c.Next()
			return
		}

		const unauthorized = "未登录或会话已过期"
		if store == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": unauthorized})
			return
		}
		token, err := c.Cookie(sessionCookieName)
		if err != nil || token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": unauthorized})
			return
		}
		ss, err := store.SessionByToken(c.Request.Context(), token)
		if err != nil {
			// 无效/过期/绝对上限已过统一 401，不向未认证方区分细节。
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": unauthorized})
			return
		}
		// 剩余不足半程则续期（写频至多每天一次每会话）；续期失败不阻断本次请求——
		// 会话当前仍有效，只是不延长。
		if time.Until(ss.ExpiresAt) < auth.SessionRenewAfter {
			if exp, slideErr := store.SlideSession(c.Request.Context(), ss.TokenHash); slideErr == nil {
				setSessionCookie(c, token, secondsUntil(exp), secure)
			}
		}
		c.Set(ctxAccountID, ss.AccountID)
		c.Set(ctxAccountName, ss.Username)
		// 操作者随 request context 传播给服务层（dispatch 等），落审计时如实记录
		// 是谁在操作，而非退化为静态标签。gin 的 c.Set 只在 handler 链可见，
		// 跨层传递必须走 request context。
		c.Request = c.Request.WithContext(audit.WithActor(c.Request.Context(), authActorPrefix+ss.Username))
		c.Next()
	}
}

// setSessionCookie 下发会话 Cookie：HttpOnly（JS 不可读）+ SameSite=Lax（跨站 POST
// 不携带，覆盖 CSRF 面）+ Path=/；secure 由 LIUSHA_COOKIE_SECURE 决定（dev http 须 false）。
func setSessionCookie(c *gin.Context, token string, maxAgeSeconds int, secure bool) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(sessionCookieName, token, maxAgeSeconds, "/", "", secure, true)
}

// clearSessionCookie 过期清除会话 Cookie（登出/改密后；HttpOnly 决定必须服务端清）。
func clearSessionCookie(c *gin.Context, secure bool) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(sessionCookieName, "", -1, "/", "", secure, true)
}

// secondsUntil 把过期时间换算为 Cookie MaxAge 秒数（向上取整，避免提前一秒失配）。
func secondsUntil(exp time.Time) int {
	return int(time.Until(exp).Seconds()) + 1
}
