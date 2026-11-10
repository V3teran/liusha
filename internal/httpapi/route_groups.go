package httpapi

import "github.com/gin-gonic/gin"

// uuidID 校验 UUID 主键族路由（tasks / conversations / findings）的 :id 段。
// traffic 与 llm 调用记录的 :id 是 bigint 自增键，由各自 handler 解析。
var uuidID = requireUUIDParam("id")

// registerCredentialsRoutes 注册凭证库路由（资源式；host 段经 CanonicalHost 归一）。
// 静态子路由先于参数路由注册，避免 gin 树冲突。
func registerCredentialsRoutes(r gin.IRouter, d Deps) {
	if d.Credentials == nil {
		return
	}
	r.GET("/credentials", listCredentialHostsHandler(d.Credentials))
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
	r.GET("/workspaces/tree", getWorkspaceTreeHandler(d.Workspaces))
	r.GET("/workspaces/raw", getWorkspaceRawHandler(d.Workspaces))
	r.GET("/workspaces/content", getWorkspaceFileHandler(d.Workspaces))
	r.PUT("/workspaces/content", putWorkspaceFileHandler(d.Workspaces))
	r.DELETE("/workspaces", deleteWorkspacePathHandler(d.Workspaces))
	r.GET("/findings", listFindingsHandler(d.Findings))
	r.GET("/findings/stat", findingStatHandler(d.Findings))
	r.PATCH("/findings/:id/status", uuidID, updateFindingStatusHandler(d.Findings))
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
	// 四个固定角色以 code 寻址：只读、自定义与恢复出厂，不增删。
	r.GET("/agents", listAgentsHandler(d.ConfigStore))
	r.GET("/agents/:code", getAgentHandler(d.ConfigStore))
	r.PATCH("/agents/:code", patchAgentHandler(d.ConfigStore))
	r.DELETE("/agents/:code/overrides", resetAgentHandler(d.ConfigStore))
	r.DELETE("/agents/:code/overrides/:field", resetAgentHandler(d.ConfigStore))
	r.DELETE("/agents/:code/prompt-archive", deletePromptArchiveHandler(d.ConfigStore))
}

// registerSkillRoutes 注册技能/知识库路由
func registerSkillRoutes(r gin.IRouter, d Deps) {
	if d.SkillStore == nil {
		return
	}
	// 内置技能：自定义与恢复出厂；自建技能：增删改。
	r.GET("/skills", listSkillsHandler(d.SkillStore))
	r.POST("/skills", createSkillHandler(d.SkillStore))
	r.GET("/skills/:code", getSkillHandler(d.SkillStore))
	r.PATCH("/skills/:code", patchSkillHandler(d.SkillStore))
	r.DELETE("/skills/:code", deleteSkillHandler(d.SkillStore))
	r.DELETE("/skills/:code/overrides", resetSkillHandler(d.SkillStore))
	r.DELETE("/skills/:code/overrides/:field", resetSkillHandler(d.SkillStore))
	r.PUT("/skills/:code/agents/:agentCode", assignSkillHandler(d.SkillStore))
}

// registerExplorationGraphRoutes 注册探索图路由
func registerExplorationGraphRoutes(r gin.IRouter, d Deps) {
	if d.ExplorationGraph == nil {
		return
	}
	// Phase 1: 探索图 API（e2e 测试迁移专用）
	r.GET("/conversations/:id/exploration/graph", uuidID, getConversationGraph(d.ExplorationGraph))
	r.GET("/conversations/:id/exploration/verifications", uuidID, getConversationVerifications(d.ExplorationGraph))
}

// registerTasksRoutes 注册任务模块路由（前端 /tasks 页）。
func registerTasksRoutes(r gin.IRouter, d Deps) {
	if d.Tasks != nil {
		r.GET("/tasks", listTasksHandler(d.Tasks))
		r.GET("/tasks/:id", uuidID, getTaskHandler(d.Tasks))
	}
	if d.TaskDispatch != nil {
		r.POST("/tasks", createTasksHandler(d.TaskDispatch))
	}
	if d.TaskControl != nil {
		r.POST("/tasks/:id/control", uuidID, taskControlHandler(d.TaskControl))
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
		r.POST("/models/providers", createProviderHandler(d.Models, d.KeyEncrypter))
		r.PUT("/models/providers/:code", updateProviderHandler(d.Models, d.KeyEncrypter))
	}

	r.DELETE("/models/providers/:code", deleteProviderHandler(d.Models))
	r.DELETE("/models/providers/:code/key", clearProviderKeyHandler(d.Models))

	// 档位路由
	r.GET("/models/routing", listRoutingHandler(d.Models))
	r.PUT("/models/routes/:role", saveRoleRouteHandler(d.Models))
	r.DELETE("/models/routes/:role", deleteRoleRouteHandler(d.Models))

	// 连接诊断（实测，不落库）
	if d.ProviderDiagnoser != nil {
		r.POST("/models/providers/diagnose", diagnoseProviderHandler(d.ProviderDiagnoser))
	}
}

