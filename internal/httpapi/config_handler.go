// Package httpapi: scenario/executor 配置 CRUD handler（前端配置管理页）。
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
	cfgscenario "github.com/V3teran/liusha/internal/config/scenario"
)

// ConfigAPI 是两资源（scenario/executor）CRUD handler 依赖的窄接口；*configstore.Store 自动满足。
// 读经缓存、写经失效广播的语义全在 configstore 内，handler 只做 HTTP 编解码 + 应用层校验。
type ConfigAPI interface {
	// scenario
	ListScenarios(ctx context.Context, onlyEnabled bool) ([]cfgscenario.Scenario, error)
	ListScenariosPaged(ctx context.Context, p cfgscenario.ListParams) ([]cfgscenario.Scenario, error)
	CountScenarios(ctx context.Context, p cfgscenario.ListParams) (int, error)
	ScenarioByID(ctx context.Context, id string) (cfgscenario.Scenario, error)
	SaveScenario(ctx context.Context, p cfgscenario.NewParams) (cfgscenario.Scenario, error)
	DeleteScenario(ctx context.Context, id, code string) error
	// agent
	ListExecutors(ctx context.Context, onlyEnabled bool) ([]cfgagent.Agent, error)
	ListExecutorsPaged(ctx context.Context, p cfgagent.ListParams) ([]cfgagent.Agent, error)
	CountExecutors(ctx context.Context, p cfgagent.ListParams) (int, error)
	ExecutorByID(ctx context.Context, id string) (cfgagent.Agent, error)
	ExecutorByCode(ctx context.Context, code string) (cfgagent.Agent, error)
	SaveExecutor(ctx context.Context, p cfgagent.NewParams) (cfgagent.Agent, error)
	UpdateExecutorComplexity(ctx context.Context, id, complexity string) (cfgagent.Agent, error)
	DeleteExecutor(ctx context.Context, id, code string) error
}

// configPageSize 约束 scenario/executor 分页 size 上限，防超大扫描。
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
// 删 executor 若被 scenario.solo_executor_id 引用会撞 ON DELETE RESTRICT，据此转 409。
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// ── scenario ──────────────────────────────────────────────────────────

// listScenariosHandler 处理 GET /scenarios：单一口径，全量（含 disabled）+ 全字段。
// 场景对 picker 与配置管理页统一「全部可见」——对话里停用场景仍要展示（置灰不可选），
// 配置页要能重新启用它们。可见 ≠ 可用由前端按 enabled 区分（picker 禁选、运行期不装配），
// 不再靠服务端两套响应形态分流（此端点全程 X-API-Key 鉴权，无字段泄露顾虑）。
func listScenariosHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		page, size, paged := parsePaging(c)
		// 无 page 参数：全量（含 disabled、全字段），保 ScenarioPicker 一次拉全。
		if !paged {
			rows, err := api.ListScenarios(ctx, false)
			if err != nil {
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
			out := make([]gin.H, 0, len(rows))
			for _, r := range rows {
				out = append(out, scenarioJSON(r))
			}
			c.JSON(200, gin.H{"scenarios": out})
			return
		}
		// 分页：配置管理页搜索 + 翻页，附 total。
		params := cfgscenario.ListParams{Q: c.Query("q"), Limit: size, Offset: (page - 1) * size}
		total, err := api.CountScenarios(ctx, params)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		rows, err := api.ListScenariosPaged(ctx, params)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		out := make([]gin.H, 0, len(rows))
		for _, r := range rows {
			out = append(out, scenarioJSON(r))
		}
		c.JSON(200, gin.H{"scenarios": out, "total": total})
	}
}

// getScenarioHandler 处理 GET /scenarios/:id（单条读走 ScenarioByID 多级缓存的 id 路）。
func getScenarioHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		sc, err := api.ScenarioByID(c.Request.Context(), c.Param("id"))
		if err != nil {
			c.JSON(404, gin.H{"error": err.Error(), "id": c.Param("id")})
			return
		}
		c.JSON(200, gin.H{"scenario": scenarioJSON(sc)})
	}
}

