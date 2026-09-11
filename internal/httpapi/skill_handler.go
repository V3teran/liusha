package httpapi

import (
	"context"
	"net/http"

	"github.com/V3teran/liusha/internal/skillstore"
	"github.com/gin-gonic/gin"
)

// SkillAPI 是 Skill Handler 依赖的 store 接口
type SkillAPI interface {
	ListSkills(ctx context.Context, p skillstore.ListParams) ([]skillstore.Skill, error)
	SkillByCode(ctx context.Context, code string) (skillstore.Skill, error)
	CreateSkill(ctx context.Context, sk skillstore.Skill) (skillstore.Skill, error)
	UpdateSkill(ctx context.Context, id string, p skillstore.UpdateParams) (skillstore.Skill, error)
	DeleteSkill(ctx context.Context, id string) error
}

// listSkillsHandler 获取 Skill 列表
func listSkillsHandler(api SkillAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		skills, err := api.ListSkills(c.Request.Context(), skillstore.ListParams{})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, skills)
	}
}

// getSkillHandler 获取单个 Skill
func getSkillHandler(api SkillAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		code := c.Param("code")
		skill, err := api.SkillByCode(c.Request.Context(), code)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}

		var req updateSkillRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}

		if err := api.DeleteSkill(c.Request.Context(), existing.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.Status(http.StatusNoContent)
	}
}
