// Package httpapi: 技能 handler（前端「技能」页）。
//
// 技能一张表：内置技能（出厂内容是种子，没改过的随版本更新）可修改名称、描述、正文、启用，
// 可恢复出厂、删除；删除的内置技能升级时不再补回，可在「已删除的内置」里恢复。自建技能可增删改。
// 同 code 只有一个，自建占用时内置版本被遮蔽。
package httpapi

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/allowlist"
	"github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/override"
	"github.com/V3teran/liusha/internal/skillstore"
)

// SkillAPI 是技能 handler 依赖的窄接口；*configstore.Store 自动满足。
type SkillAPI interface {
	SkillDetails(ctx context.Context) ([]configstore.SkillDetail, error)
	SkillDetail(ctx context.Context, code string) (configstore.SkillDetail, error)
	CreateSkill(ctx context.Context, sk skillstore.Skill) (skillstore.Skill, error)
	PatchSkill(ctx context.Context, code string, changes override.Document) (skillstore.Skill, error)
	ResetSkill(ctx context.Context, code string, fields ...string) (skillstore.Skill, error)
	DeleteSkill(ctx context.Context, code string) error
	DeletedSkills(ctx context.Context) ([]skillstore.Skill, error)
	RestoreSkill(ctx context.Context, code string) (skillstore.Skill, error)

	// 装配到智能体：读全部 agent 算装配态，按 agent 技能名单单项装卸。
	Agents(ctx context.Context) ([]agent.Agent, error)
	ToggleAgent(ctx context.Context, code, field, elem string, on bool) (agent.Agent, error)
}

// listSkillsHandler 处理 GET /skills?q=&page=&size= → {skills, total}。
// 每项为生效技能，附已自定义字段名与是否遮蔽同名内置技能；未带 page 返回全部。
// 带 deleted=true 时返回被删除、可恢复的内置技能（内容为当前出厂）。
func listSkillsHandler(api SkillAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Query("deleted") == "true" {
			deleted, err := api.DeletedSkills(c.Request.Context())
			if err != nil {
				respondErr(c, http.StatusInternalServerError, err)
				return
			}
			items := make([]gin.H, 0, len(deleted))
			for _, sk := range deleted {
				items = append(items, skillJSON(sk))
			}
			c.JSON(http.StatusOK, gin.H{"skills": items, "total": len(items)})
			return
		}
		all, err := api.SkillDetails(c.Request.Context())
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		q := c.Query("q")
		items := make([]gin.H, 0, len(all))
		for _, d := range all {
			sk := d.Skill
			if !matchesQuery(q, sk.Code, sk.Name, sk.Description) {
				continue
			}
			item := skillJSON(sk)
			item["customized"] = customizedFields(d.Overrides)
			item["shadows_builtin"] = d.Shadows
			items = append(items, item)
		}
		page, total := pageOf(c, items, defaultConfigPageSize, maxConfigPageSize)
		c.JSON(http.StatusOK, gin.H{"skills": page, "total": total})
	}
}

// getSkillHandler 处理 GET /skills/:code → 生效技能、出厂内容（内置）、自定义状态与各 agent 的装配态。
func getSkillHandler(api SkillAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		respondSkillDetail(c, api, c.Param("code"))
	}
}

// createSkillHandler 处理 POST /skills：新建自建技能。与内置或已有技能同名 → 409。
func createSkillHandler(api SkillAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Code        string `json:"code"`
			Category    string `json:"category"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Body        string `json:"body"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		created, err := api.CreateSkill(c.Request.Context(), skillstore.Skill{
			Code: generatedCode(req.Code, req.Name), Category: req.Category, Name: req.Name,
			Description: req.Description, Body: req.Body, Enabled: true,
		})
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusCreated, skillJSON(created))
	}
}

// patchSkillHandler 处理 PATCH /skills/:code。请求体只含改动的字段（name/description/body/enabled），
// 内置与自建同一路径（内置技能改回与出厂相同即不算自定义）。
func patchSkillHandler(api SkillAPI) gin.HandlerFunc {
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
		if _, err := api.PatchSkill(c.Request.Context(), c.Param("code"), changes); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		respondSkillDetail(c, api, c.Param("code"))
	}
}

// resetSkillHandler 处理 DELETE /skills/:code/overrides[/:field]：内置技能恢复出厂内容。
func resetSkillHandler(api SkillAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var fields []string
		if f := c.Param("field"); f != "" {
			fields = append(fields, f)
		}
		if _, err := api.ResetSkill(c.Request.Context(), c.Param("code"), fields...); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		respondSkillDetail(c, api, c.Param("code"))
	}
}

// deleteSkillHandler 处理 DELETE /skills/:code：删除技能（同时清除各 agent 对它的引用）。
// 内置技能记删除标记，可恢复。
func deleteSkillHandler(api SkillAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := api.DeleteSkill(c.Request.Context(), c.Param("code")); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// restoreSkillHandler 处理 POST /skills/:code/restore：把删除的内置技能恢复成当前出厂内容。
// 不是已删除的内置技能 → 404。
func restoreSkillHandler(api SkillAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, err := api.RestoreSkill(c.Request.Context(), c.Param("code")); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		respondSkillDetail(c, api, c.Param("code"))
	}
}

// assignSkillHandler 处理 PUT /skills/:code/agents/:agentCode {involved}：把技能装配到
// 某 agent 或从其卸载（记录为该 agent 技能名单的单项勾选或取消）。
func assignSkillHandler(api SkillAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		involved, ok := bindInvolved(c)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		code, agentCode := c.Param("code"), c.Param("agentCode")
		if _, err := api.SkillDetail(ctx, code); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		if _, err := api.ToggleAgent(ctx, agentCode, agent.FieldSkills, code, involved); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"agent_code": agentCode, "involved": involved})
	}
}

func respondSkillDetail(c *gin.Context, api SkillAPI, code string) {
	ctx := c.Request.Context()
	d, err := api.SkillDetail(ctx, code)
	if err != nil {
		respondErr(c, http.StatusInternalServerError, err)
		return
	}
	agents, err := api.Agents(ctx)
	if err != nil {
		respondErr(c, http.StatusInternalServerError, err)
		return
	}
	views := make([]gin.H, 0, len(agents))
	for _, a := range agents {
		views = append(views, gin.H{"code": a.Code, "name": a.Name, "involved": allowlist.Allows(a.Skills, d.Skill.Code)})
	}
	c.JSON(http.StatusOK, gin.H{
		"skill":           skillJSON(d.Skill),
		"defaults":        d.Defaults,
		"overrides":       d.Overrides,
		"shadows_builtin": d.Shadows,
		"agents":          views,
	})
}

// skillJSON 是技能的单一序列化点。
func skillJSON(sk skillstore.Skill) gin.H {
	return gin.H{
		"code": sk.Code, "category": sk.Category, "name": sk.Name,
		"description": sk.Description, "body": sk.Body,
		"enabled": sk.Enabled, "is_builtin": sk.IsBuiltin,
	}
}
