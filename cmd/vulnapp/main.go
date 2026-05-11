// cmd/vulnapp 是 BAC e2e 测试用的故意漏洞靶机。
//
// 路径前缀 `/api/bac/`：留给 BAC 漏洞类别。后续扩 SQLi 用 `/api/sqli/`、
// SSRF 用 `/api/ssrf/`，与认知/启发式打分规则一一对应，方便加新漏洞类型。
//
// 端点（4 类各 1 个，不重复）：
//
//	GET  /api/bac/profile        — baseline (无漏洞，登录后拿自己资料)
//	GET  /api/bac/order/:oid     — bac.horizontal_priv_esc (不校验 owner)
//	GET  /api/bac/admin/users    — bac.vertical_priv_esc   (普通用户也能进)
//	POST /api/bac/admin/delete   — bac.unauthorized_access (不调 requireLogin)
//
// 鉴权模型（参考 liusha2 的 requireLogin）：
//
//	需登录的接口 → handler 首行调 requireLogin；anonymous 一律返回 200
//	  + {"detail":"Unauthorized..."} 并 abort（按 BAC SKILL.md 规则视为被拒绝）。
//	唯一例外：POST /api/bac/admin/delete —— 故意不调 requireLogin，
//	  让 anonymous 也能成功，用以触发 bac.unauthorized_access。
//
// 命中越权时打 vulnerability 字段日志，便于人工核对真值。
package main

import (
	"net/http"
	"os"

	"github.com/V3teran/liusha/internal/logx"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

const anonymous = "anonymous"

// 固定 session → 用户名映射（不安全：靶机故意如此）。
var sessions = map[string]string{
	"admin_sess_a1b2c3":   "admin",
	"test_sess_d4e5f6":    "test",
	"m233241_sess_g7h8i9": "m233241",
}

// 用户角色（仅用于垂直越权判定）。admin 是高权限，其余是普通用户。
var userRoles = map[string]string{
	"admin":   "admin",
	"test":    "user",
	"m233241": "user",
	anonymous: "anonymous",
}

// 假订单数据：order 7 属于 test，order 9 属于 m233241。
var fakeOrders = map[string]map[string]any{
	"7": {"id": 7, "owner": "test", "amount": 100},
	"9": {"id": 9, "owner": "m233241", "amount": 80},
}

// newRouter 构建 gin 路由（与 main 解耦，便于 httptest 单测）。
func newRouter(logger zerolog.Logger) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	// 健康检查。
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "vulnapp"})
	})

	// 登录：根据用户名颁发固定 session cookie。
	r.POST("/login", func(c *gin.Context) {
		var req struct {
			Name string `json:"name"`
		}
		_ = c.ShouldBindJSON(&req)
		var sess string
		switch req.Name {
		case "admin":
			sess = "admin_sess_a1b2c3"
		case "test":
			sess = "test_sess_d4e5f6"
		case "m233241":
			sess = "m233241_sess_g7h8i9"
		default:
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unknown user"})
			return
		}
		c.Header("Set-Cookie", "session="+sess+"; Path=/")
		c.JSON(http.StatusOK, gin.H{"ok": true, "user": req.Name})
	})

	bac := r.Group("/api/bac")

	// 1. /api/bac/profile：baseline — 登录后回显当前用户。无漏洞。
	bac.GET("/profile", func(c *gin.Context) {
		user := requireLogin(c, logger)
		if user == "" {
			return
		}
		c.JSON(http.StatusOK, gin.H{"me": user})
	})

	// 2. /api/bac/order/:oid：水平越权 — 直接按 oid 查询，不校验 owner。
	bac.GET("/order/:oid", func(c *gin.Context) {
		user := requireLogin(c, logger)
		if user == "" {
			return
		}
		oid := c.Param("oid")
		order, ok := fakeOrders[oid]
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		if owner, _ := order["owner"].(string); owner != user {
			logger.Warn().
				Str("vulnerability", "horizontal_privilege_escalation").
				Str("current_user", user).
				Str("order_owner", owner).
				Str("oid", oid).
				Msg("水平越权访问订单")
		}
		c.JSON(http.StatusOK, order)
	})

	// 3. /api/bac/admin/users：垂直越权 — 普通用户也能访问 admin 接口。
	bac.GET("/admin/users", func(c *gin.Context) {
		user := requireLogin(c, logger)
		if user == "" {
			return
		}
		if userRoles[user] != "admin" {
			logger.Warn().
				Str("vulnerability", "vertical_privilege_escalation").
				Str("current_user", user).
				Str("role", userRoles[user]).
				Msg("垂直越权访问管理接口")
		}
		c.JSON(http.StatusOK, gin.H{"users": []string{"admin", "test", "m233241"}})
	})

	// 4. /api/bac/admin/delete：未授权访问 — 故意不调 requireLogin。
	// anonymous 直接 200 返回；by 字段保留供 main_test 校验。
	bac.POST("/admin/delete", func(c *gin.Context) {
		var req struct {
			UID string `json:"uid"`
		}
		_ = c.ShouldBindJSON(&req)
		user := whoami(c.Request)
		if user == anonymous {
			logger.Warn().
				Str("vulnerability", "unauthorized_access").
				Str("target_uid", req.UID).
				Msg("未授权删除用户")
		}
		c.JSON(http.StatusOK, gin.H{"deleted": req.UID, "by": user})
	})

	return r
}

// requireLogin 从 cookie 解析用户；anonymous 直接 200 返回 detail 并 abort。
//
// 返回 200 而非 401 是参考 liusha2：BAC SKILL.md 把 body 含 "Unauthorized" /
// "Forbidden" 视为被拒绝，相似度比较走 body 文本即可。
func requireLogin(c *gin.Context, logger zerolog.Logger) string {
	user := whoami(c.Request)
	if user == anonymous {
		logger.Debug().
			Str("path", c.Request.URL.Path).
			Str("method", c.Request.Method).
			Msg("未授权访问被拒")
		c.JSON(http.StatusOK, gin.H{"detail": "Unauthorized: Please login first"})
		c.Abort()
		return ""
	}
	return user
}

// whoami 从 cookie 解析用户名，未识别则返回 anonymous。
func whoami(r *http.Request) string {
	c, err := r.Cookie("session")
	if err != nil {
		return anonymous
	}
	if u, ok := sessions[c.Value]; ok {
		return u
	}
	return anonymous
}

func main() {
	logger := logx.New("vulnapp")
	gin.SetMode(gin.ReleaseMode)

	r := newRouter(logger)

	// 请求日志中间件：便于 e2e 调试。
	r.Use(func(c *gin.Context) {
		c.Next()
		logger.Info().
			Str("method", c.Request.Method).
			Str("path", c.Request.URL.Path).
			Int("status", c.Writer.Status()).
			Str("user", whoami(c.Request)).
			Msg("request")
	})

	// 监听端口可通过 VULNAPP_ADDR 覆盖（如远程部署到非默认端口：VULNAPP_ADDR=:38001）。
	addr := os.Getenv("VULNAPP_ADDR")
	if addr == "" {
		addr = ":8001"
	}
	logger.Info().Str("addr", addr).Msg("vulnapp listening")
	if err := r.Run(addr); err != nil {
		logger.Fatal().Err(err).Msg("server exited")
	}
}
