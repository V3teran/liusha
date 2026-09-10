// Package httpapi: 通用 Agent 配置 CRUD handler（支持 Planner/Executor/Evaluator）
//
// 新增的通用 API，支持所有三个 Agent 类型。
// 路由：GET /api/agents, GET /api/agents/:code, PUT /api/agents/:code, POST /api/agents/:code/reset
package httpapi

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"

	cfgagent "github.com/V3teran/liusha/internal/config/agent"
)

// AgentAPI 是通用 Agent CRUD 的接口（比 ConfigAPI 更窄，只要核心方法）
type AgentAPI interface {
	GetAgentByCode(ctx context.Context, code string) (cfgagent.Agent, error)
	UpdateAgent(ctx context.Context, id string, p cfgagent.UpdateParams) (cfgagent.Agent, error)
}

// RegisterAgentRoutes 注册通用 Agent API 路由
// 路由前缀：/api/agents
func RegisterAgentRoutes(r *gin.RouterGroup, cache AgentAPI, store *cfgagent.Store) {
	agents := r.Group("/agents")
	{
		// GET /api/agents - 列出所有 Agent（Planner + Executor + Evaluator）
		agents.GET("", listAllAgentsHandler(cache))

		// GET /api/agents/:code - 获取单个 Agent（code: planner/executor/evaluator）
		agents.GET("/:code", getAgentByCodeHandler(cache))

		// PUT /api/agents/:code - 更新 Agent 配置
		agents.PUT("/:code", updateAgentHandler(cache))

		// POST /api/agents/:code/reset - 重置为默认配置（从种子文件）
		agents.POST("/:code/reset", resetAgentHandler(cache, store))
	}
}

// listAllAgentsHandler 列出所有 Agent（Planner + Executor + Evaluator）
func listAllAgentsHandler(api AgentAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		agentCodes := []string{"planner", "executor", "evaluator"}

		result := make([]gin.H, 0, len(agentCodes))
		for _, code := range agentCodes {
			agent, err := api.GetAgentByCode(ctx, code)
			if err != nil {
				// 跳过不存在的（如 evaluator 还未运行 migration）
				continue
			}
			result = append(result, agentToJSON(agent))
		}

		c.JSON(200, gin.H{"agents": result})
	}
}

// getAgentByCodeHandler 获取单个 Agent 配置
func getAgentByCodeHandler(api AgentAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		code := c.Param("code")
		ctx := c.Request.Context()

		agent, err := api.GetAgentByCode(ctx, code)
		if err != nil {
			c.JSON(404, gin.H{"error": fmt.Sprintf("Agent %q not found", code)})
			return
		}

		c.JSON(200, gin.H{"agent": agentToJSON(agent)})
	}
}

// updateAgentHandler 更新 Agent 配置
func updateAgentHandler(api AgentAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		code := c.Param("code")
		ctx := c.Request.Context()

		// 先获取现有 Agent（验证存在 + 获取 ID）
		existing, err := api.GetAgentByCode(ctx, code)
		if err != nil {
			c.JSON(404, gin.H{"error": fmt.Sprintf("Agent %q not found", code)})
			return
		}

		// 解析更新参数
		var body struct {
			SystemPrompt  *string   `json:"system_prompt"`
			FunctionTools *[]string `json:"function_tools"`
			CliTools      *[]string `json:"cli_tools"`
			MaxIterations *int      `json:"max_iterations"`
			Complexity    *string   `json:"complexity"`
		}

		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(400, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}

		// 构造更新参数
		updates := cfgagent.UpdateParams{
			SystemPrompt:  body.SystemPrompt,
			FunctionTools: body.FunctionTools,
			CliTools:      body.CliTools,
			MaxIterations: body.MaxIterations,
			Complexity:    body.Complexity,
		}

		// 执行更新
		updated, err := api.UpdateAgent(ctx, existing.ID, updates)
		if err != nil {
			c.JSON(500, gin.H{"error": "更新失败: " + err.Error()})
			return
		}

		c.JSON(200, gin.H{"agent": agentToJSON(updated)})
	}
}

// resetAgentHandler 重置 Agent 为默认配置（从种子文件重新加载）
func resetAgentHandler(api AgentAPI, store *cfgagent.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		code := c.Param("code")

		// 验证 Agent 存在
		_, err := api.GetAgentByCode(c.Request.Context(), code)
		if err != nil {
			c.JSON(404, gin.H{"error": fmt.Sprintf("Agent %q not found", code)})
			return
		}

		// 从种子文件重新加载
		// TODO: 这里需要实现一个 seed.LoadAgentSeed(code) 函数
		// 目前先返回提示

		// 临时方案：返回成功但说明需要重启
		c.JSON(200, gin.H{
			"ok":      true,
			"message": fmt.Sprintf("Agent %q 将在下次重启时从种子文件重新加载", code),
			"note":    "正式实现需要添加 seed.LoadAgentSeed() 函数",
		})

		// TODO: 正式实现
		// seedData, err := seed.LoadAgentSeed(code)
		// if err != nil {
		//     c.JSON(500, gin.H{"error": "加载种子失败: " + err.Error()})
		//     return
		// }
		//
		// updates := cfgagent.UpdateParams{
		//     SystemPrompt:  &seedData.SystemPrompt,
		//     FunctionTools: &seedData.FunctionTools,
		//     CliTools:      &seedData.CliTools,
		//     MaxIterations: &seedData.MaxIterations,
		//     Complexity:    &seedData.Complexity,
		// }
		//
		// updated, err := api.UpdateAgent(ctx, existing.ID, updates)
		// if err != nil {
		//     c.JSON(500, gin.H{"error": "重置失败: " + err.Error()})
		//     return
		// }
		//
		// c.JSON(200, gin.H{"agent": agentToJSON(updated), "message": "已重置为默认配置"})
	}
}

// agentToJSON 将 Agent 转换为 JSON 格式
func agentToJSON(a cfgagent.Agent) gin.H {
	return gin.H{
		"id":              a.ID,
		"code":            a.Code,
		"kind":            string(a.Kind),
		"name":            a.Name,
		"description":     a.Description,
		"system_prompt":   a.SystemPrompt,
		"function_tools":  a.FunctionTools,
		"cli_tools":       a.CliTools,
		"max_iterations":  a.MaxIterations,
		"complexity":      a.Complexity,
		"enabled":         a.Enabled,
		"created_at":      a.CreatedAt,
		"updated_at":      a.UpdatedAt,
	}
}
