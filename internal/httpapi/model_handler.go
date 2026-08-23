// Package httpapi: 模型模块 CRUD handler（前端「LLM 配置」页）。
//
// 两资源对应 migration 0097+0099+0103+0105：llm_provider（部署注册表）/ llm_role_route（能力档
// → provider 路由，0105 引入 tier 中间层）。写路径一律走 llmstore（落 DB + redis 广播失效），
// runner 进程的两个 LLM 工厂下次 For(role) 即读到最新路由（见 D7），绝不直穿底层 store。
//
// 路由模型：agent → tier → provider 两跳。agent→tier 固定在代码（llmcfg.AgentTier），
// tier→provider 落 llm_role_route（role 列存档名 heavy/vision/light）。保留 role __fallback__
// （retry 备胎）与档同表，前端「能力分档」把它单列为「全局备胎」；未绑定的档回落隐式默认档 heavy。
//
// API Key（migration 0103）：前端直填明文，handler 用 cryptx.Cipher 加密成
// EncryptedAPIKey 才交给 llmstore 落库——本包是唯一见到明文的 Go 层（仅在这次请求处理
// 期间持有，不落任何字段/日志）。请求体 api_key 留空 = 不改动已存密钥（编辑时的默认行为）。
// 响应用 key_present 提示「是否已设置」，绝不回显明文或密文。
package httpapi

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/config/llmcfg"
)

// KeyEncrypter 加密前端直填的明文 API Key（*cryptx.Cipher 自动满足）。
type KeyEncrypter interface {
	Encrypt(plaintext string) ([]byte, error)
}

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
// api_key 是前端直填的明文，仅本次请求内存活，handler 加密后即弃（见 saveProviderHandler）；
// 编辑时留空 = 不改动已存密钥。
type providerBody struct {
	Key            string `json:"key"`
	Type           string `json:"type"`
	BaseURL        string `json:"base_url"`
	DefaultModel   string `json:"default_model"`
	APIKey         string `json:"api_key"`
	MaxTokens      int    `json:"max_tokens"`
	SupportsTools  bool   `json:"supports_tools"`
	SupportsVision bool   `json:"supports_vision"`
	ContextWindow  int    `json:"context_window"`
	Description    string `json:"description"`
	SortOrder      int    `json:"sort_order"`
	Enabled        bool   `json:"enabled"`
}

// saveProviderHandler 处理 POST /models/providers 与 PUT /models/providers/:key（均走 upsert-by-key）。
// enc 加密请求体里的明文 api_key；新建（POST）必须带 key，编辑（PUT）留空则 KeepExistingKey。
func saveProviderHandler(api ModelAPI, enc KeyEncrypter) gin.HandlerFunc {
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
		if b.BaseURL == "" || b.DefaultModel == "" {
			c.JSON(400, gin.H{"error": "base_url / default_model 不能为空"})
			return
		}
		isNew := c.Request.Method == "POST"
		if isNew && b.APIKey == "" {
			c.JSON(400, gin.H{"error": "api_key 不能为空（新建 provider 必须提供密钥）"})
			return
		}
		if b.ContextWindow <= 0 {
			c.JSON(400, gin.H{"error": "context_window 必须 > 0（model 总上下文窗口 tokens）"})
			return
		}
		params := llmcfg.ProviderParams{
			Key: b.Key, Type: b.Type, BaseURL: b.BaseURL, DefaultModel: b.DefaultModel,
			MaxTokens: b.MaxTokens, SupportsTools: b.SupportsTools,
			SupportsVision: b.SupportsVision, ContextWindow: b.ContextWindow,
			Description: b.Description, SortOrder: b.SortOrder, Enabled: b.Enabled,
		}
		if b.APIKey == "" {
			params.KeepExistingKey = true // 编辑未重填密钥：沿用已存密文
		} else {
			sealed, err := enc.Encrypt(b.APIKey)
			if err != nil {
				c.JSON(500, gin.H{"error": "加密密钥失败: " + err.Error()})
				return
			}
			params.EncryptedAPIKey = sealed
			params.APIKeyLast4 = keyLast4(b.APIKey) // 与密文成对写入（脱敏辨识用）
		}
		p, err := api.SaveProvider(c.Request.Context(), params)
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
// key_present：该 provider 是否已有可用密钥来源（加密落库或旧 ENV 变量名），不含密钥值本身。
// key_last4：明文末 4 位（脱敏辨识锚点，非密钥值）；旧行或仅 ENV 回退时为空串。
func providerJSON(p llmcfg.Provider) gin.H {
	return gin.H{
		"key": p.Key, "type": p.Type, "base_url": p.BaseURL, "default_model": p.DefaultModel,
		"key_present": p.HasStoredKey(), "key_last4": p.APIKeyLast4,
		"max_tokens": p.MaxTokens, "supports_tools": p.SupportsTools,
		"supports_vision": p.SupportsVision, "context_window": p.ContextWindow,
		"description": p.Description, "sort_order": p.SortOrder, "enabled": p.Enabled,
		"created_at": p.CreatedAt, "updated_at": p.UpdatedAt,
	}
}

// keyLast4 取密钥明文末 4 位（脱敏辨识用）。短于 4 位时返回全部，避免越界。
func keyLast4(plaintext string) string {
	r := []rune(plaintext)
	if len(r) <= 4 {
		return string(r)
	}
	return string(r[len(r)-4:])
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

// deleteRoleRouteHandler 处理 DELETE /models/routes/:role（删后该档回落隐式默认档 heavy）。
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
