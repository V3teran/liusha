package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Deps 是 NewServer 的注入参数集合。
// Credentials / Owners / Graph / ActiveScan 为 nil 时对应路由不注册（部分场景测试用）。
type Deps struct {
	APIKey      string
	Credentials CredentialsAPI
	Owners OwnersAPI
	Graph       GraphAPI
	// Invocations 为 nil 时 /llm/invocations/:eid 路由不注册。
	// 由 cmd/api 注入 *llminvocation.Store（自动满足 InvocationsAPI 窄接口）。
	Invocations InvocationsAPI
	// AgentRuns 为 nil 时 /agent_runs/:eid 路由不注册。
	// 由 cmd/api 注入 *hunter.Store（自动满足 AgentRunsAPI 窄接口）。
	// 前端 viewer 用此 endpoint 按 commander_id 拼任务树（subtask swarm 可观测）。
	AgentRuns AgentRunsAPI
	// ActiveScan 为 nil 时 /scan/active 路由不注册。
	// 由 cmd/api 注入自定义 adapter（包 owner store + hunter.Store + worker.Client）。
	ActiveScan ActiveScanAPI
	// StaticFS 可选：注入时挂 / 路径 serve 静态前端（PR-3 graph viewer SPA）。
	// 为 nil 时不注册——避免 cmd/api 之外的进程意外暴露前端资源。
	StaticFS http.FileSystem
	// EnableDevAutofill 仅 dev 用：true 时挂 GET /viewer/config.json，把 APIKey 明文
	// 暴露给前端 viewer 自动填充——**production 严禁开启**。
	// 由 cmd/api 读 LIUSHA_VIEWER_DEV_KEY 环境变量决定。
	EnableDevAutofill bool
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
	if d.Owners != nil {
		r.POST("/scan/passive", passiveScanHandler(d.Owners))
		r.POST("/session/:id/abort", abortHandler(d.Owners))
		r.GET("/session", listSessionsHandler(d.Owners))
	}
	if d.Graph != nil {
		r.GET("/graph/:owner_id", graphHandler(d.Graph))
	}
	if d.Invocations != nil {
		r.GET("/llm/invocations/:owner_id", llmInvocationsHandler(d.Invocations))
	}
	if d.AgentRuns != nil {
		r.GET("/agent_runs/:owner_id", agentRunsHandler(d.AgentRuns))
	}
	if d.ActiveScan != nil {
		r.POST("/scan/active", activeScanHandler(d.ActiveScan))
	}
	if d.EnableDevAutofill && d.APIKey != "" {
		// dev-only：viewer 启动时拉这个端点自动填充 API key。
		// 路径**不能**放在 /viewer/ 之下——Gin 路由树不允许同前缀下既有具名路径
		// 又有 StaticFS 的 catch-all（panic: catch-all conflicts with existing path）。
		// 用同级 /viewer-config.json 绕开；middleware 已加专门 bypass。
		key := d.APIKey
		r.GET("/viewer-config.json", func(c *gin.Context) {
			c.Header("Cache-Control", "no-store")
			c.JSON(200, gin.H{"api_key": key})
		})
	}
	if d.StaticFS != nil {
		// 静态前端 (PR-3 graph viewer)：挂 /viewer/* 路径，作为前缀 catch-all。
		// 不在 RequireAPIKey 之内——前端 HTML/JS/CSS 是公开资产；
		// 真正的 /graph/:eid API 仍受 X-API-Key 保护。
		r.StaticFS("/viewer", d.StaticFS)
	}
	return r
}
