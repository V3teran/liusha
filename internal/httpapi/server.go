package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Deps 是 NewServer 的注入参数集合。
// Credentials / Tasks / Scan 为 nil 时对应路由不注册（部分场景测试用）。
type Deps struct {
	APIKey      string
	Credentials CredentialsAPI
	Tasks       TaskAPI
	// Findings 为 nil 时 /findings 路由不注册。由 cmd/api 注入 *finding.Store。
	// 全局漏洞台账（active+passive 全量 + triage 处置），漏洞页用。
	Findings FindingsAPI
	// Invocations 为 nil 时 /llm/invocations/:eid 路由不注册。
	// 由 cmd/api 注入 *llminvocation.Store（自动满足 InvocationsAPI 窄接口）。
	Invocations InvocationsAPI
	// Scan 为 nil 时 /scan 路由不注册。
	// 由 cmd/api 注入自定义 adapter（包 task store + agent.Store + worker.Client）。
	Scan ScanAPI
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
	// ConfigStore 为 nil 时 executor 配置 CRUD 路由不注册。
	// 由 cmd/api 注入 *configstore.Store（自动满足 ConfigAPI 窄接口）。
	// 写路径经其失效广播，保证 runner 进程 L1 被动失效（见 D7）。
	ConfigStore ConfigAPI
	// Traffic 为 nil 时代理捕获流量浏览路由（/traffic 系列）不注册。
	// 由 cmd/api 注入 *traffic.ProxyStore（自动满足 TrafficAPI 窄接口）。只读浏览，无写入。
	Traffic TrafficAPI
	// TrafficConv 把消费某流量的 task 反解为会话 id（详情消费关系 chip 跳转）；可空（chip 不可跳）。
	// 由 cmd/api 注入 *conversation.Store（满足 TaskConvResolver）。
	TrafficConv TaskConvResolver
	// ToolCatalog 或 ConfigStore 为 nil 时工具目录路由（/tools 系列）不注册——
	// 详情/装配需列举、改写智能体，故与 ConfigStore 同守卫。
	// 由 cmd/api 注入 *cfgtool.Store（自动满足 ToolCatalogAPI 窄接口）。数据源是 tool 表
	// （启动期由代码事实源 reconcile 同步），供前端工具模块检索/展示 + 智能体配置页选工具。
	ToolCatalog ToolCatalogAPI
	// Models 为 nil 时模型模块路由（/models 系列）不注册。
	// 由 cmd/api 注入 *llmstore.Store（自动满足 ModelAPI 窄接口）。
	// provider 部署 CRUD + 角色路由面板；写经其失效广播，runner 进程下次 For(role) 读到最新（见 D7）。
	Models ModelAPI
	// KeyEncrypter 为 nil 时 provider 保存路由（POST/PUT /models/providers*）不注册——
	// 前端直填的明文 API Key 必须先加密才能落库，缺加密器就不该开这条写路径（fail-closed）。
	// 由 cmd/api 注入 *cryptx.Cipher（自动满足），密钥来自 LIUSHA_LLM_KEY_SECRET（fail-fast）。
	KeyEncrypter KeyEncrypter
	// ProviderTester 为 nil 时实连探测路由（POST /models/providers/test|list-models）不注册。
	// 由 cmd/api 注入闭合 llmstore（取已存密钥走多级缓存）+ cryptx（解密）+ internal/llm（建 client 发请求）的适配器。
	// 测试连接发一条最小 chat completion；模型探测 GET {base_url}/models——都是即时反馈，不落库。
	ProviderTester ProviderTester
	// Settings 为 nil 时系统配置路由（/settings 系列）不注册。
	// 由 cmd/api 注入 *settingstore.Store（自动满足 SettingsAPI 窄接口）。
	// compaction/runtime 写经失效广播 runner 现读即生效；proxy_filter 写触发 proxy 进程热换过滤链（见 D7）。
	Settings SettingsAPI
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
	// ControlPlane 为 nil 时任务控制路由（/tasks/:id/control）不注册。
	// 由 cmd/api 注入 *controlplane.Store（人工干预接口）。
	ControlPlane ControlPlaneAPI
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
		r.POST("/tasks/:id/abort", abortTaskHandler(d.Tasks))
		r.GET("/tasks", listTasksHandler(d.Tasks))
	}
	if d.ControlPlane != nil {
		r.POST("/tasks/:id/control", handleCreateControlEvent(d.ControlPlane))
		r.GET("/tasks/:id/control", handleListControlEvents(d.ControlPlane))
		r.GET("/control-events/:eventID", handleGetControlEvent(d.ControlPlane))
	}
	if d.Findings != nil {
		// 静态子路由（/hosts）须先于 :id 类路由注册避免冲突（同 /traffic 组的既有约定）。
		r.GET("/findings", listFindingsHandler(d.Findings))
		r.GET("/findings/hosts", findingHostsHandler(d.Findings))
		r.PATCH("/findings/:id/status", updateFindingStatusHandler(d.Findings))
	}
	if d.Invocations != nil {
		r.GET("/llm/invocations/:task_id", llmInvocationsHandler(d.Invocations))
		r.GET("/llm/invocations/:task_id/invocation/:id", llmInvocationDetailHandler(d.Invocations))
		r.GET("/llm/invocations/:task_id/stat", llmInvocationStatHandler(d.Invocations))
		r.GET("/llm/invocations/:task_id/facets", llmInvocationFacetsHandler(d.Invocations))
	}
	if d.Scan != nil {
		r.POST("/scan", scanHandler(d.Scan))
	}
	if d.ConfigStore != nil {
		// executor 配置 CRUD（前端配置管理页）。路由挂在 root，
		// 与现有约定一致——前端/反代把 /api/* 前缀剥离后打到这里（见 web/vite.config.ts）。
		r.GET("/executors", listExecutorsHandler(d.ConfigStore))
		r.GET("/executors/:id", getExecutorHandler(d.ConfigStore))
		r.POST("/executors", saveExecutorHandler(d.ConfigStore))
		r.PUT("/executors/:id", saveExecutorHandler(d.ConfigStore))
		r.PATCH("/executors/:id/tier", updateExecutorTierHandler(d.ConfigStore))
		r.DELETE("/executors/:id", deleteExecutorHandler(d.ConfigStore))
	}
	if d.Traffic != nil {
		// 代理捕获流量只读浏览（前端流量模块）：全局分页列表 + host 下拉 + 单条详情。
		// :id 与静态子路由都挂在 /traffic 下，静态段（/hosts、/content-types）须先于 /traffic/:id 注册避免冲突。
		r.GET("/traffic", listTrafficHandler(d.Traffic))
		r.GET("/traffic/hosts", trafficHostsHandler(d.Traffic))
		r.GET("/traffic/content-types", trafficContentTypesHandler(d.Traffic))
		r.GET("/traffic/:id", trafficDetailHandler(d.Traffic, d.TrafficConv))
	}
	if d.ToolCatalog != nil && d.ConfigStore != nil {
		// 工具目录（只读检索）+ 工具↔智能体装配（读写下沉后端，单一权威）：
		// 内置 function + 外置 cli 两套体系。前端工具模块检索/分页；详情附全量智能体及
		// 各自 involved（是否已装配）；PUT .../agents/:code 从工具侧装/卸该智能体的该工具。
		// 装配读写都要智能体数据，故与 ConfigStore 同守卫。
		r.GET("/tools", listToolsHandler(d.ToolCatalog))
		r.GET("/tools/:name", getToolHandler(d.ToolCatalog, d.ConfigStore))
		r.PUT("/tools/:name/agents/:code", assignToolHandler(d.ToolCatalog, d.ConfigStore))
	}
	if d.Models != nil {
		// LLM 配置模块（前端「LLM 配置」页）：provider 部署 CRUD + 角色 → provider 直连路由（0099 拆别名层）。
		// provider key 是稳定引用键（角色路由 FK 指向它），故 GET/PUT/DELETE 用 :key 而非自增 id。
		// /providers 下 POST 与 PUT :key 都走 upsert-by-key。routing 一次拉齐全部角色路由供面板全景。
		r.GET("/models/providers", listProvidersHandler(d.Models))
		r.GET("/models/providers/:key", getProviderHandler(d.Models))
		if d.KeyEncrypter != nil {
			// 保存路径必须能加密明文 API Key 才开——fail-closed，缺加密器时前端仍能看列表/路由，
			// 只是没法新建/改密钥（比默默存明文安全）。
			r.POST("/models/providers", saveProviderHandler(d.Models, d.KeyEncrypter))
			r.PUT("/models/providers/:key", saveProviderHandler(d.Models, d.KeyEncrypter))
		}
		r.DELETE("/models/providers/:key", deleteProviderHandler(d.Models))

		r.GET("/models/routing", listRoutingHandler(d.Models))
		r.PUT("/models/routes/:role", saveRoleRouteHandler(d.Models))
		r.DELETE("/models/routes/:role", deleteRoleRouteHandler(d.Models))

		if d.ProviderTester != nil {
			// 实连探测（即时反馈，不落库）：测试连接发最小 chat completion，模型探测 GET {base_url}/models。
			// 缺 tester（如未装配 internal/llm 适配器）时前端仍能 CRUD，只是没有「测试连接」/模型下拉探测。
			r.POST("/models/providers/test", testProviderHandler(d.ProviderTester))
			r.POST("/models/providers/list-models", listProviderModelsHandler(d.ProviderTester))
		}
	}
	if d.Settings != nil {
		// 系统配置模块（前端「系统配置」页）：三组业务旋钮全量读写。
		// compaction/runtime 写后 runner 现读即生效；proxy_filter 写后 proxy 进程热换过滤链。
		r.GET("/settings/compaction", getCompactionSettingsHandler(d.Settings))
		r.PUT("/settings/compaction", putCompactionSettingsHandler(d.Settings))
		r.GET("/settings/runtime", getRuntimeSettingsHandler(d.Settings))
		r.PUT("/settings/runtime", putRuntimeSettingsHandler(d.Settings))
		r.GET("/settings/proxy-filter", getProxyFilterSettingsHandler(d.Settings))
		r.PUT("/settings/proxy-filter", putProxyFilterSettingsHandler(d.Settings))
	}
	if d.Chat != nil {
		r.POST("/chat", chatHandler(d.Chat, d.StreamCookieSecret, d.CookieSecure))
	}
	if d.Conversations != nil {
		r.GET("/conversations", listConversationsHandler(d.Conversations))
		r.GET("/conversations/:id/messages", messagesHandler(d.Conversations))
		r.GET("/conversations/:id/messages/:msg_id", messageDetailHandler(d.Conversations))
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
