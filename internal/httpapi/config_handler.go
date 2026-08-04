// Package httpapi: scenario/playbook/hunter 配置 CRUD handler（前端配置管理页）。
//
// 写路径一律走 configstore（自动落 DB + redis 广播失效），绝不直穿底层 store——
// 否则 api 进程改配置后 runner 进程的本地 L1 不失效，会用旧配置装配（见 D7）。
// 读路径也走 configstore：单条读命中三级缓存，列表读直穿 DB（低频）。
package httpapi

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"

	cfghunter "github.com/V3teran/liusha/internal/config/hunter"
	cfgplaybook "github.com/V3teran/liusha/internal/config/playbook"
	cfgscenario "github.com/V3teran/liusha/internal/config/scenario"
)

// ConfigAPI 是三资源 CRUD handler 依赖的窄接口；*configstore.Store 自动满足。
// 读经缓存、写经失效广播的语义全在 configstore 内，handler 只做 HTTP 编解码 + 应用层校验。
type ConfigAPI interface {
	// scenario
	ListScenarios(ctx context.Context, onlyEnabled bool) ([]cfgscenario.Scenario, error)
	ScenarioByID(ctx context.Context, id string) (cfgscenario.Scenario, error)
	SaveScenario(ctx context.Context, p cfgscenario.NewParams) (cfgscenario.Scenario, error)
	DeleteScenario(ctx context.Context, id, code string) error
	// playbook
	ListPlaybooks(ctx context.Context) ([]cfgplaybook.Playbook, error)
	PlaybookByID(ctx context.Context, id string) (cfgplaybook.Playbook, error)
	PlaybookHunters(ctx context.Context, playbookID string) ([]cfghunter.Hunter, error)
	SavePlaybook(ctx context.Context, p cfgplaybook.NewParams) (cfgplaybook.Playbook, error)
	SetHunters(ctx context.Context, playbookID string, items []cfgplaybook.PlaybookHunter) error
	DeletePlaybook(ctx context.Context, id, code string) error
	// hunter
	ListHunters(ctx context.Context, onlyEnabled bool) ([]cfghunter.Hunter, error)
	HunterByID(ctx context.Context, id string) (cfghunter.Hunter, error)
	SaveHunter(ctx context.Context, p cfghunter.NewParams) (cfghunter.Hunter, error)
	DeleteHunter(ctx context.Context, id, code string) error
}

// isForeignKeyViolation 判定错误是否为 DB 外键约束冲突（pg 23503）。
// 删 playbook/hunter 若被下游引用会撞 ON DELETE RESTRICT，据此转 409。
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
		rows, err := api.ListScenarios(c.Request.Context(), false)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		out := make([]gin.H, 0, len(rows))
		for _, r := range rows {
			out = append(out, scenarioJSON(r))
		}
		c.JSON(200, gin.H{"scenarios": out})
	}
}

// getScenarioHandler 处理 GET /scenarios/:id（单条读走 ScenarioByID 三级缓存的 id 路）。
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
type scenarioBody struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Instruction string `json:"instruction"`
	Domain      string `json:"domain"`
	Engine      string `json:"engine"`
	PlaybookID  string `json:"playbook_id"`
	Enabled     bool   `json:"enabled"`
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
		if b.PlaybookID == "" {
			c.JSON(400, gin.H{"error": "playbook_id 不能为空"})
			return
		}
		sc, err := api.SaveScenario(c.Request.Context(), cfgscenario.NewParams{
			Code: b.Code, Name: b.Name, Description: b.Description, Instruction: b.Instruction,
			Domain: b.Domain, Engine: b.Engine, PlaybookID: b.PlaybookID, Enabled: b.Enabled,
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
func scenarioJSON(sc cfgscenario.Scenario) gin.H {
	return gin.H{
		"id": sc.ID, "code": sc.Code, "name": sc.Name, "description": sc.Description,
		"instruction": sc.Instruction, "domain": sc.Domain, "engine": sc.Engine,
		"playbook_id": sc.PlaybookID, "enabled": sc.Enabled,
		"created_at": sc.CreatedAt, "updated_at": sc.UpdatedAt,
	}
}

// ── playbook ──────────────────────────────────────────────────────────

// listPlaybooksHandler 处理 GET /playbooks（全量，含 enabled）。
func listPlaybooksHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := api.ListPlaybooks(c.Request.Context())
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		out := make([]gin.H, 0, len(rows))
		for _, r := range rows {
			out = append(out, playbookJSON(r, nil))
		}
		c.JSON(200, gin.H{"playbooks": out})
	}
}

// getPlaybookHandler 处理 GET /playbooks/:id（含有序 hunters 组合 [{hunter_id,position}]）。
func getPlaybookHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		pb, err := api.PlaybookByID(c.Request.Context(), id)
		if err != nil {
			c.JSON(404, gin.H{"error": err.Error(), "id": id})
			return
		}
		hunters, err := api.PlaybookHunters(c.Request.Context(), pb.ID)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"playbook": playbookJSON(pb, hunters)})
	}
}

// playbookBody 是 POST/PUT playbook 的请求体。hunters 非空时一并重设组合（SetHunters）。
type playbookBody struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	// Hunters：按序的领域猎手 id 列表；position 由数组下标决定（0-based）。
	Hunters []string `json:"hunters"`
}

