package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/auth"
)

// auth_handler.go：登录认证的 HTTP 入口。
//
//   - POST /auth/login     用户名+密码换会话 Cookie
//   - POST /auth/logout    吊销当前会话并清 Cookie
//   - GET  /auth/me        当前账号（前端路由守卫数据源）
//   - POST /auth/password  改密：吊销全部会话（含当前），全员重新登录
//
// login/logout 两端点在 RequireAuth 白名单内（见 auth.go）；me/password
// 走正常会话校验。初始建号不经 HTTP（api 组件启动期用 env 密码直接建 admin）。

// AuthStore 是登录认证依赖的窄接口；*auth.Store 自动满足。
// RequireAuth 中间件只消费 SessionByToken/SlideSession，其余为各 handler 所需。
// 初始建号（CountAccounts/CreateAccountIfAbsent）不经 HTTP——api 组件启动期
// 直接对具体 store 调用，api 全程没有任何未认证的建号端点。
type AuthStore interface {
	AccountByUsername(ctx context.Context, username string) (auth.Account, error)
	CreateSession(ctx context.Context, accountID int64) (token string, exp time.Time, err error)
	SessionByToken(ctx context.Context, token string) (auth.Session, error)
	SlideSession(ctx context.Context, tokenHash string) (time.Time, error)
	DeleteSession(ctx context.Context, token string) error
	// ReplacePassword 原子改哈希并吊销全部会话（单事务，无中间态）。
	ReplacePassword(ctx context.Context, accountID int64, passwordHash string) error
}

// AuthAudit 是登录事件审计出口（*audit.Store 满足）。nil 时跳过审计（单测），生产必配。
type AuthAudit interface {
	Append(ctx context.Context, e audit.Event) (int64, error)
}

// 审计 actor 前缀与 action 常量。
const (
	authActorPrefix = "user:"
	authActorAnon   = "anonymous"

	authActionLogin       = "auth.login"
	authActionLoginFailed = "auth.login_failed"
	authActionLogout      = "auth.logout"
	authActionPasswd      = "auth.password_change"
)

// loginRequest 是 POST /auth/login 请求体。
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// changePasswordRequest 是 POST /auth/password 请求体。
type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// appendAuthAudit 落一条认证审计事件；meta 非空时并入 metadata（如来源 IP）。
// aud 为 nil 或写入失败只记日志不阻断主流程。
func appendAuthAudit(c *gin.Context, aud AuthAudit, actor, action, targetID string, meta gin.H) {
	if aud == nil {
		return
	}
	var raw json.RawMessage
	if len(meta) > 0 {
		if b, err := json.Marshal(meta); err == nil {
			raw = b
		}
	}
	if _, err := aud.Append(c.Request.Context(), audit.Event{
		Actor:      actor,
		Action:     action,
		TargetKind: "auth",
		TargetID:   targetID,
		Metadata:   raw,
	}); err != nil {
		apiLog.Error().Err(err).Str("action", action).Msg("写认证审计失败")
	}
}

// loginHandler 处理 POST /auth/login：校验凭据，成功签发会话 Cookie。
// 失败统一 401「用户名或密码错误」（防账号枚举）；用户不存在时跑一次
// 等量级 dummy 比对（防时序探测）。连续失败触发限流 429。
func loginHandler(api AuthStore, aud AuthAudit, limiter *loginLimiter, secure bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 未鉴权端点：请求体设上限，防超大 JSON body 打内存（4KB 对用户名+密码绰绰有余）。
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
		var req loginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if !limiter.allow(req.Username, c.ClientIP()) {
			tooManyAttempts(c, limiter.retryAfter(req.Username, c.ClientIP()))
			return
		}
		reject := func() {
			limiter.fail(req.Username, c.ClientIP())
			appendAuthAudit(c, aud, authActorAnon, authActionLoginFailed, req.Username, gin.H{"ip": c.ClientIP()})
			c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		}
		acct, err := api.AccountByUsername(c.Request.Context(), req.Username)
		if errors.Is(err, auth.ErrAccountNotFound) {
			auth.DummyCompare(req.Password)
			reject()
			return
		}
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		if auth.CheckPassword(acct.PasswordHash, req.Password) != nil {
			reject()
			return
		}
		limiter.reset(req.Username, c.ClientIP())
		token, exp, err := api.CreateSession(c.Request.Context(), acct.ID)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		setSessionCookie(c, token, secondsUntil(exp), secure)
		appendAuthAudit(c, aud, authActorPrefix+acct.Username, authActionLogin, acct.Username, gin.H{"ip": c.ClientIP()})
		c.JSON(http.StatusOK, gin.H{"username": acct.Username})
	}
}

// logoutHandler 处理 POST /auth/logout：吊销当前会话并清 Cookie。
// 白名单端点——带着已失效会话调用也须成功（清 HttpOnly Cookie 只能服务端做）。
func logoutHandler(api AuthStore, aud AuthAudit, secure bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if token, err := c.Cookie(sessionCookieName); err == nil && token != "" {
			// 先读会话拿审计 actor（读不到按匿名计），再删行，最后清 Cookie。
			if ss, err := api.SessionByToken(c.Request.Context(), token); err == nil {
				appendAuthAudit(c, aud, authActorPrefix+ss.Username, authActionLogout, ss.Username, nil)
			}
			if err := api.DeleteSession(c.Request.Context(), token); err != nil {
				respondErr(c, http.StatusInternalServerError, err)
				return
			}
		}
		clearSessionCookie(c, secure)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// meHandler 处理 GET /auth/me：返回当前账号（身份由 RequireAuth 注入 context）。
// no-store：会话状态响应绝不允许来自缓存（登出后回退/前进不得读到旧"已登录"）。
func meHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"username": c.GetString(ctxAccountName)})
	}
}

// changePasswordHandler 处理 POST /auth/password：校验当前密码 → 更新哈希 →
// 吊销该账号全部会话（含当前）并清 Cookie。改密即全员下线，不留"except 当前"分支。
func changePasswordHandler(api AuthStore, aud AuthAudit, secure bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req changePasswordRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if msg := validatePassword(req.NewPassword, req.CurrentPassword); msg != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
		username := c.GetString(ctxAccountName)
		accountID := c.GetInt64(ctxAccountID)
		acct, err := api.AccountByUsername(c.Request.Context(), username)
		if errors.Is(err, auth.ErrAccountNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "账号不存在"})
			return
		}
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		if auth.CheckPassword(acct.PasswordHash, req.CurrentPassword) != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "当前密码不正确"})
			return
		}
		hash, err := auth.HashPassword(req.NewPassword)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		if err := api.ReplacePassword(c.Request.Context(), accountID, hash); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		clearSessionCookie(c, secure)
		appendAuthAudit(c, aud, authActorPrefix+username, authActionPasswd, username, nil)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// validatePassword 校验新密码（≥8 字节、≤72 字节 bcrypt 上限、不得与当前相同）。
// current 为空串时跳过"与当前相同"检查（首启建号无当前密码）。
func validatePassword(pw, current string) string {
	if len(pw) < 8 {
		return "密码至少 8 位"
	}
	if len(pw) > 72 {
		return "密码至多 72 字节"
	}
	if current != "" && pw == current {
		return "新密码不能与当前密码相同"
	}
	return ""
}

// tooManyAttempts 回 429 + Retry-After（秒数）。
func tooManyAttempts(c *gin.Context, retryAfterSeconds int) {
	c.Header("Retry-After", strconv.Itoa(retryAfterSeconds))
	c.JSON(http.StatusTooManyRequests, gin.H{"error": "尝试次数过多，请稍后再试"})
}
