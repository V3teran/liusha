// Package httpapi: executor 配置 CRUD handler（前端配置管理页）。
//
// 写路径一律走 configstore（自动落 DB + redis 广播失效），绝不直穿底层 store——
// 否则 api 进程改配置后 runner 进程的本地 L1 不失效，会用旧配置装配（见 D7）。
// 读路径也走 configstore：单条读命中 L1/L2 缓存，列表读直穿 DB（低频）。
package httpapi

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"

	cfgagent "github.com/V3teran/liusha/internal/config/agent"
)

// ConfigAPI 是 executor CRUD handler 依赖的窄接口；*configstore.Store 自动满足。
// 读经缓存、写经失效广播的语义全在 configstore 内，handler 只做 HTTP 编解码 + 应用层校验。
type ConfigAPI interface {
	// agent
	ListExecutors(ctx context.Context, onlyEnabled bool) ([]cfgagent.Agent, error)
	ListExecutorsPaged(ctx context.Context, p cfgagent.ListParams) ([]cfgagent.Agent, error)
	CountExecutors(ctx context.Context, p cfgagent.ListParams) (int, error)
	ExecutorByID(ctx context.Context, id string) (cfgagent.Agent, error)
	ExecutorByCode(ctx context.Context, code string) (cfgagent.Agent, error)
	UpdateExecutor(ctx context.Context, code string, p cfgagent.UpdateParams) (cfgagent.Agent, error)
	UpdateExecutorComplexity(ctx context.Context, id, complexity string) (cfgagent.Agent, error)
	DeleteExecutor(ctx context.Context, id, code string) error
}

// configPageSize 约束 executor 分页 size 上限，防超大扫描。
const (
	defaultConfigPageSize = 12
	maxConfigPageSize     = 100
)

// parsePaging 解析 page/size：page 缺省/非法 = 0（表示不分页，返回全量，供 picker/selector 复用）。
// page>=1 时分页；size 缺省 defaultConfigPageSize，clamp 到 [1,maxConfigPageSize]。
// 返回 (page, size, paged)：paged=false 时调用方走全量分支。
func parsePaging(c *gin.Context) (page, size int, paged bool) {
	pageStr := c.Query("page")
	if pageStr == "" {
		return 0, 0, false
	}
	page = atoiOr(pageStr, 0)
	if page < 1 {
		return 0, 0, false
	}
	size = atoiOr(c.Query("size"), defaultConfigPageSize)
	if size < 1 {
		size = defaultConfigPageSize
	}
	if size > maxConfigPageSize {
		size = maxConfigPageSize
	}
	return page, size, true
}

// isForeignKeyViolation 判定错误是否为 DB 外键约束冲突（pg 23503）。
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// ── agent ────────────────────────────────────────────────────────────

// listExecutorsHandler 处理 GET /executors（全量，含 enabled + planner/domain 两类）。
func listExecutorsHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		page, size, paged := parsePaging(c)
		// 无 page 参数：全量（含 planner/domain 两类），保 solo_agent 选择器一次拉全。
		if !paged {
			rows, err := api.ListExecutors(ctx, false)
			if err != nil {
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
			out := make([]gin.H, 0, len(rows))
			for _, r := range rows {
				out = append(out, executorJSON(r))
			}
			c.JSON(200, gin.H{"executors": out})
			return
		}
		// 分页：配置管理页搜索 + 翻页，附 total。
		params := cfgagent.ListParams{Q: c.Query("q"), Limit: size, Offset: (page - 1) * size}
		total, err := api.CountExecutors(ctx, params)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		rows, err := api.ListExecutorsPaged(ctx, params)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		out := make([]gin.H, 0, len(rows))
		for _, r := range rows {
			out = append(out, executorJSON(r))
		}
		c.JSON(200, gin.H{"executors": out, "total": total})
	}
}

