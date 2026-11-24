package httpapi

import (
	"io/fs"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/logx"
)

// Deps 是 NewServer 的注入参数集合。
// Credentials 为 nil 时对应路由不注册（部分场景测试用）。
type Deps struct {
	Credentials CredentialsAPI
	// Auth 是登录认证依赖（*auth.Store 满足窄接口）；nil 时 /auth 路由组不注册且
	// RequireAuth fail-closed（除白名单外全部 401）——生产必配。
	Auth AuthStore
	// AuthAudit 是登录事件审计出口（*audit.Store 满足）；nil 时不落审计（单测），生产必配。
	AuthAudit AuthAudit
	// WebUI 是控制台前端的静态资源（web.Assets()）；nil 时不提供前端。
	WebUI fs.FS

	// CookieSecure 控制会话 Cookie 的 Secure 属性（配置 api.cookie_secure）：经 HTTPS
	// 访问时 true；纯 http 访问须 false（否则浏览器不种）。
	CookieSecure bool
	// CredentialAudit 是凭证库审计出口（*audit.Store 满足）；nil 时变更/明文读取
	// 不落审计（单测），生产必配——凭证库是敏感操作面。
	CredentialAudit CredentialAudit
	// TaskStatus 把会话身份归属的 task 状态反解给凭证详情视图；nil 时会话行
	// 不带任务状态。
	TaskStatus TaskStatusResolver
	// Findings 为 nil 时 /findings 路由不注册。由 api 组件注入 *finding.Store。
	// 全局漏洞台账（人工下发 + 代理捕获全量 + triage 处置），漏洞页用。
	Findings FindingsAPI
	// Workspaces 为 nil 时 /workspaces 路由不注册。由 api 组件注入
	// *workspace.Store——任务工作区的文件检索与 CRUD（沙盒工件）。
	Workspaces WorkspaceFileAPI
	// WorkspaceLabels 给工作区批次 / 任务目录取显示名（nil = 目录按 ID 显示）。
	WorkspaceLabels WorkspaceLabelsAPI
	// Invocations 为 nil 时 /llm/invocations/:eid 路由不注册。
	// 由 api 组件注入 *llminvocation.Store（自动满足 InvocationsAPI 窄接口）。
	Invocations InvocationsAPI
	// 会话式平台（任一为 nil 时对应路由不注册）：
	//   Chat          POST /chat 发起会话扫描（api 组件注入 *dispatch.Service）
	//   Conversations GET /conversations[/:id/messages]（*conversation.Store 满足）
	//   EventStream   GET /conversations/:id/stream SSE（api 组件注入 redis 适配器）
	Chat          ChatAPI
	Conversations ConversationsAPI
	EventStream   EventStream
	// FollowUp 为 nil 时 POST /conversations/:id/messages 不注册（多轮动作续接）。
	FollowUp FollowUpAPI
	// TaskControl 为 nil 时 POST /tasks/:id/control 不注册（任务行「暂停/继续/停止」）。
	// api 组件注入 dispatch 侧适配器：三个操作都只写任务行，运行中的循环按行观察。
	TaskControl TaskControlAPI
	// TaskSteering 为 nil 时运行中引导路由不注册（调整目标 / 指派动作 / 否决动作）。
	TaskSteering TaskSteeringAPI
	// LiveRuns 为 nil 时 GET /tasks/:id/run 不注册（任务当前存活的一轮 run 与它的存活心跳）。
	// api 组件注入 *agentrun.Store。
	LiveRuns LiveRunReader
	// Deleter 为 nil 时 DELETE /conversations/:id 不注册（删会话+消息，不动 scan/finding 成果）。
	Deleter ConversationDeleter
	// Attachments/AttachmentContext 为 nil（或 AttachmentMaxBytes<=0）时附件上传路由不注册。
	// api 组件注入 *attachment.Store + conversation/task 组合体 + conversation.attachment_max_bytes。
	Attachments        AttachmentUploader
	AttachmentContext  AttachmentContext
	AttachmentMaxBytes int64
	// Renamer 为 nil 时 PATCH /conversations/:id 不注册（重命名会话标题）。
	Renamer ConversationRenamer
	// ConfigStore 为 nil 时 agent 配置读取/编辑路由不注册。
	// 由 api 组件注入 *configstore.Store（自动满足 ConfigAPI 窄接口）。
	// 写路径经其失效广播，保证 runner 进程 L1 被动失效。
	ConfigStore ConfigAPI
	// Traffic 为 nil 时代理捕获流量浏览路由（/traffic 系列）不注册。
	// 由 api 组件注入 *traffic.ProxyStore（自动满足 TrafficAPI 窄接口）。只读浏览，无写入。
	Traffic TrafficAPI
	// ToolCatalog 或 ConfigStore 为 nil 时工具目录路由（/tools 系列）不注册——
	// 详情/装配需列举、改写智能体，故与 ConfigStore 同守卫。
	// 由 api 组件注入 *toolstore.Store（自动满足 ToolCatalogAPI 窄接口）。数据源是 tool 表
	// （启动期由代码事实源 reconcile 同步），供前端工具模块检索/展示 + 智能体配置页选工具。
	ToolCatalog ToolCatalogAPI
	// SandboxInventory 为工具目录附沙盒安装状态；nil 时安装状态一律未知（null）。
	SandboxInventory SandboxInventoryAPI
	// Models 为 nil 时模型配置路由（/models 系列）不注册。
	// 由 api 组件注入 *llmstore.Store（自动满足 ModelAPI 窄接口）。
	// provider CRUD + 能力路由；写经其失效广播，各进程下次 For(complexity) 读到最新。
	Models ModelAPI
	// KeyEncrypter 为 nil 时 provider 保存路由（POST/PUT /models/providers*）不注册——
	// 前端直填的明文 API Key 必须先加密才能落库，缺加密器就不该开这条写路径（fail-closed）。
	// 由 api 组件注入 *cryptx.Cipher（自动满足），密钥来自配置 llm.encryption_key（fail-fast）。
	KeyEncrypter KeyEncrypter
	// ProviderDiagnoser 为 nil 时连接诊断路由（POST /models/providers/diagnose）不注册。
	ProviderDiagnoser ProviderDiagnoser
	// RetrievalDiagnoser 为 nil 时检索模型诊断路由（POST /models/retrieval/diagnose）不注册。
	RetrievalDiagnoser RetrievalDiagnoser
	// RetrievalModelIndex 为 nil 时保存检索模型不触发向量收口（重置 + 即时补齐，单测）；
	// api 组件注入知识库服务适配器——与 runner 的周期巡检共用同一份实现。
	RetrievalModelIndex RetrievalModelIndex
	// Settings 为 nil 时系统配置路由（/settings 系列）不注册。
	// 由 api 组件注入 *settingstore.Store（自动满足 SettingsAPI 窄接口）。
	// compaction/runtime 写经失效广播 runner 现读即生效；proxy_filter 写触发 proxy 进程热换过滤链。
	Settings SettingsAPI
	// 任务用量合计（GET /tasks/:id/usage）：与 TaskReader 任一为 nil 则路由不注册。
	// api 组件注入 invocationStore / toolStore（各满足对应窄接口）。
	UsageLLM   LLMUsageAggregator
	UsageTools ToolUsageAggregator
	// SkillStore 为 nil 时 Skill 配置路由（/skills 系列）不注册。
	// 由 api 组件注入 *configstore.Store（L1/L2/DB 多级缓存 + 写失效广播，
	// 自动满足 SkillAPI 窄接口）。Skill 是 Agent 可访问的知识库文档
	// （工具手册、漏洞检测指南等）；运行时 runner 经同一 cfgstore 读（read_skill）。
	SkillStore SkillAPI
	// ScenarioStore 为 nil 时场景路由（/scenarios 系列）不注册；api 组件注入 *configstore.Store。
	ScenarioStore ScenarioAPI
	// Corpus 为 nil（或 ScenarioStore 为 nil）时知识库路由（/corpus 系列）不注册；api 组件注入 *corpus.Service。
	Corpus CorpusAPI
	// RetrievalHealth 为 nil 时知识库状态不带运行降级信息（单测）；
	// api 组件注入 *retrievalstatus.Store（语义检索最近一次失败的跨进程记录）。
	RetrievalHealth RetrievalHealthReader
	// ExplorationGraph 与 TaskReader 都非 nil 时注册探索图路由（/tasks/:id/exploration/*）。
	// 由 api 组件注入 *explorationgraph.Store（自动满足 ExplorationGraphAPI 窄接口）。
	ExplorationGraph ExplorationGraphAPI
	// TaskReader 读任务行，任务名下资源（探索图、用量）据此判定任务存在；注入 *task.Store。
	TaskReader TaskReader
	// Tasks 为 nil 时任务模块读路由（GET /tasks 系列）不注册。
	// 由 api 组件注入 *task.Store（自动满足 TasksAPI 窄接口）——列表/详情
	// JOIN assignment（来源/批次）、conversation（对话链接）、finding/探索图步数派生列。
	Tasks TasksAPI
	// TaskDispatch 为 nil 时 POST /tasks 不注册（「新建任务」弹层的批量/定时下发）。
	// 由 api 组件注入 *dispatch.Service（DispatchBatch / DispatchScheduled）。
	TaskDispatch TaskDispatchAPI
	// Schedules 为 nil 时定时模板管理路由（/schedules 系列：查看 / 启停 / 删除）不注册。
	// 由 api 组件注入 *cronschedule.Store。
	Schedules SchedulesAPI
	// AuditReader 为 nil 时审计查看路由（GET /audit）不注册。
	// 由 api 组件注入 *audit.Store（自动满足窄接口）——audit_log 的唯一读取入口（只读）。
	AuditReader AuditReader
	// Logs 为 nil 时日志查看路由（/logs 系列）不注册。
	// 由 api 组件注入 logx.NewReader(<data_dir>/logs)（自动满足 LogsAPI 窄接口）——
	// 尾读各进程的 JSON lines 日志文件，冷读 + 游标增量轮询。
	Logs LogsAPI
	// Overview 为 nil 时「概览」路由（/overview 系列）不注册。由 api 组件注入 *overview.Store；
	// 组件健康检查用 Runners / Proxy / ProxyTraffic / SandboxInventory / DataStores，缺哪项哪项报 unknown。
	Overview OverviewAPI
	// Runners 列出在线的 runner 实例（由 api 组件注入 *worker.Client）。
	Runners RunnerRegistryAPI
	// ProxyTraffic 统计代理捕获的流量（由 api 组件注入 *traffic.ProxyStore）。
	ProxyTraffic ProxyTrafficAPI
	// DataStores 探测 PostgreSQL 与 Redis（由 api 组件注入 db.DataStores）。
	DataStores DataStoresAPI
	// Proxy 为 nil 时代理接入路由（/proxy 系列）不注册。由 api 组件注入 *proxystatus.Store。
	Proxy ProxyStatusAPI
}

