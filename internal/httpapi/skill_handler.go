package httpapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/skillstore"
	"github.com/gin-gonic/gin"
)

// SkillAPI 是 Skill Handler 依赖的 store 接口
type SkillAPI interface {
	ListSkills(ctx context.Context, p skillstore.ListParams) ([]skillstore.Skill, error)
	CountSkills(ctx context.Context, p skillstore.ListParams) (int, error)
	SkillByCode(ctx context.Context, code string) (skillstore.Skill, error)
	CreateSkill(ctx context.Context, sk skillstore.Skill) (skillstore.Skill, error)
	UpdateSkill(ctx context.Context, id string, p skillstore.UpdateParams) (skillstore.Skill, error)
	DeleteSkill(ctx context.Context, id string) error

	// 装配到智能体（对称工具模块）：读全量 agent 算 involved、按 agent.skills
	// 白名单写回（后端单一权威，同 tool assignToolHandler）。
	ListAgents(ctx context.Context, onlyEnabled bool) ([]agent.Agent, error)
	UpdateAgent(ctx context.Context, code string, p agent.UpdateParams) (agent.Agent, error)
	AgentByCode(ctx context.Context, code string) (agent.Agent, error)
}

// listSkillsHandler 获取 Skill 列表（智能判断分页或全量）
func listSkillsHandler(api SkillAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 如果有分页参数，使用分页模式
		if c.Query("page") != "" || c.Query("size") != "" {
			listSkillsPagedHandler(api)(c)
			return
		}

		// 否则返回全量（兼容旧用法）
		skills, err := api.ListSkills(c.Request.Context(), skillstore.ListParams{})
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, skills)
	}
}

// listSkillsPagedHandler 分页获取 Skill 列表
func listSkillsPagedHandler(api SkillAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, _ := strconv.Atoi(c.Query("page"))
		size, _ := strconv.Atoi(c.Query("size"))
		q := c.Query("q")

		if page < 1 {
			page = 1
		}
		if size < 1 {
			size = 50
		}

		params := skillstore.ListParams{
			Limit:  size,
			Offset: (page - 1) * size,
			Search: q,
		}

		skills, err := api.ListSkills(c.Request.Context(), params)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}

		total, err := api.CountSkills(c.Request.Context(), params)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"skills": skills,
			"total":  total,
		})
	}
}

// getSkillHandler 获取单个 Skill
func getSkillHandler(api SkillAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		code := c.Param("code")
		skill, err := api.SkillByCode(c.Request.Context(), code)
		if err != nil {
			respondErr(c, http.StatusNotFound, err)
			return
		}
		c.JSON(http.StatusOK, skill)
	}
}

// createSkillRequest 创建请求
type createSkillRequest struct {
	Code        string `json:"code" binding:"required"`
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Category    string `json:"category" binding:"required"`
	Body        string `json:"body" binding:"required"`
}

// createSkillHandler 创建 Skill
func createSkillHandler(api SkillAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createSkillRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			respondErr(c, http.StatusBadRequest, err)
			return
		}

		skill := skillstore.Skill{
			Code:        req.Code,
			Name:        req.Name,
			Description: req.Description,
			Category:    req.Category,
			Body:        req.Body,
			Enabled:     true,
			IsBuiltin:   false,
		}

		created, err := api.CreateSkill(c.Request.Context(), skill)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}

		c.JSON(http.StatusCreated, created)
	}
}