// savePlaybookHandler 处理 POST /playbooks 与 PUT /playbooks/:id。
// 先 upsert playbook 主体（拿到权威 id），再按 hunters 数组重设组合关系。
func savePlaybookHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b playbookBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(400, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if b.Code == "" || b.Name == "" {
			c.JSON(400, gin.H{"error": "code 与 name 不能为空"})
			return
		}
		pb, err := api.SavePlaybook(c.Request.Context(), cfgplaybook.NewParams{
			Code: b.Code, Name: b.Name, Description: b.Description, Enabled: b.Enabled,
		})
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		items := make([]cfgplaybook.PlaybookHunter, 0, len(b.Hunters))
		for i, hid := range b.Hunters {
			items = append(items, cfgplaybook.PlaybookHunter{PlaybookID: pb.ID, HunterID: hid, Position: i})
		}
		if err := api.SetHunters(c.Request.Context(), pb.ID, items); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		hunters, err := api.PlaybookHunters(c.Request.Context(), pb.ID)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"playbook": playbookJSON(pb, hunters)})
	}
}

// deletePlaybookHandler 处理 DELETE /playbooks/:id。
// 被 scenario 引用时撞 DB ON DELETE RESTRICT（FK 23503）→ 409 中文提示。
func deletePlaybookHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		pb, err := api.PlaybookByID(c.Request.Context(), id)
		if err != nil {
			c.JSON(404, gin.H{"error": err.Error(), "id": id})
			return
		}
		if err := api.DeletePlaybook(c.Request.Context(), pb.ID, pb.Code); err != nil {
			if isForeignKeyViolation(err) {
				c.JSON(409, gin.H{"error": "该剧本仍被场景引用，请先解除引用再删除"})
				return
			}
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	}
}

// playbookJSON 是 playbook 响应的单一序列化点。hunters 为 nil 时省略组合（列表页）。
func playbookJSON(pb cfgplaybook.Playbook, hunters []cfghunter.Hunter) gin.H {
	h := gin.H{
		"id": pb.ID, "code": pb.Code, "name": pb.Name, "description": pb.Description,
		"enabled": pb.Enabled, "created_at": pb.CreatedAt, "updated_at": pb.UpdatedAt,
	}
	if hunters != nil {
		items := make([]gin.H, 0, len(hunters))
		for i, hu := range hunters {
			items = append(items, gin.H{"hunter_id": hu.ID, "position": i})
		}
		h["hunters"] = items
	}
	return h
}

// ── hunter ────────────────────────────────────────────────────────────

// listHuntersHandler 处理 GET /hunters（全量，含 enabled + orchestrator/domain 两类）。
func listHuntersHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := api.ListHunters(c.Request.Context(), false)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		out := make([]gin.H, 0, len(rows))
		for _, r := range rows {
			out = append(out, hunterJSON(r))
		}
		c.JSON(200, gin.H{"hunters": out})
	}
}

// getHunterHandler 处理 GET /hunters/:id。
func getHunterHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		h, err := api.HunterByID(c.Request.Context(), id)
		if err != nil {
			c.JSON(404, gin.H{"error": err.Error(), "id": id})
			return
		}
		c.JSON(200, gin.H{"hunter": hunterJSON(h)})
	}
}

// hunterBody 是 POST/PUT hunter 的请求体。kind 应用层白名单校验。
type hunterBody struct {
	Code          string   `json:"code"`
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Body          string   `json:"body"`
	Tools         []string `json:"tools"`
	MaxIterations int      `json:"max_iterations"`
	Enabled       bool     `json:"enabled"`
}

// saveHunterHandler 处理 POST /hunters 与 PUT /hunters/:id（均走 upsert-by-code）。
func saveHunterHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b hunterBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(400, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if b.Code == "" || b.Name == "" {
			c.JSON(400, gin.H{"error": "code 与 name 不能为空"})
			return
		}
		kind := cfghunter.Kind(b.Kind)
		if kind != cfghunter.KindOrchestrator && kind != cfghunter.KindDomain {
			c.JSON(400, gin.H{"error": "非法 kind（应为 orchestrator|domain）"})
			return
		}
		h, err := api.SaveHunter(c.Request.Context(), cfghunter.NewParams{
			Code: b.Code, Kind: kind, Name: b.Name, Description: b.Description,
			Body: b.Body, Tools: b.Tools, MaxIterations: b.MaxIterations, Enabled: b.Enabled,
		})
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"hunter": hunterJSON(h)})
	}
}

// deleteHunterHandler 处理 DELETE /hunters/:id。
// 被 playbook_hunter 引用时撞 DB ON DELETE RESTRICT（FK 23503）→ 409 中文提示。
func deleteHunterHandler(api ConfigAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		h, err := api.HunterByID(c.Request.Context(), id)
		if err != nil {
			c.JSON(404, gin.H{"error": err.Error(), "id": id})
			return
		}
		if err := api.DeleteHunter(c.Request.Context(), h.ID, h.Code); err != nil {
			if isForeignKeyViolation(err) {
				c.JSON(409, gin.H{"error": "该猎手仍被剧本引用，请先从剧本移除再删除"})
				return
			}
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	}
}

// hunterJSON 是 hunter 响应的单一序列化点。tools 保证非 nil（前端按数组渲染）。
func hunterJSON(h cfghunter.Hunter) gin.H {
	tools := h.Tools
	if tools == nil {
		tools = []string{}
	}
	return gin.H{
		"id": h.ID, "code": h.Code, "kind": string(h.Kind), "name": h.Name,
		"description": h.Description, "body": h.Body, "tools": tools,
		"max_iterations": h.MaxIterations, "enabled": h.Enabled,
		"created_at": h.CreatedAt, "updated_at": h.UpdatedAt,
	}
}
