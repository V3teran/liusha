package httpapi

import "github.com/gin-gonic/gin"

// registerCredentialsRoutes 注册凭证管理路由
func registerCredentialsRoutes(r *gin.Engine, d Deps) {
	if d.Credentials == nil {
		return
	}
	r.POST("/credential/batch", batchSaveHandler(d.Credentials))
	r.GET("/credential", listCredentialHandler(d.Credentials))
	r.DELETE("/credential", deleteCredentialHandler(d.Credentials))
}

// registerTaskRoutes 注册任务管理路由
func registerTaskRoutes(r *gin.Engine, d Deps) {
	if d.Tasks == nil {
		return
	}
	r.POST("/tasks/:id/abort", abortTaskHandler(d.Tasks))
	r.GET("/tasks", listTasksHandler(d.Tasks))
}

// registerControlPlaneRoutes 注册控制平面路由
func registerControlPlaneRoutes(r *gin.Engine, d Deps) {
	if d.ControlPlane == nil {
		return
	}
	r.POST("/tasks/:id/control", handleCreateControlEvent(d.ControlPlane))
	r.GET("/tasks/:id/control", handleListControlEvents(d.ControlPlane))
	r.GET("/control-events/:eventID", handleGetControlEvent(d.ControlPlane))
}

// registerFindingsRoutes 注册漏洞管理路由
func registerFindingsRoutes(r *gin.Engine, d Deps) {
	if d.Findings == nil {
		return
	}
	// 静态子路由（/hosts）须先于 :id 类路由注册避免冲突
	r.GET("/findings", listFindingsHandler(d.Findings))
	r.GET("/findings/hosts", findingHostsHandler(d.Findings))
	r.PATCH("/findings/:id/status", updateFindingStatusHandler(d.Findings))
}

// registerLLMInvocationRoutes 注册 LLM 调用记录路由
func registerLLMInvocationRoutes(r *gin.Engine, d Deps) {
	if d.Invocations == nil {
		return
	}
	r.GET("/llm/invocations/:task_id", llmInvocationsHandler(d.Invocations))
	r.GET("/llm/invocations/:task_id/invocation/:id", llmInvocationDetailHandler(d.Invocations))
	r.GET("/llm/invocations/:task_id/stat", llmInvocationStatHandler(d.Invocations))
	r.GET("/llm/invocations/:task_id/facets", llmInvocationFacetsHandler(d.Invocations))
}

// registerScanRoutes 注册扫描路由
func registerScanRoutes(r *gin.Engine, d Deps) {
	if d.Scan == nil {
		return
	}
	r.POST("/scan", scanHandler(d.Scan))
}

// registerExecutorConfigRoutes 注册 executor 配置路由
func registerExecutorConfigRoutes(r *gin.Engine, d Deps) {
	if d.ConfigStore == nil {
		return
	}
	// executor 配置 CRUD（前端配置管理页）
	r.GET("/executors", listExecutorsHandler(d.ConfigStore))
	r.GET("/executors/:id", getExecutorHandler(d.ConfigStore))
	r.POST("/executors", saveExecutorHandler(d.ConfigStore))
	r.PUT("/executors/:id", saveExecutorHandler(d.ConfigStore))
	r.PATCH("/executors/:id/complexity", updateExecutorComplexityHandler(d.ConfigStore))
}

// registerSkillRoutes 注册技能/知识库路由
func registerSkillRoutes(r *gin.Engine, d Deps) {
	if d.SkillStore == nil {
		return
	}
	// Skill 配置 CRUD（前端知识库管理页）
	r.GET("/skills", listSkillsHandler(d.SkillStore))
	r.GET("/skills/:code", getSkillHandler(d.SkillStore))
	r.POST("/skills", createSkillHandler(d.SkillStore))
	r.PUT("/skills/:code", updateSkillHandler(d.SkillStore))
	r.DELETE("/skills/:code", deleteSkillHandler(d.SkillStore))
}

// registerExplorationGraphRoutes 注册探索图路由
func registerExplorationGraphRoutes(r *gin.Engine, d Deps) {
	if d.ExplorationGraph == nil {
		return
	}
	// Phase 1: 探索图 API（e2e 测试迁移专用）
	r.GET("/api/v1/tasks/:taskId/exploration/stats", getTaskStats(d.ExplorationGraph))
	r.GET("/api/v1/tasks/:taskId/exploration/nodes", getTaskNodes(d.ExplorationGraph))
	r.GET("/api/v1/tasks/:taskId/exploration/graph", getTaskGraph(d.ExplorationGraph))
}

