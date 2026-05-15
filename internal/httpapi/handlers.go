package httpapi

import (
	"context"
	"fmt"

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

// EngagementsAPI 是 handlers 对 engagement store 的窄接口。
// EnsureProxySession：返回当前 active proxy session（不存在则建新），不带 host。
// Abort：把 engagement 置为 aborted。
// List：按 created_at DESC 列最近 N 个；前端 viewer 下拉用。
//
// v0033：proxy session 不再 per-host，单个 active proxy 容纳所有 host 流量。
type EngagementsAPI interface {
	Abort(ctx context.Context, id string) error
	EnsureProxySession(ctx context.Context) (string, error)
	List(ctx context.Context, limit int) ([]EngagementSummary, error)
}

// EngagementSummary 是 List 返回行——只暴露前端 viewer 需要的字段，
// 不直接返回 engagement.Engagement 完整结构（避免泄露大字段 + 减小响应体）。
//
// v0033：删除 TargetHost，加 Scope（jsonb 字符串）+ ExpiresAt（RFC3339）。
type EngagementSummary struct {
	ID            string `json:"id"`
	Scope         string `json:"scope"`                   // jsonb raw（如 {"any":true} / {"hosts":[...]}）
	Status        string `json:"status"`
	Mode          string `json:"mode"`
	FlowCount     int    `json:"flow_count"`
	FindingCount  int    `json:"finding_count"`
	AgentRunCount int    `json:"agent_run_count"`
	CreatedAt     string `json:"created_at"`              // RFC3339
	ExpiresAt     string `json:"expires_at,omitempty"`    // RFC3339（proxy 模式）；browser 为空
	EndedAt       string `json:"ended_at,omitempty"`      // RFC3339（可空）
	ErrorMessage  string `json:"error_message,omitempty"`
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

// createProxyHandler 处理 POST /engagement/proxy：返回当前 active proxy session
// （不存在则建新）。v0033 起 proxy session 不再 per-host，请求体为空 {}。
// 幂等：重复调用在 TTL 窗口内返回同一 engagement_id；过期由 ingestor 内部 Rotator 轮转。
func createProxyHandler(api EngagementsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := api.EnsureProxySession(c.Request.Context())
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"engagement_id": id})
	}
}

// listEngagementsHandler 处理 GET /engagement?limit=<optional>。
// 返回最近 N 个 engagement 摘要，前端用作下拉选择。
// v0033：删除 ?host= 过滤——按 host 查找请改走 finding/flow 子资源接口。
func listEngagementsHandler(api EngagementsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := 0
		if v := c.Query("limit"); v != "" {
			// 容错：解析失败时让 store 端用默认值，不在 handler 里校验数字范围。
			_, _ = fmt.Sscanf(v, "%d", &limit)
		}
		list, err := api.List(c.Request.Context(), limit)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"engagements": list})
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
