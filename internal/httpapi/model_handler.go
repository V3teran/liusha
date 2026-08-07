// Package httpapi: 模型模块 CRUD handler（前端「模型」配置页）。
//
// 两资源对应 migration 0097+0099：llm_provider（部署注册表）/ llm_role_route（角色 → provider
// 直连路由，0099 拆掉别名中间层）。写路径一律走 llmstore（落 DB + redis 广播失效），
// runner 进程的两个 LLM 工厂下次 For(role) 即读到最新路由（见 D7），绝不直穿底层 store。
//
// 路由模型：role → provider 一跳直连。两个保留 role（__default__ 未命中兜底 / __fallback__ retry
// 备胎）与业务 role 同表，前端「角色指派」把它们单列为「全局兜底 / 全局备胎」。
//
// 安全铁律：provider.api_key_env 只存/回**环境变量名**，密钥值永不落库、永不出网。
// 响应附 key_present（该 ENV 当前是否非空）供前端提示「密钥未注入」，仍不泄露值本身。
package httpapi

import (
	"context"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/config/llmcfg"
)

// ModelAPI 是模型模块 CRUD 依赖的窄接口；*llmstore.Store 自动满足。
// 读经多级缓存 / 直穿 DB、写经失效广播的语义全在 llmstore 内，handler 只做 HTTP 编解码 + 校验。
type ModelAPI interface {
	// provider 部署
	ListProviders(ctx context.Context, onlyEnabled bool) ([]llmcfg.Provider, error)
	ProviderByKey(ctx context.Context, key string) (llmcfg.Provider, error)
	SaveProvider(ctx context.Context, p llmcfg.ProviderParams) (llmcfg.Provider, error)
	DeleteProvider(ctx context.Context, key string) error
	// 角色路由（role → provider 直连）
	ListRoleRoutes(ctx context.Context) ([]llmcfg.RoleRoute, error)
	UpsertRoleRoute(ctx context.Context, role, providerKey string) (llmcfg.RoleRoute, error)
	DeleteRoleRoute(ctx context.Context, role string) error
}

// ── provider ──────────────────────────────────────────────────────────

// listProvidersHandler 处理 GET /models/providers（全量，含 disabled，供路由绑定选择器一次拉全）。
func listProvidersHandler(api ModelAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := api.ListProviders(c.Request.Context(), false)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		out := make([]gin.H, 0, len(rows))
		for _, p := range rows {
			out = append(out, providerJSON(p))
		}
		c.JSON(200, gin.H{"providers": out})
	}
}

// getProviderHandler 处理 GET /models/providers/:key（单条读走 ProviderByKey 多级缓存）。
func getProviderHandler(api ModelAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, err := api.ProviderByKey(c.Request.Context(), c.Param("key"))
		if err != nil {
			c.JSON(404, gin.H{"error": err.Error(), "key": c.Param("key")})
			return
		}
		c.JSON(200, gin.H{"provider": providerJSON(p)})
	}
}

// providerBody 是 POST/PUT provider 的请求体。type 应用层白名单校验；
// api_key_env 只收 ENV 变量名（密钥值绝不经此传输）。
type providerBody struct {
	Key            string `json:"key"`
	Type           string `json:"type"`
	BaseURL        string `json:"base_url"`
	DefaultModel   string `json:"default_model"`
	APIKeyEnv      string `json:"api_key_env"`
	MaxTokens      int    `json:"max_tokens"`
	SupportsTools  bool   `json:"supports_tools"`
	SupportsVision bool   `json:"supports_vision"`
	ContextWindow  int    `json:"context_window"`
	Description    string `json:"description"`
	SortOrder      int    `json:"sort_order"`
	Enabled        bool   `json:"enabled"`
}