// registerSettingsRoutes 注册系统配置路由
func registerSettingsRoutes(r gin.IRouter, d Deps) {
	if d.Settings == nil {
		return
	}
	// 路由段即分组取值：compaction / runtime / proxy_filter。
	r.GET("/settings/:group", getSettingsHandler(d.Settings))
	r.PATCH("/settings/:group", patchSettingsHandler(d.Settings))
	r.DELETE("/settings/:group/overrides", resetSettingsHandler(d.Settings))
	r.DELETE("/settings/:group/overrides/:field", resetSettingsHandler(d.Settings))
}

// registerChatRoutes 注册聊天路由
func registerChatRoutes(r gin.IRouter, d Deps) {
	if d.Chat == nil {
		return
	}
	r.POST("/chat", chatHandler(d.Chat))
}

// registerConversationRoutes 注册会话管理路由
func registerConversationRoutes(r gin.IRouter, d Deps) {
	if d.Conversations == nil {
		return
	}

	r.GET("/conversations", listConversationsHandler(d.Conversations))
	r.GET("/conversations/:id/messages", uuidID, messagesHandler(d.Conversations))

	if d.FollowUp != nil {
		r.POST("/conversations/:id/messages", uuidID, followUpHandler(d.FollowUp))
	}

	// 附件上传：静态段先于 :id 族注册避免 gin 树冲突。会话存在走 :id 形态，
	// 新会话尚未创建走 /attachments（conversation_id 留空落 pending 暂存）。
	if d.Attachments != nil && d.AttachmentContext != nil && d.AttachmentMaxBytes > 0 {
		r.POST("/attachments", uploadAttachmentHandler(d.Attachments, d.AttachmentContext, d.AttachmentMaxBytes,
			func(c *gin.Context) string { return c.PostForm("conversation_id") }))
		r.POST("/conversations/:id/attachments", uuidID, uploadAttachmentHandler(d.Attachments, d.AttachmentContext, d.AttachmentMaxBytes,
			func(c *gin.Context) string { return c.Param("id") }))
	}

	if d.Abort != nil {
		r.POST("/conversations/:id/abort", uuidID, abortConversationHandler(d.Abort))
	}

	if d.Deleter != nil {
		r.DELETE("/conversations/:id", uuidID, deleteConversationHandler(d.Deleter))
	}

	if d.Renamer != nil {
		r.PATCH("/conversations/:id", uuidID, renameConversationHandler(d.Renamer))
	}

	if d.EventStream != nil {
		r.GET("/conversations/:id/stream", uuidID, streamHandler(d.Conversations, d.EventStream))
	}
}

// registerUsageRoutes 注册使用统计路由
func registerUsageRoutes(r gin.IRouter, d Deps) {
	if d.UsageTasks == nil || d.UsageLLM == nil || d.UsageTools == nil {
		return
	}
	r.GET("/conversations/:id/usage", uuidID, conversationUsageHandler(d.UsageTasks, d.UsageLLM, d.UsageTools))
}

// registerAuthRoutes 注册登录认证路由（/auth 系列）。
// Auth 为 nil 时整组不注册；RequireAuth 同时 fail-closed，除白名单外全部 401。
// 限流器随路由组创建，进程生命周期内共享（内存态，见 login_limiter.go）。
func registerAuthRoutes(r gin.IRouter, d Deps) {
	if d.Auth == nil {
		return
	}
	limiter := newLoginLimiter()
	r.POST("/auth/login", loginHandler(d.Auth, d.AuthAudit, limiter, d.CookieSecure))
	r.POST("/auth/logout", logoutHandler(d.Auth, d.AuthAudit, d.CookieSecure))
	r.GET("/auth/me", meHandler())
	r.POST("/auth/password", changePasswordHandler(d.Auth, d.AuthAudit, d.CookieSecure))
}