// getExecutorHandler 处理 GET /executors/:id。
func getExecutorHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		h, err := api.ExecutorByID(c.Request.Context(), id)
		if err != nil {
			c.JSON(404, gin.H{"error": err.Error(), "id": id})
			return
		}
		c.JSON(200, gin.H{"executor": executorJSON(h)})
	}
}

// agentBody 是 POST/PUT executor 的请求体。kind 应用层白名单校验。
// FunctionTools=内置函数工具；CliTools=外置 CLI 工具集（tools.yaml 名字），严格白名单，空=不装配任何外部工具。
type agentBody struct {
	Code          string   `json:"code"`
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	SystemPrompt  string   `json:"system_prompt"` // 改为SystemPrompt
	FunctionTools []string `json:"function_tools"`
	CliTools      []string `json:"cli_tools"`
	MaxIterations int      `json:"max_iterations"`
	Complexity    string   `json:"complexity"`
	Enabled       bool     `json:"enabled"`
}

// saveExecutorHandler 处理 POST /executors 与 PUT /executors/:id（均走 upsert-by-code）。
func saveExecutorHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b agentBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(400, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if b.Code == "" || b.Name == "" {
			c.JSON(400, gin.H{"error": "code 与 name 不能为空"})
			return
		}
		kind := cfgagent.Kind(b.Kind)
		if kind != cfgagent.KindPlanner && kind != cfgagent.KindExecutor {
			c.JSON(400, gin.H{"error": "非法 kind（应为 planner|executor）"})
			return
		}
		h, err := api.UpdateExecutor(c.Request.Context(), b.Code, cfgagent.UpdateParams{
			SystemPrompt:  &b.SystemPrompt,
			FunctionTools: &b.FunctionTools,
			CliTools:      &b.CliTools,
			MaxIterations: &b.MaxIterations,
			Complexity:    &b.Complexity,
		})
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"executor": executorJSON(h)})
	}
}

// updateExecutorTierHandler 处理 PATCH /executors/:id/tier：只改能力档单字段（分档页移档用）。
// 不碰 agent 其余字段——避免整体 upsert 覆盖别处刚改的 body/工具。
func updateExecutorTierHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		var b struct {
			Complexity string `json:"complexity"`
		}
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(400, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		switch b.Complexity {
		case "simple", "medium", "complex":
		default:
			c.JSON(400, gin.H{"error": "非法 complexity（应为 simple|medium|complex）"})
			return
		}
		h, err := api.UpdateExecutorComplexity(c.Request.Context(), id, b.Complexity)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"executor": executorJSON(h)})
	}
}

// deleteExecutorHandler 处理 DELETE /executors/:id。
// 被 scenario.solo_executor_id 引用时撞 DB ON DELETE RESTRICT（FK 23503）→ 409 中文提示。
func deleteExecutorHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		h, err := api.ExecutorByID(c.Request.Context(), id)
		if err != nil {
			c.JSON(404, gin.H{"error": err.Error(), "id": id})
			return
		}
		if err := api.DeleteExecutor(c.Request.Context(), h.ID, h.Code); err != nil {
			if isForeignKeyViolation(err) {
				c.JSON(409, gin.H{"error": "该操作员仍被场景引用（solo 场景执行操作员），请先解除引用再删除"})
				return
			}
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	}
}

// executorJSON 是 executor 响应的单一序列化点。function_tools/cli_tools 保证非 nil（前端按数组渲染）。
func executorJSON(h cfgagent.Agent) gin.H {
	fnTools := h.FunctionTools
	if fnTools == nil {
		fnTools = []string{}
	}
	cliTools := h.CliTools
	if cliTools == nil {
		cliTools = []string{}
	}
	return gin.H{
		"id": h.ID, "code": h.Code, "kind": string(h.Kind), "name": h.Name,
		"description": h.Description, "system_prompt": h.SystemPrompt, "function_tools": fnTools, "cli_tools": cliTools,
		"max_iterations": h.MaxIterations, "complexity": h.Complexity, "enabled": h.Enabled,
		"created_at": h.CreatedAt, "updated_at": h.UpdatedAt,
	}
}
