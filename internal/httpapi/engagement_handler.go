package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/V3teran/liusha/internal/engagement"
	engstore "github.com/V3teran/liusha/internal/engagement/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// createEngagementRequest 与外部 JSON 契约解耦领域模型；binding:"required" 让 gin 在反序列化阶段就挡下空字段。
type createEngagementRequest struct {
	TenantID  string         `json:"tenant_id" binding:"required"`
	Mode      string         `json:"mode"      binding:"required"`
	Scope     map[string]any `json:"scope"     binding:"required"`
	Objective string         `json:"objective"`
	Meta      map[string]any `json:"meta"`
}

// createEngagementImpl 解析请求 → 交给领域层校验 → 写入 store；任何校验失败都归一为 400 invalid_input。
func (s *Server) createEngagementImpl(c *gin.Context) {
	var in createEngagementRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		respondError(c, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	tenantID, err := uuid.Parse(in.TenantID)
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid_input", "tenant_id 必须是合法 uuid")
		return
	}
	e, err := engagement.New(engagement.CreateInput{
		TenantID:  tenantID,
		Mode:      engagement.Mode(in.Mode),
		Scope:     in.Scope,
		Objective: in.Objective,
		Meta:      in.Meta,
	})
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	if err := s.engSvc.Create(c.Request.Context(), e); err != nil {
		respondError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusCreated, marshalEngagement(e))
}

// listEngagementsImpl 按 tenant_id 列出 engagement；limit 上限 500 以限制单次响应体体量。
func (s *Server) listEngagementsImpl(c *gin.Context) {
	tenantIDStr := c.Query("tenant_id")
	if tenantIDStr == "" {
		respondError(c, http.StatusBadRequest, "invalid_input", "必须提供 tenant_id 查询参数")
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid_input", "tenant_id 必须是合法 uuid")
		return
	}
	limit := 50
	if q := c.Query("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	list, err := s.engSvc.ListByTenant(c.Request.Context(), tenantID, limit)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		out = append(out, marshalEngagement(e))
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

// getEngagementImpl 按 id 获取 engagement；ErrNotFound 映射为 404，其它错误归为 500。
func (s *Server) getEngagementImpl(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid_input", "id 必须是合法 uuid")
		return
	}
	e, err := s.engSvc.Get(c.Request.Context(), id)
	if errors.Is(err, engstore.ErrNotFound) {
		respondError(c, http.StatusNotFound, "not_found", "engagement 不存在")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, marshalEngagement(e))
}

// archiveEngagementImpl 归档需要显式传入期望 version 以触发乐观锁；版本过期返回 409 version_mismatch。
func (s *Server) archiveEngagementImpl(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid_input", "id 必须是合法 uuid")
		return
	}
	versionStr := c.Query("version")
	version, err := strconv.ParseInt(versionStr, 10, 64)
	if err != nil || version <= 0 {
		respondError(c, http.StatusBadRequest, "invalid_input", "必须提供正整数的 version 查询参数")
		return
	}
	if err := s.engSvc.Archive(c.Request.Context(), id, version); err != nil {
		if errors.Is(err, engstore.ErrVersionMismatch) {
			respondError(c, http.StatusConflict, "version_mismatch", "engagement 的 version 已过期")
			return
		}
		respondError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

// marshalEngagement 把领域对象转为对外 JSON：空 ended_at / meta 不输出，避免噪声字段。
func marshalEngagement(e engagement.Engagement) map[string]any {
	out := map[string]any{
		"id":         e.ID,
		"tenant_id":  e.TenantID,
		"mode":       string(e.Mode),
		"scope":      e.Scope,
		"status":     string(e.Status),
		"objective":  e.Objective,
		"version":    e.Version,
		"started_at": e.StartedAt,
		"updated_at": e.UpdatedAt,
	}
	if e.EndedAt != nil {
		out["ended_at"] = *e.EndedAt
	}
	if len(e.Meta) > 0 {
		out["meta"] = e.Meta
	}
	return out
}
