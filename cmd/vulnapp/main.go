// cmd/vulnapp 是 Plan 2 e2e 测试用的 BAC 靶机。
// 故意保留 6 类越权漏洞（IDOR / 垂直越权 / 未授权访问），
// 供 BAC sniffer 通过代理发现并写入 finding。
package main

import (
	"net/http"

	"github.com/V3teran/liusha/internal/logx"
	"github.com/gin-gonic/gin"
)

// 固定 session → 用户名映射（不安全：靶机故意如此）。
var sessions = map[string]string{
	"admin_sess_a1b2c3":   "admin",
	"test_sess_d4e5f6":    "test",
	"m233241_sess_g7h8i9": "m233241",
}

// 假订单数据：order 7 属于 test，order 9 属于 m233241。
var fakeOrders = map[string]any{
	"7": map[string]any{"id": 7, "owner": "test", "amount": 100},
	"9": map[string]any{"id": 9, "owner": "m233241", "amount": 80},
}

// newRouter 构建 gin 路由（与 main 解耦，便于 httptest 单测）。
func newRouter() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

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

	// /api 组：whoami 中间件解析 cookie，但不强制鉴权（漏洞）。
	authed := r.Group("/api")
	authed.Use(func(c *gin.Context) {
		c.Set("user", whoami(c.Request))
		c.Next()
	})

	authed.GET("/profile", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"me": c.GetString("user")})
	})

	// IDOR：直接回显 uid，不校验调用者身份。
	authed.GET("/user/info", func(c *gin.Context) {
		uid := c.Query("uid")
		c.JSON(http.StatusOK, gin.H{"uid": uid, "name": "u" + uid})
	})

	// IDOR：直接按 :oid 查询，不校验 owner。
	authed.GET("/order/:oid", func(c *gin.Context) {
		v, ok := fakeOrders[c.Param("oid")]
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusOK, v)
	})

	// IDOR：取消订单不校验 owner。
	authed.POST("/order/cancel", func(c *gin.Context) {
		var req struct {
			OrderID string `json:"order_id"`
		}
		_ = c.ShouldBindJSON(&req)
		c.JSON(http.StatusOK, gin.H{"cancelled": req.OrderID})
	})

	// 垂直越权：普通用户也能访问 admin 接口。
	authed.GET("/admin/users", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"users": []string{"admin", "test", "m233241"}})
	})

	// 未授权访问：无 cookie（anonymous）也允许删除。
	authed.POST("/admin/user/delete", func(c *gin.Context) {
		var req struct {
			UID string `json:"uid"`
		}
		_ = c.ShouldBindJSON(&req)
		c.JSON(http.StatusOK, gin.H{"deleted": req.UID, "by": c.GetString("user")})
	})

	return r
}

// whoami 从 cookie 解析用户名，未识别则返回 anonymous。
func whoami(r *http.Request) string {
	c, err := r.Cookie("session")
	if err != nil {
		return "anonymous"
	}
	if u, ok := sessions[c.Value]; ok {
		return u
	}
	return "anonymous"
}

func main() {
	logger := logx.New("vulnapp")
	gin.SetMode(gin.ReleaseMode)

	r := newRouter()

	// 请求日志中间件：便于 e2e 调试。
	r.Use(func(c *gin.Context) {
		c.Next()
		logger.Info().
			Str("method", c.Request.Method).
			Str("path", c.Request.URL.Path).
			Int("status", c.Writer.Status()).
			Str("user", c.GetString("user")).
			Msg("request")
	})

	logger.Info().Str("addr", ":8001").Msg("vulnapp listening")
	if err := r.Run(":8001"); err != nil {
		logger.Fatal().Err(err).Msg("server exited")
	}
}
