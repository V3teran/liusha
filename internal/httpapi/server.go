package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Deps 是 NewServer 的注入参数集合。
// Credentials / Engagements 为 nil 时对应路由不注册（部分场景测试用）。
type Deps struct {
	APIKey      string
	Credentials CredentialsAPI
	Engagements EngagementsAPI
}

// NewServer 组装 gin 路由：Recovery + 全局 X-API-Key 中间件 + 业务路由。
// 返回 http.Handler，便于 httptest.NewServer / 真实 http.Server 复用。
func NewServer(d Deps) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(RequireAPIKey(d.APIKey))

	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	if d.Credentials != nil {
		r.POST("/credential/batch", batchSaveHandler(d.Credentials))
		r.GET("/credential", listCredentialHandler(d.Credentials))
		r.DELETE("/credential", deleteCredentialHandler(d.Credentials))
	}
	if d.Engagements != nil {
		r.POST("/engagement/proxy", createProxyHandler(d.Engagements))
		r.POST("/engagement/:id/abort", abortHandler(d.Engagements))
	}
	return r
}
