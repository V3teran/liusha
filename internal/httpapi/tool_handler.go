// Package httpapi: 工具目录只读 handler（前端工具模块 + 智能体配置页选工具）。
//
// 数据源是 tool 表（启动期由代码事实源 reconcile 同步），非直读 tools.yaml/注册表。
// 列表支持 kind/q 过滤 + page/size 分页；详情附全量智能体及各自是否已装配该工具（involved）。
// 装配态的读与写都在后端（单一权威）：读处理器算 involved，写端点改智能体工具集回存。

package httpapi

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	agent "github.com/V3teran/liusha/internal/agent"
	cfgtool "github.com/V3teran/liusha/internal/config/tool"
)

// ToolCatalogAPI 是工具目录 handler 依赖的窄接口；*cfgtool.Store 自动满足。
type ToolCatalogAPI interface {
	List(ctx context.Context, p cfgtool.ListParams) ([]cfgtool.Tool, error)
	Count(ctx context.Context, p cfgtool.ListParams) (int, error)
	Get(ctx context.Context, name string) (cfgtool.Tool, error)
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
		kind := cfgtool.Kind(c.Query("kind"))
		if kind != "" && kind != cfgtool.KindFunction && kind != cfgtool.KindCLI {
			c.JSON(http.StatusBadRequest, gin.H{"error": "非法 kind（应为 function|cli 或留空）"})
			return
		}
		params := cfgtool.ListParams{Q: c.Query("q"), Kind: kind}
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

// toolArrayContains 判断某智能体是否已装配该工具：按工具 kind 选 function_tools / cli_tools 数组。
// 是装配态判据的单一实现，读（involved）与写（增删）共用，避免两处逻辑漂移。
// 数组含 "*"（全量哨兵，与运行时 BuildTools/registry.Allows 同语义）= 该 kind 全部装配。
func toolArrayContains(h agent.Profile, kind cfgtool.Kind, name string) bool {
	arr := h.FunctionTools
	if kind == cfgtool.KindCLI {
		arr = h.CliTools
	}
	for _, n := range arr {
		if n == name || n == "*" {
			return true
		}
	}
	return false
}

// getToolHandler 处理 GET /tools/:name → {tool, agents:[{code,name,involved}]}。
// 列全量智能体并逐个算 involved（是否已装配该工具），装配态由后端权威计算，前端直接渲染。
func getToolHandler(tools ToolCatalogAPI, cfg ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := c.Param("name")
		ctx := c.Request.Context()
		t, err := tools.Get(ctx, name)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "name": name})
			return
		}
		agents, err := cfg.ListAgents(ctx, false)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		views := make([]gin.H, 0, len(agents))
		for _, h := range agents {
			views = append(views, gin.H{
				"code": h.Code, "name": h.Name,
				"involved": toolArrayContains(h, t.Kind, t.Name),
			})
		}
		c.JSON(http.StatusOK, gin.H{"tool": toolJSON(t), "agents": views})
	}
}

// assignBody 是 PUT /tools/:name/agents/:code 的请求体：装配（true）/卸载（false）。
type assignBody struct {
	Involved bool `json:"involved"`
}

// assignToolHandler 处理 PUT /tools/:name/agents/:code → {code, involved}。
// 从工具侧增删某智能体的该工具：校验工具存在（定 kind）→ 按 code 取智能体 → 按 kind 改
// 对应数组 → SaveExecutor 回存。幂等：目标态已达成则原样保存。工具/智能体不存在均 404。
func assignToolHandler(tools ToolCatalogAPI, cfg ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := c.Param("name")
		code := c.Param("code")
		var b assignBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		ctx := c.Request.Context()
		t, err := tools.Get(ctx, name)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "name": name})
			return
		}
		// 该 kind 的目录全集：数组为 ["*"]（全量）时卸载单个工具需展开为「全量减一」。
		catalog, err := tools.List(ctx, cfgtool.ListParams{Kind: t.Kind})
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		h, err := cfg.AgentByCode(ctx, code)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "code": code})
			return
		}
		fnTools, cliTools := applyAssignment(h, t.Kind, t.Name, b.Involved, catalog)
		if _, err := cfg.UpdateAgent(ctx, h.Code, agent.UpdateParams{
			SystemPrompt:  &h.SystemPrompt,
			FunctionTools: &fnTools,
			CliTools:      &cliTools,
		}); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": h.Code, "involved": b.Involved})
	}
}

// applyAssignment 返回该智能体装/卸某工具后的 (function_tools, cli_tools) 新数组（不改原切片）。
// 按工具 kind 只动对应数组。显式白名单：involved=true 缺则追加、false 有则剔除，幂等。
// 全量哨兵 "*"：装配任何工具都已是覆盖态（幂等 no-op，保留 "*"）；卸载则把 "*" 物化为
// 「该 kind 目录全集减一」的显式白名单——语义等于"除了它全都要"。catalog 是该 kind 全集。
func applyAssignment(h agent.Profile, kind cfgtool.Kind, name string, involved bool, catalog []cfgtool.Tool) (fn, cli []string) {
	apply := func(arr []string) []string {
		if containsWildcard(arr) {
			if involved {
				return arr
			}
			all := make([]string, 0, len(catalog))
			for _, t := range catalog {
				if t.Name != name {
					all = append(all, t.Name)
				}
			}
			return all
		}
		return toggleName(arr, name, involved)
	}
	fn, cli = h.FunctionTools, h.CliTools
	if kind == cfgtool.KindCLI {
		cli = apply(cli)
	} else {
		fn = apply(fn)
	}
	return fn, cli
}

// containsWildcard 报告白名单数组是否为全量哨兵模式（["*", ...]）。
func containsWildcard(arr []string) bool {
	for _, n := range arr {
		if n == "*" {
			return true
		}
	}
	return false
}

// toggleName 返回把 name 加入（want=true）或移除（want=false）后的新切片，保持原序、幂等、非 nil。
func toggleName(arr []string, name string, want bool) []string {
	out := make([]string, 0, len(arr)+1)
	found := false
	for _, n := range arr {
		if n == name {
			found = true
			if want {
				out = append(out, n)
			}
			continue
		}
		out = append(out, n)
	}
	if want && !found {
		out = append(out, name)
	}
	return out
}

// toolJSON 是 tool 响应的单一序列化点（防字段漂移）。
func toolJSON(t cfgtool.Tool) gin.H {
	return gin.H{
		"name": t.Name, "kind": string(t.Kind), "category": t.Category,
		"description": t.Description,
		"sort_order":  t.SortOrder, "synced_at": t.SyncedAt,
	}
}
