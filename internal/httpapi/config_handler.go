// Package httpapi 是 executor 配置 CRUD handler（前端配置管理页）。
//
// 写路径一律走 configstore（自动落 DB + redis 广播失效），绝不直穿底层 store——
// 否则 api 进程改配置后 runner 进程的本地 L1 不失效，会用旧配置装配（见 D7）。
// 读路径也走 configstore：单条读命中 L1/L2 缓存，列表读直穿 DB（低频）。
package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"

	agent "github.com/V3teran/liusha/internal/agent"
)

// ConfigAPI 是 executor CRUD handler 依赖的窄接口；*configstore.Store 自动满足。
// 读经缓存、写经失效广播的语义全在 configstore 内，handler 只做 HTTP 编解码 + 应用层校验。
type ConfigAPI interface {
	// agent
	ListExecutors(ctx context.Context, onlyEnabled bool) ([]agent.Agent, error)
	ListExecutorsPaged(ctx context.Context, p agent.ListParams) ([]agent.Agent, error)
	CountExecutors(ctx context.Context, p agent.ListParams) (int, error)
	ExecutorByID(ctx context.Context, id string) (agent.Agent, error)
	AgentByCode(ctx context.Context, code string) (agent.Agent, error)
	UpdateExecutor(ctx context.Context, code string, p agent.UpdateParams) (agent.Agent, error)
	UpdateExecutorComplexity(ctx context.Context, id, complexity string) (agent.Agent, error)
}

// configPageSize 约束 executor 分页 size 上限，防超大扫描。
const (
	defaultConfigPageSize = 12
	maxConfigPageSize     = 100
)

// parsePaging 解析 page/size：page 缺省/非法 = 0（表示不分页，返回全量，供 picker/selector 复用）。
// page>=1 时分页；size 缺省 defaultConfigPageSize，clamp 到 [1,maxConfigPageSize]。
// parsePaging 解析 page/size，语义与 parsePagination 同为 **1 基**：
//   - 无 page 参数或 page < 1 → paged=false（调用方走全量分支；前端多选器依赖此契约）
//   - 有 page → 分页（offset = (page-1)*size），响应附 total
//
// 与 parsePagination 的差异是"支持全量模式"，不是页码基制。
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
		// 无 page 参数：全量（含全部 kind），保前端多选器一次拉全。
		if !paged {
			rows, err := api.ListExecutors(ctx, false)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			out := make([]gin.H, 0, len(rows))
			for _, r := range rows {
				out = append(out, executorJSON(r))
			}
			c.JSON(http.StatusOK, gin.H{"executors": out})
			return
		}
		// 分页：配置管理页搜索 + 翻页，附 total。
		params := agent.ListParams{Q: c.Query("q"), Limit: size, Offset: (page - 1) * size}
		total, err := api.CountExecutors(ctx, params)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		rows, err := api.ListExecutorsPaged(ctx, params)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		out := make([]gin.H, 0, len(rows))
		for _, r := range rows {
			out = append(out, executorJSON(r))
		}
		c.JSON(http.StatusOK, gin.H{"executors": out, "total": total})
	}
}

// getExecutorHandler 处理 GET /executors/:id。
func getExecutorHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		h, err := api.ExecutorByID(c.Request.Context(), id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "id": id})
			return
		}
		c.JSON(http.StatusOK, gin.H{"executor": executorJSON(h)})
	}
}

// agentBody 是 POST/PUT executor 的请求体。
// FunctionTools=内置函数工具；CliTools=外置 CLI 工具集（tools.yaml 名字），严格白名单，空=不装配任何外部工具。
type agentBody struct {
	Code          string   `json:"code"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	SystemPrompt  string   `json:"system_prompt"`
	FunctionTools []string `json:"function_tools"`
	CliTools      []string `json:"cli_tools"`
	Skills        []string `json:"skills"` // 可访问 skill 裸名白名单（渐进式加载声明）
	MaxIterations int      `json:"max_iterations"`
	Complexity    string   `json:"complexity"`
	Enabled       bool     `json:"enabled"`
}

// saveExecutorHandler 处理 POST /executors 与 PUT /executors/:id（均走 upsert-by-code）。
func saveExecutorHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b agentBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if b.Code == "" || b.Name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "code 与 name 不能为空"})
			return
		}
		h, err := api.UpdateExecutor(c.Request.Context(), b.Code, agent.UpdateParams{
			SystemPrompt:  &b.SystemPrompt,
			FunctionTools: &b.FunctionTools,
			CliTools:      &b.CliTools,
			Skills:        &b.Skills,
			MaxIterations: &b.MaxIterations,
			Complexity:    &b.Complexity,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"executor": executorJSON(h)})
	}
}

// updateExecutorComplexityHandler 处理 PATCH /executors/:id/complexity：只改 LLM 档位单字段（分档页移档用）。
// 不碰 agent 其余字段——避免整体 upsert 覆盖别处刚改的 body/工具。
func updateExecutorComplexityHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		var b struct {
			Complexity string `json:"complexity"`
		}
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		switch b.Complexity {
		case "simple", "medium", "complex":
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "非法 complexity（应为 simple|medium|complex）"})
			return
		}
		h, err := api.UpdateExecutorComplexity(c.Request.Context(), id, b.Complexity)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"executor": executorJSON(h)})
	}
}

// executorJSON 是 executor 响应的单一序列化点。function_tools/cli_tools 保证非 nil（前端按数组渲染）。
func executorJSON(h agent.Agent) gin.H {
	fnTools := h.FunctionTools
	if fnTools == nil {
		fnTools = []string{}
	}
	cliTools := h.CliTools
	if cliTools == nil {
		cliTools = []string{}
	}
	skills := h.Skills
	if skills == nil {
		skills = []string{}
	}
	return gin.H{
		"id": h.ID, "code": h.Code, "name": h.Name,
		"description": h.Description, "system_prompt": h.SystemPrompt,
		"function_tools": fnTools, "cli_tools": cliTools, "skills": skills,
		"max_iterations": h.MaxIterations, "complexity": h.Complexity, "enabled": h.Enabled,
		"created_at": h.CreatedAt, "updated_at": h.UpdatedAt,
	}
}