// registerTrafficRoutes 注册代理流量路由
func registerTrafficRoutes(r *gin.Engine, d Deps) {
	if d.Traffic == nil {
		return
	}
	// 代理捕获流量只读浏览（前端流量模块）
	// 静态段（/hosts、/content-types）须先于 /traffic/:id 注册避免冲突
	r.GET("/traffic", listTrafficHandler(d.Traffic))
	r.GET("/traffic/hosts", trafficHostsHandler(d.Traffic))
	r.GET("/traffic/content-types", trafficContentTypesHandler(d.Traffic))
	r.GET("/traffic/:id", trafficDetailHandler(d.Traffic, d.TrafficConv))
}

// registerToolCatalogRoutes 注册工具目录路由
func registerToolCatalogRoutes(r *gin.Engine, d Deps) {
	if d.ToolCatalog == nil || d.ConfigStore == nil {
		return
	}
	// 工具目录（只读检索）+ 工具↔智能体装配（读写下沉后端，单一权威）
	r.GET("/tools", listToolsHandler(d.ToolCatalog))
	r.GET("/tools/:name", getToolHandler(d.ToolCatalog, d.ConfigStore))
	r.PUT("/tools/:name/agents/:code", assignToolHandler(d.ToolCatalog, d.ConfigStore))
}

// registerModelsRoutes 注册 LLM 模型配置路由
func registerModelsRoutes(r *gin.Engine, d Deps) {
	if d.Models == nil {
		return
	}

	// Provider 部署 CRUD
	r.GET("/models/providers", listProvidersHandler(d.Models))
	r.GET("/models/providers/:key", getProviderHandler(d.Models))

	// 保存路径必须能加密明文 API Key 才开
	if d.KeyEncrypter != nil {
		r.POST("/models/providers", saveProviderHandler(d.Models, d.KeyEncrypter))
		r.PUT("/models/providers/:key", saveProviderHandler(d.Models, d.KeyEncrypter))
	}

	r.DELETE("/models/providers/:key", deleteProviderHandler(d.Models))

	// 角色路由
	r.GET("/models/routing", listRoutingHandler(d.Models))
	r.PUT("/models/routes/:role", saveRoleRouteHandler(d.Models))
	r.DELETE("/models/routes/:role", deleteRoleRouteHandler(d.Models))

	// 实连探测
	if d.ProviderTester != nil {
		r.POST("/models/providers/test", testProviderHandler(d.ProviderTester))
		r.POST("/models/providers/list-models", listProviderModelsHandler(d.ProviderTester))
	}
}

// registerSettingsRoutes 注册系统配置路由
func registerSettingsRoutes(r *gin.Engine, d Deps) {
	if d.Settings == nil {
		return
	}

	// 压缩配置
	r.GET("/settings/compaction", getCompactionSettingsHandler(d.Settings))
	r.PUT("/settings/compaction", putCompactionSettingsHandler(d.Settings))

	// 运行时配置
	r.GET("/settings/runtime", getRuntimeSettingsHandler(d.Settings))
	r.PUT("/settings/runtime", putRuntimeSettingsHandler(d.Settings))

	// 代理过滤配置
	r.GET("/settings/proxy-filter", getProxyFilterSettingsHandler(d.Settings))
	r.PUT("/settings/proxy-filter", putProxyFilterSettingsHandler(d.Settings))
}

// registerChatRoutes 注册聊天路由
func registerChatRoutes(r *gin.Engine, d Deps) {
	if d.Chat == nil {
		return
	}
	r.POST("/chat", chatHandler(d.Chat, d.StreamCookieSecret, d.CookieSecure))
}

// registerConversationRoutes 注册会话管理路由
func registerConversationRoutes(r *gin.Engine, d Deps) {
	if d.Conversations == nil {
		return
	}

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
		// 流鉴权解耦
		if len(d.StreamCookieSecret) > 0 {
			r.POST("/conversations/:id/stream-auth", streamAuthHandler(d.StreamCookieSecret, d.CookieSecure))
		}
	}
}

// registerUsageRoutes 注册使用统计路由
func registerUsageRoutes(r *gin.Engine, d Deps) {
	if d.UsageTasks == nil || d.UsageLLM == nil || d.UsageTools == nil {
		return
	}
	r.GET("/conversations/:id/usage", conversationUsageHandler(d.UsageTasks, d.UsageLLM, d.UsageTools))
}

// registerDevRoutes 注册开发辅助路由
func registerDevRoutes(r *gin.Engine, d Deps) {
	if !d.EnableDevAutofill || d.APIKey == "" {
		return
	}

	// dev-only：liusha-ui 启动时拉这个端点自动填充 API key
	// 未鉴权可访问，故仅 dev 开启、production 严禁
	key := d.APIKey
	r.GET("/dev-config.json", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(200, gin.H{"api_key": key})
	})
}
