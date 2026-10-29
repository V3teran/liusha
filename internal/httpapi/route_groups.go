package httpapi

import "github.com/gin-gonic/gin"

// registerCredentialsRoutes 注册凭证库路由（资源式；host 段经 CanonicalHost 归一）。
// 静态子路由先于参数路由注册，避免 gin 树冲突。
func registerCredentialsRoutes(r gin.IRouter, d Deps) {
	if d.Credentials == nil {
		return
	}
	r.GET("/credentials", listCredentialHostsHandler(d.Credentials, d.CredentialAudit))
	r.GET("/credentials/:host", getCredentialDetailHandler(d.Credentials, d.TaskStatus))
	r.PUT("/credentials/:host/usernames", upsertCredentialIdentityHandler(d.Credentials, d.CredentialAudit))
	r.DELETE("/credentials/:host/usernames/:username", deleteCredentialIdentityHandler(d.Credentials, d.CredentialAudit))
	r.DELETE("/credentials/:host/sessions", purgeCredentialSessionsHandler(d.Credentials, d.CredentialAudit))
	r.DELETE("/credentials/:host", deleteCredentialHostHandler(d.Credentials, d.CredentialAudit))
}

// registerFindingsRoutes 注册漏洞管理路由
func registerFindingsRoutes(r gin.IRouter, d Deps) {
	if d.Findings == nil {
		return
	}
	// 静态子路由须先于 :id 类路由注册避免冲突
	r.GET("/findings", listFindingsHandler(d.Findings))
	r.PATCH("/findings/:id/status", updateFindingStatusHandler(d.Findings))
}

// registerLLMInvocationRoutes 注册 LLM 调用记录路由
func registerLLMInvocationRoutes(r gin.IRouter, d Deps) {
	if d.Invocations == nil {
		return
	}
	// 注意：更具体的路由必须在通配路由之前注册
	r.GET("/llm/invocations/search", llmInvocationsSearchHandler(d.Invocations))
	r.GET("/llm/invocations/search/stat", llmInvocationsSearchStatHandler(d.Invocations))
	r.GET("/llm/invocations/detail/:id", llmInvocationDetailByIDHandler(d.Invocations))
}

// registerAgentConfigRoutes 注册 agent 配置路由
func registerAgentConfigRoutes(r gin.IRouter, d Deps) {
	if d.ConfigStore == nil {
		return
	}
	// agent 配置读取/编辑（前端配置管理页）。角色为固定种子：
	// 无 POST（不可新建）、无 DELETE（不可删），只有部分更新。
	r.GET("/agents", listAgentsHandler(d.ConfigStore))
	r.GET("/agents/:id", getAgentHandler(d.ConfigStore))
	r.PUT("/agents/:id", saveAgentHandler(d.ConfigStore))
	r.PATCH("/agents/:id/complexity", updateAgentComplexityHandler(d.ConfigStore))
}

// registerSkillRoutes 注册技能/知识库路由
func registerSkillRoutes(r gin.IRouter, d Deps) {
	if d.SkillStore == nil {
		return
	}
	// Skill 配置 CRUD（前端知识库管理页）
	r.GET("/skills", listSkillsHandler(d.SkillStore))
	r.GET("/skills/:code", getSkillHandler(d.SkillStore))
	r.GET("/skills/:code/detail", getSkillDetailHandler(d.SkillStore))
	r.PUT("/skills/:code/agents/:agentCode", assignSkillHandler(d.SkillStore))
	r.POST("/skills", createSkillHandler(d.SkillStore))
	r.PUT("/skills/:code", updateSkillHandler(d.SkillStore))
	r.DELETE("/skills/:code", deleteSkillHandler(d.SkillStore))
}

// registerExplorationGraphRoutes 注册探索图路由
func registerExplorationGraphRoutes(r gin.IRouter, d Deps) {
	if d.ExplorationGraph == nil {
		return
	}
	// Phase 1: 探索图 API（e2e 测试迁移专用）
	r.GET("/conversations/:id/exploration/graph", getConversationGraph(d.ExplorationGraph))
}