// saveProviderHandler 处理 POST /models/providers 与 PUT /models/providers/:key（均走 upsert-by-key）。
func saveProviderHandler(api ModelAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b providerBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(400, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if b.Key == "" {
			c.JSON(400, gin.H{"error": "key 不能为空"})
			return
		}
		if b.Type != llmcfg.ProviderTypeOpenAICompat && b.Type != llmcfg.ProviderTypeAnthropic {
			c.JSON(400, gin.H{"error": "非法 type（应为 openai_compat|anthropic）"})
			return
		}
		if b.BaseURL == "" || b.DefaultModel == "" || b.APIKeyEnv == "" {
			c.JSON(400, gin.H{"error": "base_url / default_model / api_key_env 不能为空"})
			return
		}
		if b.ContextWindow <= 0 {
			c.JSON(400, gin.H{"error": "context_window 必须 > 0（model 总上下文窗口 tokens）"})
			return
		}
		p, err := api.SaveProvider(c.Request.Context(), llmcfg.ProviderParams{
			Key: b.Key, Type: b.Type, BaseURL: b.BaseURL, DefaultModel: b.DefaultModel,
			APIKeyEnv: b.APIKeyEnv, MaxTokens: b.MaxTokens, SupportsTools: b.SupportsTools,
			SupportsVision: b.SupportsVision, ContextWindow: b.ContextWindow,
			Description: b.Description, SortOrder: b.SortOrder, Enabled: b.Enabled,
		})
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"provider": providerJSON(p)})
	}
}

// deleteProviderHandler 处理 DELETE /models/providers/:key。
// 被角色路由 FK 引用（ON DELETE RESTRICT）时撞约束（23503）→ 409 中文提示。
func deleteProviderHandler(api ModelAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.Param("key")
		if err := api.DeleteProvider(c.Request.Context(), key); err != nil {
			if isForeignKeyViolation(err) {
				c.JSON(409, gin.H{"error": "该 provider 仍被角色路由引用，请先改绑角色再删除"})
				return
			}
			c.JSON(500, gin.H{"error": err.Error(), "key": key})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	}
}

// providerJSON 是 provider 响应的单一序列化点。
// key_present：api_key_env 指向的 ENV 当前是否非空（提示密钥是否已注入；不含值）。
func providerJSON(p llmcfg.Provider) gin.H {
	return gin.H{
		"key": p.Key, "type": p.Type, "base_url": p.BaseURL, "default_model": p.DefaultModel,
		"api_key_env": p.APIKeyEnv, "key_present": os.Getenv(p.APIKeyEnv) != "",
		"max_tokens": p.MaxTokens, "supports_tools": p.SupportsTools,
		"supports_vision": p.SupportsVision, "context_window": p.ContextWindow,
		"description": p.Description, "sort_order": p.SortOrder, "enabled": p.Enabled,
		"created_at": p.CreatedAt, "updated_at": p.UpdatedAt,
	}
}

// ── role route（role → provider 直连；0099 拆别名层后无别名端点）──────────

// listRoutingHandler 处理 GET /models/routing：返回全部角色路由（含保留 role），前端「角色指派」全景。
func listRoutingHandler(api ModelAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		routes, err := api.ListRoleRoutes(c.Request.Context())
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		outRoutes := make([]gin.H, 0, len(routes))
		for _, r := range routes {
			outRoutes = append(outRoutes, roleRouteJSON(r))
		}
		c.JSON(200, gin.H{"routes": outRoutes})
	}
}

// roleRouteBody 是 PUT /models/routes/:role 的请求体（role 从路径取）。
type roleRouteBody struct {
	ProviderKey string `json:"provider_key"`
}

// saveRoleRouteHandler 处理 PUT /models/routes/:role（upsert 角色 → provider 直连映射）。
// provider_key 不存在时撞 FK（23503）→ 409。
func saveRoleRouteHandler(api ModelAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := c.Param("role")
		if role == "" {
			c.JSON(400, gin.H{"error": "role 不能为空"})
			return
		}
		var b roleRouteBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(400, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if b.ProviderKey == "" {
			c.JSON(400, gin.H{"error": "provider_key 不能为空"})
			return
		}
		rr, err := api.UpsertRoleRoute(c.Request.Context(), role, b.ProviderKey)
		if err != nil {
			if isForeignKeyViolation(err) {
				c.JSON(409, gin.H{"error": "provider_key 不存在，请先创建对应 provider"})
				return
			}
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"route": roleRouteJSON(rr)})
	}
}

// deleteRoleRouteHandler 处理 DELETE /models/routes/:role（删后该 role 回退 __default__ 兜底）。
func deleteRoleRouteHandler(api ModelAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := c.Param("role")
		if err := api.DeleteRoleRoute(c.Request.Context(), role); err != nil {
			c.JSON(500, gin.H{"error": err.Error(), "role": role})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	}
}

// roleRouteJSON 是角色路由响应的单一序列化点。
func roleRouteJSON(r llmcfg.RoleRoute) gin.H {
	return gin.H{
		"role": r.Role, "provider_key": r.ProviderKey,
		"created_at": r.CreatedAt, "updated_at": r.UpdatedAt,
	}
}
