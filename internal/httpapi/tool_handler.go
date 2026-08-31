// Package httpapi: 工具目录只读 handler（前端工具模块 + 智能体配置页选工具）。
//
// 数据源是 tool 表（启动期由代码事实源 reconcile 同步），非直读 tools.yaml/注册表。
// 列表支持 kind/q 过滤 + page/size 分页；详情附全量智能体及各自是否已装配该工具（involved）。
// 装配态的读与写都在后端（单一权威）：读处理器算 involved，写端点改智能体工具集回存。
package httpapi

import (
	"context"
	"strconv"

	"github.com/gin-gonic/gin"

	cfgagent "github.com/V3teran/liusha/internal/config/agent"
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
			c.JSON(400, gin.H{"error": "非法 kind（应为 function|cli 或留空）"})
			return
		}
		params := cfgtool.ListParams{Q: c.Query("q"), Kind: kind}
		// page 显式传入才分页；缺省时 Limit 留 0 → store 返回全量（供选择器）。
		if raw := c.Query("page"); raw != "" {
			page := atoiOr(raw, 1)
			if page < 1 {
				page = 1
			}
			size := atoiOr(c.Query("size"), defaultToolPageSize)
			if size < 1 {
				size = defaultToolPageSize
			}
			if size > maxToolPageSize {
				size = maxToolPageSize
			}
			params.Limit = size
			params.Offset = (page - 1) * size
		}
		ctx := c.Request.Context()
		total, err := api.Count(ctx, params)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		rows, err := api.List(ctx, params)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		out := make([]gin.H, 0, len(rows))
		for _, t := range rows {
			out = append(out, toolJSON(t))
		}
		c.JSON(200, gin.H{"tools": out, "total": total})
	}
}

// toolArrayContains 判断某智能体是否已装配该工具：按工具 kind 选 function_tools / cli_tools 数组。
// 是装配态判据的单一实现，读（involved）与写（增删）共用，避免两处逻辑漂移。
func toolArrayContains(h cfgagent.Agent, kind cfgtool.Kind, name string) bool {
	arr := h.FunctionTools
	if kind == cfgtool.KindCLI {
		arr = h.CliTools
	}
	for _, n := range arr {
		if n == name {
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
			c.JSON(404, gin.H{"error": err.Error(), "name": name})
			return
		}
		executors, err := cfg.ListExecutors(ctx, false)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		agents := make([]gin.H, 0, len(executors))
		for _, h := range executors {
			agents = append(agents, gin.H{
				"code": h.Code, "name": h.Name,
				"involved": toolArrayContains(h, t.Kind, t.Name),
			})
		}
		c.JSON(200, gin.H{"tool": toolJSON(t), "agents": agents})
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
			c.JSON(400, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		ctx := c.Request.Context()
		t, err := tools.Get(ctx, name)
		if err != nil {
			c.JSON(404, gin.H{"error": err.Error(), "name": name})
			return
		}
		h, err := cfg.ExecutorByCode(ctx, code)
		if err != nil {
			c.JSON(404, gin.H{"error": err.Error(), "code": code})
			return
		}
		fnTools, cliTools := applyAssignment(h, t.Kind, t.Name, b.Involved)
		if _, err := cfg.UpdateExecutor(ctx, h.Code, cfgagent.UpdateParams{
			SystemPrompt:  &h.SystemPrompt,
			FunctionTools: &fnTools,
			CliTools:      &cliTools,
		}); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"code": h.Code, "involved": b.Involved})
	}
}

// applyAssignment 返回该智能体装/卸某工具后的 (function_tools, cli_tools) 新数组（不改原切片）。
// 按工具 kind 只动对应数组；involved=true 缺则追加，false 有则剔除，幂等。
func applyAssignment(h cfgagent.Agent, kind cfgtool.Kind, name string, involved bool) (fn, cli []string) {
	fn, cli = h.FunctionTools, h.CliTools
	if kind == cfgtool.KindCLI {
		cli = toggleName(cli, name, involved)
	} else {
		fn = toggleName(fn, name, involved)
	}
	return fn, cli
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

// atoiOr 解析十进制整数，失败回退 def。
func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
