package httpapi

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/credential"
)

// CredentialsAPI 是 handlers 对凭证存储的窄接口，
// 仅暴露 HTTP 层真正用到的 3 个方法，便于测试 mock。
type CredentialsAPI interface {
	BatchSave(ctx context.Context, byHost map[string][]credential.Identity, ttlSeconds int) error
	GetIdentitiesByHost(ctx context.Context, host string) ([]credential.Identity, error)
	Delete(ctx context.Context, host string) error
}

// EngagementsAPI 是 handlers 对 engagement store 的窄接口，仅需 Abort。
type EngagementsAPI interface {
	Abort(ctx context.Context, id string) error
}

// BatchSaveRequest 是 POST /credential/batch 请求体。
// TTLSeconds=0 表示永不过期，由底层 Provider 决定语义。
type BatchSaveRequest struct {
	TTLSeconds  int                              `json:"ttl_seconds"`
	Credentials map[string][]credential.Identity `json:"credentials"`
}

// batchSaveHandler 把 host -> identities map 一次性写入凭证存储。
func batchSaveHandler(api CredentialsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req BatchSaveRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		if err := api.BatchSave(c.Request.Context(), req.Credentials, req.TTLSeconds); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	}
}

// listCredentialHandler 返回指定 host 的全部 identity（含 anonymous 注入由底层负责）。
func listCredentialHandler(api CredentialsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		host := c.Query("host")
		if host == "" {
			c.JSON(400, gin.H{"error": "host required"})
			return
		}
		ids, err := api.GetIdentitiesByHost(c.Request.Context(), host)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"identities": ids})
	}
}

// deleteCredentialHandler 删除指定 host 下的所有持久化身份。
func deleteCredentialHandler(api CredentialsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		host := c.Query("host")
		if host == "" {
			c.JSON(400, gin.H{"error": "host required"})
			return
		}
		if err := api.Delete(c.Request.Context(), host); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	}
}

// abortHandler 把指定 engagement 置为 aborted。
// 底层 store 对未知 ID 当前返回成功（UPDATE 影响 0 行），保持原语义；
// 如需 404 区分需调用方先 GetByID，本层不强加策略。
func abortHandler(api EngagementsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if id == "" {
			c.JSON(400, gin.H{"error": "id required"})
			return
		}
		if err := api.Abort(c.Request.Context(), id); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	}
}