// registerTasksRoutes 注册任务模块路由（前端 /tasks 页）。
func registerTasksRoutes(r gin.IRouter, d Deps) {
	if d.Tasks != nil {
		r.GET("/tasks", listTasksHandler(d.Tasks))
		r.GET("/tasks/:id", getTaskHandler(d.Tasks))
	}
	if d.TaskDispatch != nil {
		r.POST("/tasks", createTasksHandler(d.TaskDispatch))
	}
}

// registerLogsRoutes 注册日志查看路由（前端 /logs 页）。
func registerLogsRoutes(r gin.IRouter, d Deps) {
	if d.Logs == nil {
		return
	}
	// 静态子路由先注册（全站惯例）
	r.GET("/logs/processes", logProcessesHandler(d.Logs))
	r.GET("/logs", readLogsHandler(d.Logs))
}

// registerTrafficRoutes 注册代理流量路由
func registerTrafficRoutes(r gin.IRouter, d Deps) {
	if d.Traffic == nil {
		return
	}
	// 代理捕获流量只读浏览（前端流量模块）
	// 静态段（/hosts、/content-types）须先于 /traffic/:id 注册避免冲突
	r.GET("/traffic", listTrafficHandler(d.Traffic))
	r.GET("/traffic/content-types", trafficContentTypesHandler(d.Traffic))
	r.GET("/traffic/:id", trafficDetailHandler(d.Traffic, d.TrafficConv))
}

// registerToolCatalogRoutes 注册工具目录路由
func registerToolCatalogRoutes(r gin.IRouter, d Deps) {
	if d.ToolCatalog == nil || d.ConfigStore == nil {
		return
	}
	// 工具目录（只读检索）+ 工具↔智能体装配（读写下沉后端，单一权威）
	r.GET("/tools", listToolsHandler(d.ToolCatalog))
	r.GET("/tools/:name", getToolHandler(d.ToolCatalog, d.ConfigStore))
	r.PUT("/tools/:name/agents/:code", assignToolHandler(d.ToolCatalog, d.ConfigStore))
}

// registerModelsRoutes 注册 LLM 模型配置路由
func registerModelsRoutes(r gin.IRouter, d Deps) {
	if d.Models == nil {
		return
	}

	// Provider 部署 CRUD
	r.GET("/models/providers", listProvidersHandler(d.Models))
	r.GET("/models/providers/:code", getProviderHandler(d.Models))

	// 保存路径必须能加密明文 API Key 才开
	if d.KeyEncrypter != nil {
		r.POST("/models/providers", saveProviderHandler(d.Models, d.KeyEncrypter))
		r.PUT("/models/providers/:code", saveProviderHandler(d.Models, d.KeyEncrypter))
	}

	r.DELETE("/models/providers/:code", deleteProviderHandler(d.Models))
	r.DELETE("/models/providers/:code/key", clearProviderKeyHandler(d.Models))

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
func registerSettingsRoutes(r gin.IRouter, d Deps) {
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
func registerChatRoutes(r gin.IRouter, d Deps) {
	if d.Chat == nil {
		return
	}
	r.POST("/chat", chatHandler(d.Chat, d.StreamCookieSecret, d.CookieSecure))
}

// registerConversationRoutes 注册会话管理路由
func registerConversationRoutes(r gin.IRouter, d Deps) {
	if d.Conversations == nil {
		return
	}

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
		// 流鉴权解耦
		if len(d.StreamCookieSecret) > 0 {
			r.POST("/conversations/:id/stream-auth", streamAuthHandler(d.StreamCookieSecret, d.CookieSecure))
		}
	}
}

// registerUsageRoutes 注册使用统计路由
func registerUsageRoutes(r gin.IRouter, d Deps) {
	if d.UsageTasks == nil || d.UsageLLM == nil || d.UsageTools == nil {
		return
	}
	r.GET("/conversations/:id/usage", conversationUsageHandler(d.UsageTasks, d.UsageLLM, d.UsageTools))
}

// registerDevRoutes 注册开发辅助路由
func registerDevRoutes(r gin.IRouter, d Deps) {
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
