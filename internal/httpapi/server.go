package httpapi

import (
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/logx"
)

// Deps 是 NewServer 的注入参数集合。
// Credentials 为 nil 时对应路由不注册（部分场景测试用）。
type Deps struct {
	APIKey      string
	Credentials CredentialsAPI
	// CredentialAudit 是凭证库审计出口（*audit.Store 满足）；nil 时变更/明文读取
	// 不落审计（单测），生产必配——凭证库是敏感操作面。
	CredentialAudit CredentialAudit
	// TaskStatus 把会话身份归属的 task 状态反解给凭证详情视图；nil 时会话行
	// 不带任务状态。
	TaskStatus TaskStatusResolver
	// Findings 为 nil 时 /findings 路由不注册。由 cmd/api 注入 *finding.Store。
	// 全局漏洞台账（active+passive 全量 + triage 处置），漏洞页用。
	Findings FindingsAPI
	// Invocations 为 nil 时 /llm/invocations/:eid 路由不注册。
	// 由 cmd/api 注入 *llminvocation.Store（自动满足 InvocationsAPI 窄接口）。
	Invocations InvocationsAPI
	// 会话式平台（任一为 nil 时对应路由不注册）：
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
	// ConfigStore 为 nil 时 agent 配置读取/编辑路由不注册。
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
	// 由 cmd/api 注入闭合 llmstore（取已存密钥走多级缓存）+ cryptx（解密）+ framework/llm（建 client 发请求）的适配器。
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
	// SkillStore 为 nil 时 Skill 配置路由（/skills 系列）不注册。
	// 由 cmd/api 注入 *cache.Store（cfgstore：L1/L2/DB 多级缓存 + 写失效广播，
	// 自动满足 SkillAPI 窄接口）。Skill 是 Agent 可访问的知识库文档
	// （工具手册、漏洞检测指南等）；运行时 runner 经同一 cfgstore 读（read_skill）。
	SkillStore SkillAPI
	// ExplorationGraph 为 nil 时探索图 API 路由（/tasks/:id/graph|nodes|stats）不注册。
	// Phase 1: 探索图 API（e2e 测试迁移专用）。
	// 由 cmd/api 注入 *explorationgraph.Store（自动满足 ExplorationGraphAPI 窄接口）。
	ExplorationGraph ExplorationGraphAPI
	// Tasks 为 nil 时任务模块读路由（GET /tasks 系列）不注册。
	// 由 cmd/api 注入 *task.Store（自动满足 TasksAPI 窄接口）——列表/详情
	// JOIN assignment（来源/批次）、conversation（对话链接）、finding/探索图步数派生列。
	Tasks TasksAPI
	// TaskDispatch 为 nil 时 POST /tasks 不注册（「新建任务」弹层的批量/定时下发）。
	// 由 cmd/api 注入 *scanAdapter（cmd/api/adapters.go 的 DispatchBatch/DispatchScheduled）。
	TaskDispatch TaskDispatchAPI
	// Logs 为 nil 时日志查看路由（/logs 系列）不注册。
	// 由 cmd/api 注入 *logx.NewReader(logx.Dir())（自动满足 LogsAPI 窄接口）——
	// 尾读共享日志卷上各进程的 JSON lines 文件，冷读 + 游标增量轮询。
	Logs LogsAPI
}

// NewServer 组装 gin 路由：Recovery + 全局 X-API-Key 中间件 + 业务路由。
// 返回 http.Handler，便于 httptest.NewServer / 真实 http.Server 复用。
func NewServer(d Deps) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// 请求访问日志：API 可观测性的底座（此前请求级踪迹只有错误响应与 SSE
	// 诊断，成功请求完全无痕）。/healthz 是探活高频路径，跳过防刷屏。
	accessLog := logx.New("httpapi.access")
	r.Use(func(c *gin.Context) {
		if c.Request.URL.Path == "/healthz" {
			c.Next()
			return
		}
		start := time.Now()
		c.Next()
		ev := accessLog.Info()
		if c.Writer.Status() >= http.StatusInternalServerError {
			ev = accessLog.Error()
		}
		ev.Str("method", c.Request.Method).
			Str("path", c.Request.URL.Path).
			Int("status", c.Writer.Status()).
			Int64("latency_ms", time.Since(start).Milliseconds()).
			Str("client", c.ClientIP()).
			Msg("request")
	})

	// panic 恢复：gin 默认 Recovery 写 stderr——文件日志（日志页数据源）看不到
	// 崩溃现场。这里结构化记录 panic 值与调用栈后回 500。
	r.Use(gin.CustomRecovery(func(c *gin.Context, err any) {
		apiLog.Error().
			Interface("panic", err).
			Str("stack", string(debug.Stack())).
			Str("method", c.Request.Method).
			Str("path", c.Request.URL.Path).
			Msg("HTTP handler panic 已恢复")
		c.AbortWithStatus(http.StatusInternalServerError)
	}))

	// CORS middleware for development
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key")
		c.Writer.Header().Set("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})

	r.Use(RequireAPIKey(d.APIKey, d.StreamCookieSecret))

	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	// 创建 /api 路由组（前端所有请求都带 /api 前缀）
	api := r.Group("/api")
	registerRouteGroups(api, d)
	return r
}

// registerRouteGroups 按依赖可选性注册各路由组：
// 每个 nil 依赖对应的能力域整组跳过（增量部署/最小暴露面）。
func registerRouteGroups(r gin.IRouter, d Deps) {
	// 按业务模块注册路由
	registerCredentialsRoutes(r, d)
	registerFindingsRoutes(r, d)
	registerLLMInvocationRoutes(r, d)
	registerAgentConfigRoutes(r, d)
	registerSkillRoutes(r, d)
	registerExplorationGraphRoutes(r, d)
	registerTasksRoutes(r, d)
	registerLogsRoutes(r, d)
	registerTrafficRoutes(r, d)
	registerToolCatalogRoutes(r, d)
	registerModelsRoutes(r, d)
	registerSettingsRoutes(r, d)
	registerChatRoutes(r, d)
	registerConversationRoutes(r, d)
	registerUsageRoutes(r, d)
	registerDevRoutes(r, d)
}
