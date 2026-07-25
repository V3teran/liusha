package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Deps 是 NewServer 的注入参数集合。
// Credentials / Tasks / Sitemap / ActiveScan 为 nil 时对应路由不注册（部分场景测试用）。
type Deps struct {
	APIKey      string
	Credentials CredentialsAPI
	Tasks       TaskAPI
	Sitemap     SitemapAPI // 仅 active 模式攻击面树视图
	// Findings 为 nil 时 /findings 路由不注册。由 cmd/api 注入 *finding.Store。
	// 全局漏洞台账（active+passive 全量 + triage 处置），漏洞管理页用。
	Findings FindingsAPI
	// AttackGraph 为 nil 时 /attack_graph/:task_id 路由不注册。
	// 由 cmd/api 注入 *attackgraph.Projector（自动满足 AttackGraphAPI）。
	// 执行图（思维链+成果链）read-model 投影，见 docs/attack-graph-design.md。
	AttackGraph AttackGraphAPI
	// Invocations 为 nil 时 /llm/invocations/:eid 路由不注册。
	// 由 cmd/api 注入 *llminvocation.Store（自动满足 InvocationsAPI 窄接口）。
	Invocations InvocationsAPI
	// ActiveScan 为 nil 时 /scan/active 路由不注册。
	// 由 cmd/api 注入自定义 adapter（包 task store + hunter.Store + worker.Client）。
	ActiveScan ActiveScanAPI
	// 阶段B 会话式平台（任一为 nil 时对应路由不注册）：
	//   Chat          POST /chat 发起会话扫描（cmd/api 注入 chatAdapter）
	//   Conversations GET /conversations[/:id/messages]（*conversation.Store 满足）
	//   EventStream   GET /conversations/:id/stream SSE（cmd/api 注入 redis 适配器）
	Chat          ChatAPI
	Conversations ConversationsAPI
	EventStream   EventStream
	// FollowUp 为 nil 时 POST /conversations/:id/messages 不注册（多轮动作续接）。
	FollowUp FollowUpAPI
	// Abort 为 nil 时 POST /conversations/:id/abort 不注册（停止会话关联扫描）。
	Abort AbortAPI
	// Deleter 为 nil 时 DELETE /conversations/:id 不注册（删会话+消息，不动 scan/finding 成果）。
	Deleter ConversationDeleter
	// Renamer 为 nil 时 PATCH /conversations/:id 不注册（重命名会话标题）。
	Renamer ConversationRenamer
	// Roles 为 nil 时 GET /roles 不注册（场景 role 列表，供前端会话选择）。
	Roles RolesAPI
	// 会话用量合计（GET /conversations/:id/usage）：三者任一为 nil 则路由不注册。
	// cmd/api 注入 convStore / invocationStore / toolStore（各满足对应窄接口）。
	UsageTasks UsageTaskResolver
	UsageLLM   LLMUsageAggregator
	UsageTools ToolUsageAggregator
	// EnableDevAutofill 仅 dev 用：true 时挂 GET /dev-config.json，把 APIKey 明文
	// 暴露给 liusha-ui 自动填充——**production 严禁开启**。
	// 由 cmd/api 读 LIUSHA_DEV_AUTOFILL 环境变量决定。
	EnableDevAutofill bool
	// StreamCookieSecret 给 SSE stream cookie 签名/校验；空则 stream 仅接受 X-API-Key header。
	// 由 cmd/api 读 LIUSHA_STREAM_COOKIE_SECRET 注入。
	StreamCookieSecret []byte
	// CookieSecure 控制 SSE 鉴权 cookie 的 Secure 属性。prod HTTPS 反代应 true；
	// dev http 同源开发设 false（否则浏览器不种）。由 cmd/api 读 LIUSHA_COOKIE_SECURE 注入。
	CookieSecure bool
}

// NewServer 组装 gin 路由：Recovery + 全局 X-API-Key 中间件 + 业务路由。
// 返回 http.Handler，便于 httptest.NewServer / 真实 http.Server 复用。
func NewServer(d Deps) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(RequireAPIKey(d.APIKey, d.StreamCookieSecret))

	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	if d.Credentials != nil {
		r.POST("/credential/batch", batchSaveHandler(d.Credentials))
		r.GET("/credential", listCredentialHandler(d.Credentials))
		r.DELETE("/credential", deleteCredentialHandler(d.Credentials))
	}
	if d.Tasks != nil {
		r.POST("/session/:id/abort", abortHandler(d.Tasks))
		r.GET("/session", listSessionsHandler(d.Tasks))
	}
	if d.Sitemap != nil {
		r.GET("/sitemap/:task_id", sitemapHandler(d.Sitemap))
	}
	if d.Findings != nil {
		r.GET("/findings", listFindingsHandler(d.Findings))
		r.PATCH("/findings/:id/status", updateFindingStatusHandler(d.Findings))
	}
	if d.AttackGraph != nil {
		r.GET("/attack_graph/:task_id", attackGraphHandler(d.AttackGraph))
		r.GET("/attack_graph/:task_id/milestones", attackGraphMilestonesHandler(d.AttackGraph))
	}
	if d.Invocations != nil {
		r.GET("/llm/invocations/:task_id", llmInvocationsHandler(d.Invocations))
	}
	if d.ActiveScan != nil {
		r.POST("/scan/active", activeScanHandler(d.ActiveScan))
	}
	if d.Roles != nil {
		r.GET("/roles", rolesHandler(d.Roles))
	}
	if d.Chat != nil {
		r.POST("/chat", chatHandler(d.Chat, d.StreamCookieSecret, d.CookieSecure))
	}
	if d.Conversations != nil {
		r.GET("/conversations", listConversationsHandler(d.Conversations))
		r.GET("/conversations/:id/messages", messagesHandler(d.Conversations))
		if d.FollowUp != nil {
			r.POST("/conversations/:id/messages", followUpHandler(d.FollowUp))
		}
		if d.Abort != nil {
			r.POST("/conversations/:id/abort", abortConversationHandler(d.Abort))
		}
		if d.Deleter != nil {
			r.DELETE("/conversations/:id", deleteConversationHandler(d.Deleter))
		}
		if d.Renamer != nil {
			r.PATCH("/conversations/:id", renameConversationHandler(d.Renamer))
		}
		if d.EventStream != nil {
			r.GET("/conversations/:id/stream", streamHandler(d.Conversations, d.EventStream))
			// 流鉴权解耦：前端打开会话/重连前调此端点拿/刷新 stream cookie（X-API-Key 保护）。
			if len(d.StreamCookieSecret) > 0 {
				r.POST("/conversations/:id/stream-auth", streamAuthHandler(d.StreamCookieSecret, d.CookieSecure))
			}
		}
	}
	if d.UsageTasks != nil && d.UsageLLM != nil && d.UsageTools != nil {
		r.GET("/conversations/:id/usage", conversationUsageHandler(d.UsageTasks, d.UsageLLM, d.UsageTools))
	}
	if d.EnableDevAutofill && d.APIKey != "" {
		// dev-only：liusha-ui 启动时拉这个端点自动填充 API key，免去手输登录。
		// 未鉴权可访问（middleware 已专门 bypass），故仅 dev 开启、production 严禁。
		key := d.APIKey
		r.GET("/dev-config.json", func(c *gin.Context) {
			c.Header("Cache-Control", "no-store")
			c.JSON(200, gin.H{"api_key": key})
		})
	}
	return r
}
