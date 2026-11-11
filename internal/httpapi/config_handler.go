// Package httpapi: agent 配置 handler（前端「智能体」页）。
//
// 四个 agent 是固定角色，以 code 寻址，只读与自定义、不增删。生效配置 = 出厂值叠加
// 用户自定义；写入只记录用户改动的字段（见 internal/override），读写一律经 configstore
// （读走三级缓存，写后广播失效，runner 下个任务即用新配置）。
package httpapi

import (
	"context"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/constants"
	evaluatorcontract "github.com/V3teran/liusha/internal/evaluator/contract"
	executorcontract "github.com/V3teran/liusha/internal/executor/contract"
	"github.com/V3teran/liusha/internal/override"
	plannercontract "github.com/V3teran/liusha/internal/planner/contract"
)

// ConfigAPI 是 agent 配置 handler 依赖的窄接口；*configstore.Store 自动满足。
type ConfigAPI interface {
	Agents(ctx context.Context) ([]agent.Agent, error)
	AgentDetails(ctx context.Context) ([]configstore.AgentDetail, error)
	AgentDetail(ctx context.Context, code string) (configstore.AgentDetail, error)
	PatchAgent(ctx context.Context, code string, changes override.Document) (agent.Agent, error)
	ToggleAgent(ctx context.Context, code, field, elem string, on bool) (agent.Agent, error)
	ResetAgent(ctx context.Context, code string, fields ...string) (agent.Agent, error)
	DeletePromptArchive(ctx context.Context, code string) error
}

const (
	defaultConfigPageSize = 12
	maxConfigPageSize     = 100
)

// contracts 是各 agent 的输出契约原文（只读展示；monitor 的决策经工具调用发布，无契约）。
var contracts = map[string]string{
	constants.AgentCodePlanner:   plannercontract.Text,
	constants.AgentCodeExecutor:  executorcontract.Text,
	constants.AgentCodeEvaluator: evaluatorcontract.Text,
}

// listAgentsHandler 处理 GET /agents?q=&page=&size= → {agents, total}。
// 每项为生效配置，附已自定义字段名与是否有待处理的提示词归档；未带 page 返回全部。
func listAgentsHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		all, err := api.AgentDetails(c.Request.Context())
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		q := c.Query("q")
		items := make([]gin.H, 0, len(all))
		for _, d := range all {
			p := d.Agent
			if !matchesQuery(q, p.Code, p.Name, p.Description) {
				continue
			}
			items = append(items, agentListItem(d))
		}
		page, total := pageOf(c, items, defaultConfigPageSize, maxConfigPageSize)
		c.JSON(http.StatusOK, gin.H{"agents": page, "total": total})
	}
}

// getAgentHandler 处理 GET /agents/:code → 生效配置、出厂值、自定义状态、提示词归档与只读契约。
func getAgentHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		d, err := api.AgentDetail(c.Request.Context(), c.Param("code"))
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, agentDetailJSON(d))
	}
}

// patchAgentHandler 处理 PATCH /agents/:code。请求体只含改动的字段：章程、调度摘要、
// 迭代上限、档位提交新值；工具与技能白名单提交 {on, off}（可含 "*" = 全部）。
// 与出厂值相同的字段不算自定义；整体校验通过才写入。
func patchAgentHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var changes override.Document
		if err := c.ShouldBindJSON(&changes); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if len(changes) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "没有要修改的字段"})
			return
		}
		ctx := c.Request.Context()
		if _, err := api.PatchAgent(ctx, c.Param("code"), changes); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		respondAgentDetail(c, api, c.Param("code"))
	}
}

// resetAgentHandler 处理 DELETE /agents/:code/overrides[/:field]：恢复出厂值（不带字段 = 全部字段）。
func resetAgentHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var fields []string
		if f := c.Param("field"); f != "" {
			fields = append(fields, f)
		}
		if _, err := api.ResetAgent(c.Request.Context(), c.Param("code"), fields...); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		respondAgentDetail(c, api, c.Param("code"))
	}
}

// deletePromptArchiveHandler 处理 DELETE /agents/:code/prompt-archive：用户对照处理完升级前的
// 自定义提示词后清除归档。
func deletePromptArchiveHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := api.DeletePromptArchive(c.Request.Context(), c.Param("code")); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func respondAgentDetail(c *gin.Context, api ConfigAPI, code string) {
	d, err := api.AgentDetail(c.Request.Context(), code)
	if err != nil {
		respondErr(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, agentDetailJSON(d))
}

func agentListItem(d configstore.AgentDetail) gin.H {
	item := agentJSON(d.Agent)
	item["customized"] = customizedFields(d.Overrides)
	item["has_prompt_archive"] = d.PromptArchive != nil
	return item
}

func agentDetailJSON(d configstore.AgentDetail) gin.H {
	return gin.H{
		"agent":          agentJSON(d.Agent),
		"defaults":       d.Defaults,
		"overrides":      d.Overrides,
		"prompt_archive": d.PromptArchive,
		"contract":       contracts[d.Agent.Code],
	}
}

// agentJSON 是 agent 生效配置的单一序列化点（名单字段保证为数组，前端按数组渲染）。
func agentJSON(p agent.Agent) gin.H {
	return gin.H{
		"code": p.Code, "name": p.Name,
		"charter": p.Charter, "description": p.Description,
		"function_tools": nonNil(p.FunctionTools), "cli_tools": nonNil(p.CliTools), "skills": nonNil(p.Skills),
		"max_iterations": p.MaxIterations, "complexity": p.Complexity,
	}
}

// customizedFields 返回已自定义的字段名（排序，供列表徽标）。
func customizedFields(states map[string]override.State) []string {
	out := make([]string, 0, len(states))
	for name := range states {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// bindInvolved 解析名单单项装卸请求体 {"involved": true|false}。字段必填——
// 缺省若按 false 处理，会把格式错误的请求静默执行成「卸载」。
func bindInvolved(c *gin.Context) (involved, ok bool) {
	var b struct {
		Involved *bool `json:"involved"`
	}
	if err := c.ShouldBindJSON(&b); err != nil || b.Involved == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体应为 {\"involved\": true|false}"})
		return false, false
	}
	return *b.Involved, true
}