// NewServer 组装 gin 路由：访问日志 + panic 恢复 + 全局会话认证中间件 + 业务路由。
// 返回 http.Handler，便于 httptest.NewServer / 真实 http.Server 复用。
func NewServer(d Deps) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// 代理信任收敛到私网段：部署模型里反代（nginx）与 api 同宿主/同 compose 网络，
	// 其追加的 X-Forwarded-For 可信；直连来源伪造的 XFF 一律忽略。gin 默认信任
	// 0.0.0.0/0——不显式收窄，ClientIP（访问日志与登录限流键）可被任意伪造。
	if err := r.SetTrustedProxies([]string{
		"127.0.0.0/8", "::1/128",
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"fc00::/7",
	}); err != nil {
		apiLog.Error().Err(err).Msg("SetTrustedProxies 失败（静态 CIDR 不应失败）")
	}

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
		// 降为 debug 的两类高频请求：前端静态资源（每次打开页面几十个）；日志查看
		// 端点被服务器日志页自身 3s 增量轮询，info 级会在服务器日志页里自我回灌成噪音墙。
		// 默认视图不出现，排查时仍可按级别开启。
		ev := accessLog.Info()
		if !isAPIPath(c.Request.URL.Path) || strings.HasPrefix(c.Request.URL.Path, "/api/logs") {
			ev = accessLog.Debug()
		}
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

	// panic 恢复：gin 默认 Recovery 写 stderr——文件日志（服务器日志页数据源）看不到
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

	// 认证：/api 下的接口统一校验会话 Cookie（例外见 auth.go）。前端由本服务同源
	// 提供（开发时经 vite dev 代理），无跨域消费方，故不需要 CORS。
	r.Use(RequireAuth(d.Auth, d.CookieSecure))

	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	// 创建 /api 路由组（前端所有请求都带 /api 前缀）
	api := r.Group("/api")
	registerRouteGroups(api, d)

	// 已注册路由之外：/api 下返回 JSON 404，其余交给控制台前端（WebUI 为 nil 时一律 404）。
	if d.WebUI != nil {
		r.NoRoute(newStaticSite(d.WebUI).handle)
	} else {
		r.NoRoute(func(c *gin.Context) {
			if isAPIPath(c.Request.URL.Path) {
				c.JSON(http.StatusNotFound, gin.H{"error": "接口不存在"})
				return
			}
			c.Status(http.StatusNotFound)
		})
	}
	return r
}

// registerRouteGroups 按依赖可选性注册各路由组：
// 每个 nil 依赖对应的能力域整组跳过（增量部署/最小暴露面）。
func registerRouteGroups(r gin.IRouter, d Deps) {
	// 按业务模块注册路由（auth 最先：登录是其余路由的入口前提）
	registerAuthRoutes(r, d)
	registerOverviewRoutes(r, d)
	registerCredentialsRoutes(r, d)
	registerFindingsRoutes(r, d)
	registerLLMInvocationRoutes(r, d)
	registerAgentConfigRoutes(r, d)
	registerSkillRoutes(r, d)
	registerScenarioRoutes(r, d)
	registerCorpusRoutes(r, d)
	registerExplorationGraphRoutes(r, d)
	registerTasksRoutes(r, d)
	registerAuditRoutes(r, d)
	registerLogsRoutes(r, d)
	registerProxyRoutes(r, d)
	registerTrafficRoutes(r, d)
	registerToolCatalogRoutes(r, d)
	registerModelsRoutes(r, d)
	registerSettingsRoutes(r, d)
	registerChatRoutes(r, d)
	registerConversationRoutes(r, d)
	registerUsageRoutes(r, d)
}
