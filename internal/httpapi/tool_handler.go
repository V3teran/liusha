// Package httpapi: 工具目录 handler（前端工具模块 + 智能体配置页选工具）。
//
// 数据源是 tool 表（启动期由代码事实源 reconcile 同步），非直读 tools.yaml/注册表。
// 列表支持 kind/q 过滤 + page/size 分页；详情附全部 agent 及各自是否已装配该工具（involved）。
// 装配态的读与写都在后端（单一权威）：读按 agent 生效名单计算，写记为名单的单项勾选或取消。

package httpapi

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	agent "github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/toolstore"
)

// ToolCatalogAPI 是工具目录 handler 依赖的窄接口；*toolstore.Store 自动满足。
type ToolCatalogAPI interface {
	List(ctx context.Context, p toolstore.ListParams) ([]toolstore.Definition, error)
	Count(ctx context.Context, p toolstore.ListParams) (int, error)
	Get(ctx context.Context, name string) (toolstore.Definition, error)
}

// defaultToolPageSize / maxToolPageSize 约束分页 size，防超大扫描。
const (
	defaultToolPageSize = 12
	maxToolPageSize     = 100
)

// listToolsHandler 处理 GET /tools?kind=&q=&page=&size= → {tools, total}。
// kind 空 = 两套体系都要；page 缺省 = 不分页返回过滤后全量（智能体选工具器依赖此契约，
// 绝不能被默认 size 截断）；page 存在才分页，从 1 起，非法回退默认页。
func listToolsHandler(api ToolCatalogAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		kind := toolstore.Kind(c.Query("kind"))
		if kind != "" && kind != toolstore.KindFunction && kind != toolstore.KindCLI {
			c.JSON(http.StatusBadRequest, gin.H{"error": "非法 kind（应为 function|cli 或留空）"})
			return
		}
		params := toolstore.ListParams{Q: c.Query("q"), Kind: kind}
		// page 显式传入才分页；缺省时 Limit 留 0 → store 返回全量（供选择器）。
		if raw := c.Query("page"); raw != "" {
			page, size := parsePagination(raw, c.Query("size"), defaultToolPageSize, maxToolPageSize)
			params.Limit = size
			params.Offset = (page - 1) * size
		}
		ctx := c.Request.Context()
		total, err := api.Count(ctx, params)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		rows, err := api.List(ctx, params)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		out := make([]gin.H, 0, len(rows))
		for _, t := range rows {
			out = append(out, toolJSON(t))
		}
		c.JSON(http.StatusOK, gin.H{"tools": out, "total": total})
	}
}

// agentToolList 取 agent 生效配置里与工具种类对应的名单。
func agentToolList(p agent.Profile, kind toolstore.Kind) []string {
	if kind == toolstore.KindCLI {
		return p.CliTools
	}
	return p.FunctionTools
}

// agentToolField 是工具种类对应的 agent 名单字段名。
func agentToolField(kind toolstore.Kind) string {
	if kind == toolstore.KindCLI {
		return agent.FieldCliTools
	}
	return agent.FieldFunctionTools
}

// getToolHandler 处理 GET /tools/:name → {tool, agents:[{code,name,involved}]}。
// involved 按 agent 生效名单判断（"*" = 全部），由后端权威计算。
func getToolHandler(tools ToolCatalogAPI, cfg ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := c.Param("name")
		ctx := c.Request.Context()
		t, err := tools.Get(ctx, name)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "name": name})
			return
		}
		agents, err := cfg.Agents(ctx)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		views := make([]gin.H, 0, len(agents))
		for _, a := range agents {
			views = append(views, gin.H{
				"code": a.Code, "name": a.Name,
				"involved": membership(agentToolList(a, t.Kind), t.Name),
			})
		}
		c.JSON(http.StatusOK, gin.H{"tool": toolJSON(t), "agents": views})
	}
}

// assignToolHandler 处理 PUT /tools/:name/agents/:code {involved}：把工具装配到某 agent
// 或从其卸载，记录为该 agent 对应名单的单项勾选或取消（「全部」状态下卸载即「全部减此项」，
// 以后新增的工具仍自动装配）。工具不存在 → 404。
func assignToolHandler(tools ToolCatalogAPI, cfg ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		involved, ok := bindInvolved(c)
		if !ok {
			return
		}
		name, code := c.Param("name"), c.Param("code")
		ctx := c.Request.Context()
		t, err := tools.Get(ctx, name)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "name": name})
			return
		}
		if _, err := cfg.ToggleAgent(ctx, code, agentToolField(t.Kind), t.Name, involved); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": code, "involved": involved})
	}
}

// toolJSON 是 tool 响应的单一序列化点（防字段漂移）。
func toolJSON(t toolstore.Definition) gin.H {
	return gin.H{
		"name": t.Name, "kind": string(t.Kind), "category": t.Category,
		"description": t.Description,
		"sort_order":  t.SortOrder, "synced_at": t.SyncedAt,
	}
}