// scenarioBody 是 POST/PUT scenario 的请求体。engine 应用层白名单校验。
// SoloExecutorID：solo 引擎必填（指定唯一执行操作员），swarm 引擎必须为空（子代理池=全部 enabled 领域操作员）。
type scenarioBody struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instruction  string `json:"instruction"`
	Engine       string `json:"engine"`
	SoloExecutorID string `json:"solo_executor_id"`
	Enabled      bool   `json:"enabled"`
}

// saveScenarioHandler 处理 POST /scenarios 与 PUT /scenarios/:id（均走 upsert-by-code）。
func saveScenarioHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b scenarioBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(400, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if b.Code == "" || b.Name == "" {
			c.JSON(400, gin.H{"error": "code 与 name 不能为空"})
			return
		}
		if b.Engine != cfgscenario.EngineSolo && b.Engine != cfgscenario.EngineSwarm {
			c.JSON(400, gin.H{"error": "非法 engine（应为 solo|swarm）"})
			return
		}
		// solo 必须指定唯一操作员；swarm 不接受 solo_executor_id（子代理池由全部 enabled 领域操作员动态构成）。
		// 具体互斥再由 configstore→scenario.store 的 validateParams 做二次强校验，此处早失败给前端友好提示。
		if b.Engine == cfgscenario.EngineSolo && b.SoloExecutorID == "" {
			c.JSON(400, gin.H{"error": "solo 引擎必须指定 solo_executor_id"})
			return
		}
		if b.Engine == cfgscenario.EngineSwarm && b.SoloExecutorID != "" {
			c.JSON(400, gin.H{"error": "swarm 引擎不接受 solo_executor_id（子代理池=全部启用领域操作员）"})
			return
		}
		var soloExecutorID *string
		if b.SoloExecutorID != "" {
			soloExecutorID = &b.SoloExecutorID
		}
		sc, err := api.SaveScenario(c.Request.Context(), cfgscenario.NewParams{
			Code: b.Code, Name: b.Name, Description: b.Description, Instruction: b.Instruction,
			Engine: b.Engine, SoloExecutorID: soloExecutorID, Enabled: b.Enabled,
		})
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"scenario": scenarioJSON(sc)})
	}
}

// deleteScenarioHandler 处理 DELETE /scenarios/:id。
// task.scenario_id 是裸 text 无 FK，删场景不影响历史 task（见 D3），故不会撞 RESTRICT。
func deleteScenarioHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		sc, err := api.ScenarioByID(c.Request.Context(), id)
		if err != nil {
			c.JSON(404, gin.H{"error": err.Error(), "id": id})
			return
		}
		if err := api.DeleteScenario(c.Request.Context(), sc.ID, sc.Code); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	}
}

// scenarioJSON 是 scenario 响应的单一序列化点（防字段漂移）。
// solo_executor_id 为空指针时序列化为 null（swarm 场景 / 未配置）。
func scenarioJSON(sc cfgscenario.Scenario) gin.H {
	var soloExecutorID any
	if sc.SoloExecutorID != nil {
		soloExecutorID = *sc.SoloExecutorID
	}
	return gin.H{
		"id": sc.ID, "code": sc.Code, "name": sc.Name, "description": sc.Description,
		"instruction": sc.Instruction, "engine": sc.Engine,
		"solo_executor_id": soloExecutorID, "enabled": sc.Enabled,
		"created_at": sc.CreatedAt, "updated_at": sc.UpdatedAt,
	}
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
	Body          string   `json:"body"`
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
			c.JSON(400, gin.H{"error": "非法 kind（应为 planner|domain）"})
			return
		}
		h, err := api.SaveExecutor(c.Request.Context(), cfgagent.NewParams{
			Code: b.Code, Kind: kind, Name: b.Name, Description: b.Description,
			Body: b.Body, FunctionTools: b.FunctionTools, CliTools: b.CliTools,
			MaxIterations: b.MaxIterations, Complexity: b.Complexity, Enabled: b.Enabled,
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
		"description": h.Description, "body": h.Body, "function_tools": fnTools, "cli_tools": cliTools,
		"max_iterations": h.MaxIterations, "complexity": h.Complexity, "enabled": h.Enabled,
		"created_at": h.CreatedAt, "updated_at": h.UpdatedAt,
	}
}
