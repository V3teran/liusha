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

// TaskAPI 是 handlers 对 task store 的窄接口。
//
// 合表后 owner 概念坍缩为 task：
//   - Abort：把 task 置为 aborted。
//   - List：按 created_at DESC 列最近 N 个 task（各场景混列）；前端下拉用。
//
// task 由 API 下发或 ingestor 聚合器按流量窗口生成，不走预热路径。
type TaskAPI interface {
	Abort(ctx context.Context, id string) error
	List(ctx context.Context, limit int) ([]TaskSummary, error)
}

// TaskSummary 是 List 返回行——只暴露前端需要的字段，不直接返回 task 完整结构。
type TaskSummary struct {
	ID           string `json:"id"`
	Scope        string `json:"scope"` // jsonb raw：{"brief":..., "target_host":...}
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`         // RFC3339
	EndedAt      string `json:"ended_at,omitempty"` // RFC3339（可空）
	ErrorMessage string `json:"error_message,omitempty"`
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

// listTasksHandler 处理 GET /tasks?limit=<optional>。
// 返回最近 N 个 task 摘要，前端用作下拉选择。
// 按 host 查找请改走 finding/traffic 子资源接口。
func listTasksHandler(api TaskAPI) gin.HandlerFunc {
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
		c.JSON(200, gin.H{"tasks": list})
	}
}

// abortTaskHandler 把指定 task 置为 aborted。
// 底层 store 对未知 ID 当前返回成功（UPDATE 影响 0 行），保持原语义；
// 如需 404 区分需调用方先 GetByID，本层不强加策略。
func abortTaskHandler(api TaskAPI) gin.HandlerFunc {
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

// ScanAPI 是 handlers 对扫描入口的窄接口（无会话纯后台扫描）。
// CreateScan 一站式做三件事：建 scan、建 agent agent_run、入 asynq 队列；
// 由 cmd/api 的 adapter 用 task store + agent.Store + worker.Client 实现。
type ScanAPI interface {
	CreateScan(ctx context.Context, brief string) (taskID, agentID string, err error)
}

// CreateScanRequest 是 POST /scan 请求体。
//
// Brief 必填——用户自然语言任务简报，含目标 URL/IP / 账号密码 / 测试方向等全部信息。
// 后端不解析 brief（不抽 URL、不做 NL parser），整段透传给 agent LLM 自行识别。
// ScenarioID 必填——场景 code，runner 据此数据驱动派发引擎与操作员编排。
//
// 例：
//
//	{"brief": "测试网站 http://111.229.193.40:34280/login.php，账号 admin/password，只测 XSS", "": "web-pentest"}
//
// 这种"一句话"形态便于将来接通微信 / 飞书 / 钉钉机器人——平台原文直接转发即可。
type CreateScanRequest struct {
	Brief      string `json:"brief"`
}

// scanHandler 处理 POST /scan：校验 brief + _id 非空 + 调 ScanAPI 起任务。
//
// 成功返 200 + {task_id, agent_id}；调用方据此查任务进度
// （前端 / GET /llm/invocations/:task_id）。
// 不等任务完成——异步 ReAct 由 runner 进程消费。
func scanHandler(api ScanAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreateScanRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		brief := strings.TrimSpace(req.Brief)
		if brief == "" {
			c.JSON(400, gin.H{"error": "brief required"})
			return
		}
	}
}