// updateSkillRequest 更新请求
type updateSkillRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Body        *string `json:"body,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
}

// updateSkillHandler 更新 Skill
func updateSkillHandler(api SkillAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		code := c.Param("code")

		// 先查询获取 ID
		existing, err := api.SkillByCode(c.Request.Context(), code)
		if err != nil {
			respondErr(c, http.StatusNotFound, err)
			return
		}

		var req updateSkillRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			respondErr(c, http.StatusBadRequest, err)
			return
		}

		params := skillstore.UpdateParams{
			Name:        req.Name,
			Description: req.Description,
			Body:        req.Body,
			Enabled:     req.Enabled,
		}

		updated, err := api.UpdateSkill(c.Request.Context(), existing.ID, params)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}

		c.JSON(http.StatusOK, updated)
	}
}

// deleteSkillHandler 删除 Skill
func deleteSkillHandler(api SkillAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		code := c.Param("code")

		// 先查询获取 ID
		existing, err := api.SkillByCode(c.Request.Context(), code)
		if err != nil {
			respondErr(c, http.StatusNotFound, err)
			return
		}

		// 内置技能是 skills/*.md 种子的镜像（reseed 覆盖复位），删除无意义且
		// 会被复活——store 层同规则拒绝，这里给出更准确的 409 语义。
		if existing.IsBuiltin {
			c.JSON(http.StatusConflict, gin.H{"error": "内置技能不可删除（由 skills/ 目录种子管理）"})
			return
		}

		if err := api.DeleteSkill(c.Request.Context(), existing.ID); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}

		c.Status(http.StatusNoContent)
	}
}

// ── 装配到智能体（对称工具模块）──────────────────────────────────────

// skillAgentsView 是详情里的一个智能体条目：轻量标识 + 是否已装配该技能
// （involved 由后端权威计算：agent.skills 白名单含 code，或含 "*" 全量哨兵）。
type skillAgentsView struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Involved bool   `json:"involved"`
}

// agentHasSkill 判据的单一实现：白名单含 "*"（executor 种子语义 = 全量装配）或含 code。
func agentHasSkill(a agent.Agent, code string) bool {
	for _, s := range a.Skills {
		if s == code || s == "*" {
			return true
		}
	}
	return false
}

// getSkillDetailHandler 处理 GET /skills/:code/detail → {skill, agents}。
// agents 为全量智能体及各自装配态（对称 GET /tools/:name 的 involved）。
func getSkillDetailHandler(api SkillAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		code := c.Param("code")
		sk, err := api.SkillByCode(c.Request.Context(), code)
		if err != nil {
			respondErr(c, http.StatusNotFound, err)
			return
		}
		agents, err := api.ListAgents(c.Request.Context(), false)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		views := make([]skillAgentsView, 0, len(agents))
		for _, a := range agents {
			views = append(views, skillAgentsView{
				Code:     a.Code,
				Name:     a.Name,
				Involved: agentHasSkill(a, sk.Code),
			})
		}
		c.JSON(http.StatusOK, gin.H{"skill": skillJSON(sk), "agents": views})
	}
}

// skillJSON 是 skill 响应的单一序列化点（与列表 handler 共用形状）。
func skillJSON(sk skillstore.Skill) gin.H {
	return gin.H{
		"id": sk.ID, "code": sk.Code, "category": sk.Category, "name": sk.Name,
		"description": sk.Description, "body": sk.Body,
		"is_builtin": sk.IsBuiltin, "enabled": sk.Enabled,
		"created_at": sk.CreatedAt, "updated_at": sk.UpdatedAt,
	}
}

// assignSkillRequest 是 PUT /skills/:code/agents/:agentCode 的请求体：装配（true）/卸载（false）。
type assignSkillRequest struct {
	Involved bool `json:"involved"`
}

// assignSkillHandler 处理 PUT /skills/:code/agents/:agentCode → {agentCode, involved}。
// 从技能侧装/卸某智能体的该技能：校验技能存在 → 按 code 取智能体 → 改 agent.skills
// 白名单 → UpdateAgent 回存（后端单一权威）。全量哨兵 "*"：装配=幂等保留；
// 卸载=物化为「技能全集减一」。技能/智能体不存在均 404。
func assignSkillHandler(api SkillAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		code := c.Param("code")
		agentCode := c.Param("agentCode")
		var b assignSkillRequest
		if err := c.ShouldBindJSON(&b); err != nil {
			respondErr(c, http.StatusBadRequest, err)
			return
		}
		ctx := c.Request.Context()
		sk, err := api.SkillByCode(ctx, code)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "code": code})
			return
		}
		catalog, err := api.ListSkills(ctx, skillstore.ListParams{})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		a, err := api.AgentByCode(ctx, agentCode)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "agentCode": agentCode})
			return
		}

		skills := applySkillAssignment(a, sk.Code, b.Involved, catalog)
		if _, err := api.UpdateAgent(ctx, a.Code, agent.UpdateParams{Skills: &skills}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"agentCode": agentCode, "involved": b.Involved})
	}
}

// applySkillAssignment 返回该智能体装/卸某技能后的 skills 白名单（不改原切片）。
// 全量哨兵 "*"：装配=幂等保留；卸载=物化为「技能全集减一」（对称 tool 的
// applyAssignment 物化语义）。显式白名单：装=追加、卸=剔除，幂等。
func applySkillAssignment(a agent.Agent, code string, involved bool, catalog []skillstore.Skill) []string {
	if containsWildcardSkill(a.Skills) {
		if involved {
			return a.Skills
		}
		out := make([]string, 0, len(catalog))
		for _, t := range catalog {
			if t.Code != code {
				out = append(out, t.Code)
			}
		}
		return out
	}
	found := false
	out := make([]string, 0, len(a.Skills)+1)
	for _, s := range a.Skills {
		if s == code {
			found = true
			if involved {
				out = append(out, s)
			}
			continue
		}
		out = append(out, s)
	}
	if involved && !found {
		out = append(out, code)
	}
	return out
}

// containsWildcardSkill 报告白名单数组是否含全量哨兵 "*"。
func containsWildcardSkill(arr []string) bool {
	for _, s := range arr {
		if s == "*" {
			return true
		}
	}
	return false
}
