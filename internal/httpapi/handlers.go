package httpapi

import (
	"context"
	"fmt"
	"strings"

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

// OwnersAPI 是 handlers 对  owner store 的窄接口。
// EnsurePassiveSession：按 host 找/建 active passive_session（v1.1 per-host 单 active）。
//   - host 非空 → 调 passivesession.Store.LookupOrCreate 返该 host 的 owner_id
//   - host 空   → 返空 id（向后兼容旧 mitmproxy 预热路径"代理就绪信号"）
//
// Abort：把  owner 置为 aborted。
// List：按 created_at DESC 列最近 N 个；前端下拉用。
type OwnersAPI interface {
	Abort(ctx context.Context, id string) error
	EnsurePassiveSession(ctx context.Context, host string) (string, error)
	List(ctx context.Context, limit int) ([]OwnerSummary, error)
}

// OwnerSummary 是 List 返回行——只暴露前端需要的字段，
// 不直接返回 passive_session/active_scan 完整结构（避免泄露大字段 + 减小响应体）。
type OwnerSummary struct {
	ID           string `json:"id"`
	Scope        string `json:"scope"` // jsonb raw（如 {"any":true} / {"hosts":[...]}）
	Status       string `json:"status"`
	Mode         string `json:"mode"`
	CreatedAt    string `json:"created_at"`           // RFC3339
	ExpiresAt    string `json:"expires_at,omitempty"` // RFC3339 proxy session 必填
	EndedAt      string `json:"ended_at,omitempty"`   // RFC3339（可空）
	ErrorMessage string `json:"error_message,omitempty"`
	// ConversationID 关联本会话的对话流（阶段2，passive 会话用）；前端据此打开对话流插话。空=未绑。
	ConversationID string `json:"conversation_id,omitempty"`
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

// passiveScanHandler 处理 POST /scan/passive：按 host 找/建 passive_session。
// body: {"host":"example.com:8080"}（host 可空，空则返空 id 作"代理就绪"信号）
// 幂等：v1.1 per-host 单 active 模型，同 host 重复调用返同一 owner_id；过期由 sweeper 轮转。
//
// 与 activeScanHandler 路径对仗：/scan/passive 开"被动接流量入口"，/scan/active 触发"主动扫描"。
func passiveScanHandler(api OwnersAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Host string `json:"host"`
		}
		// 容错：body 空/解析失败都不报错（向后兼容旧 client）
		_ = c.ShouldBindJSON(&body)
		id, err := api.EnsurePassiveSession(c.Request.Context(), body.Host)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"owner_id": id})
	}
}

// listSessionsHandler 处理 GET /session?limit=<optional>。
// 返回最近 N 个  owner 摘要，前端用作下拉选择。
// 按 host 查找请改走 finding/flow 子资源接口。
func listSessionsHandler(api OwnersAPI) gin.HandlerFunc {
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
		c.JSON(200, gin.H{"sessions": list})
	}
}

// abortHandler 把指定  owner 置为 aborted。
// 底层 store 对未知 ID 当前返回成功（UPDATE 影响 0 行），保持原语义；
// 如需 404 区分需调用方先 GetByID，本层不强加策略。
func abortHandler(api OwnersAPI) gin.HandlerFunc {
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

// ActiveScanAPI 是 handlers 对 active 模式扫描入口的窄接口。
// CreateActiveScan 一站式做三件事：建 active scan、建 hunter agent_run、入 asynq 队列；
// 由 cmd/api 的 adapter 用 owner store + hunter.Store + worker.Client 实现。
type ActiveScanAPI interface {
	CreateActiveScan(ctx context.Context, brief string) (ownerID, agentRunID string, err error)
}

// CreateActiveScanRequest 是 POST /scan/active 请求体。
//
// Brief 必填——用户自然语言任务简报，含目标 URL/IP / 账号密码 / 测试方向等全部信息。
// 后端不解析 brief（不抽 URL、不做 NL parser），整段透传给 hunter LLM 自行识别。
//
// 例：
//
//	{"brief": "测试网站 http://111.229.193.40:34280/login.php，账号 admin/password，只测 XSS"}
//
// 这种"一句话"形态便于将来接通微信 / 飞书 / 钉钉机器人——平台原文直接转发即可。
type CreateActiveScanRequest struct {
	Brief string `json:"brief"`
}

// activeScanHandler 处理 POST /scan/active：校验 brief 非空 + 调 ActiveScanAPI 起任务。
//
// 成功返 200 + {owner_id, hunter_id}；调用方据此查任务进度
// （前端 / GET /llm/invocations/:owner_id）。
// 不等任务完成——异步 ReAct 由 scanner 进程消费。
func activeScanHandler(api ActiveScanAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreateActiveScanRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		brief := strings.TrimSpace(req.Brief)
		if brief == "" {
			c.JSON(400, gin.H{"error": "brief required"})
			return
		}

		eid, hunterID, err := api.CreateActiveScan(c.Request.Context(), brief)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{
			"owner_id":  eid,
			"hunter_id": hunterID,
		})
	}
}
