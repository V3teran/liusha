// Package httpapi: LLM 配置 CRUD handler（前端「LLM 配置」页，/models 系列）。
//
// 两资源：llm_provider（provider 注册表）/ llm_role_route（档位 → provider 路由）。写路径一律
// 走 llmstore（落 DB + redis 广播失效），各进程的 llm.Router 下次 For(complexity) 即读到最新
// 路由，绝不直穿底层 store。
//
// 路由模型：档位 → provider（llm_role_route 的 role 列存档名 simple/medium/complex）；agent 的
// 档位在其生效配置的 complexity。缺档由 llm.Router 就近降级到相邻档；重试耗尽即失败。
//
// API Key：前端直填明文，handler 用 cryptx.Cipher 加密成
// EncryptedAPIKey 才交给 llmstore 落库——本包是唯一见到明文的 Go 层（仅在这次请求处理
// 期间持有，不落任何字段/日志）。请求体 api_key 留空 = 不改动已存密钥（编辑时的默认行为）。
// 响应用 key_present 提示「是否已设置」，绝不回显明文或密文。

package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/llmconfig"
)

// KeyEncrypter 加密前端直填的明文 API Key（*cryptx.Cipher 自动满足）。
type KeyEncrypter interface {
	Encrypt(plaintext string) ([]byte, error)
}

// ModelAPI 是 LLM 配置 CRUD 依赖的窄接口；*llmstore.Store 自动满足。
// 读经多级缓存 / 直穿 DB、写经失效广播的语义全在 llmstore 内，handler 只做 HTTP 编解码 + 校验。
type ModelAPI interface {
	// provider 部署
	ListProviders(ctx context.Context, onlyEnabled bool) ([]llmconfig.Provider, error)
	ProviderByCode(ctx context.Context, code string) (llmconfig.Provider, error)
	CreateProvider(ctx context.Context, p llmconfig.ProviderParams) (llmconfig.Provider, error)
	UpdateProvider(ctx context.Context, p llmconfig.ProviderParams) (llmconfig.Provider, error)
	DeleteProvider(ctx context.Context, code string) error
	// ClearProviderKey 清除 UI 设置的加密密钥（密文+尾号），保留 api_key_env 引用——「恢复使用环境变量」。
	ClearProviderKey(ctx context.Context, code string) (llmconfig.Provider, error)
	// 档位路由（档 → provider 直连）
	ListRoleRoutes(ctx context.Context) ([]llmconfig.RoleRoute, error)
	UpsertRoleRoute(ctx context.Context, role, providerKey string) (llmconfig.RoleRoute, error)
	DeleteRoleRoute(ctx context.Context, role string) error
}

// ── provider ──────────────────────────────────────────────────────────

// listProvidersHandler 处理 GET /models/providers（全量，含 disabled，供路由绑定选择器一次拉全）。
func listProvidersHandler(api ModelAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := api.ListProviders(c.Request.Context(), false)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		out := make([]gin.H, 0, len(rows))
		for _, p := range rows {
			out = append(out, providerJSON(p))
		}
		c.JSON(http.StatusOK, gin.H{"providers": out})
	}
}

// getProviderHandler 处理 GET /models/providers/:code（单条读走 ProviderByCode 多级缓存）。
func getProviderHandler(api ModelAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, err := api.ProviderByCode(c.Request.Context(), c.Param("code"))
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"provider": providerJSON(p)})
	}
}

// providerBody 是 POST/PUT provider 的请求体。api_key 是前端直填的明文，仅本次请求
// 内存活，handler 加密后即弃；编辑时留空 = 不改动已存密钥。
// code 只在 POST 时可选携带（脚本/导入场景指定标识，缺省由系统生成 UUID）；PUT 的
// 标识只认路径，请求体携带且与路径不一致时拒绝，避免改错对象。
type providerBody struct {
	Code                 string `json:"code"`
	Type                 string `json:"type"`
	BaseURL              string `json:"base_url"`
	DefaultModel         string `json:"default_model"`
	APIKey               string `json:"api_key"`
	AuthScheme           string `json:"auth_scheme"`        // 空 = 协议标准方式
	OutputLimitParam     string `json:"output_limit_param"` // 空 = max_tokens
	MaxTokens            int    `json:"max_tokens"`
	ThinkingBudgetTokens int64  `json:"thinking_budget_tokens"`
	SupportsTools        bool   `json:"supports_tools"`
	SupportsVision       bool   `json:"supports_vision"`
	ContextWindow        int    `json:"context_window"`
	DisplayName          string `json:"display_name"`
	Description          string `json:"description"`
	SortOrder            int    `json:"sort_order"`
	Enabled              bool   `json:"enabled"`
}

// params 把请求体组装为落库入参：明文密钥在此加密（留空 = 沿用已存密钥）。
func (b providerBody) params(code string, enc KeyEncrypter) (llmconfig.ProviderParams, error) {
	p := llmconfig.ProviderParams{
		Code: code, Type: b.Type, BaseURL: b.BaseURL, DefaultModel: b.DefaultModel,
		AuthScheme: b.AuthScheme, OutputLimitParam: b.OutputLimitParam,
		MaxTokens: b.MaxTokens, ThinkingBudgetTokens: b.ThinkingBudgetTokens,
		SupportsTools: b.SupportsTools, SupportsVision: b.SupportsVision, ContextWindow: b.ContextWindow,
		DisplayName: strings.TrimSpace(b.DisplayName), Description: b.Description, SortOrder: b.SortOrder, Enabled: b.Enabled,
	}
	if b.APIKey == "" {
		p.KeepExistingKey = true // 编辑未重填密钥：沿用已存密文
		return p, nil
	}
	sealed, err := enc.Encrypt(b.APIKey)
	if err != nil {
		return p, fmt.Errorf("加密密钥失败: %w", err)
	}
	p.EncryptedAPIKey = sealed
	p.APIKeyLast4 = keyLast4(b.APIKey) // 与密文成对写入（脱敏辨识用）
	return p, nil
}

// validated 归一并校验入参（与落库同一口径：Normalize → Validate）。
func validated(p llmconfig.ProviderParams) (llmconfig.ProviderParams, error) {
	p.Normalize()
	return p, p.Validate()
}

// createProviderHandler 处理 POST /models/providers：新建必须带密钥；code 已存在 → 409。
func createProviderHandler(api ModelAPI, enc KeyEncrypter) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b providerBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if b.APIKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "新建 provider 必须提供 API Key"})
			return
		}
		code := strings.TrimSpace(b.Code)
		if code == "" {
			code = uuid.NewString()
		}
		params, err := b.params(code, enc)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		if params, err = validated(params); err != nil {
			respondErr(c, http.StatusBadRequest, err)
			return
		}
		p, err := api.CreateProvider(c.Request.Context(), params)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"provider": providerJSON(p)})
	}
}

// updateProviderHandler 处理 PUT /models/providers/:code：只改路径指向的既有 provider
// （不存在 → 404，不借 PUT 新建）；api_key 留空 = 沿用已存密钥。
func updateProviderHandler(api ModelAPI, enc KeyEncrypter) gin.HandlerFunc {
	return func(c *gin.Context) {
		code := c.Param("code")
		var b providerBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if b.Code != "" && b.Code != code {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体 code 与路径不一致"})
			return
		}
		params, err := b.params(code, enc)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		if params, err = validated(params); err != nil {
			respondErr(c, http.StatusBadRequest, err)
			return
		}
		p, err := api.UpdateProvider(c.Request.Context(), params)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"provider": providerJSON(p)})
	}
}

// deleteProviderHandler 处理 DELETE /models/providers/:code。
// 被档位路由 FK 引用（ON DELETE RESTRICT）时撞约束（23503）→ 409 中文提示；不存在 → 404。
func deleteProviderHandler(api ModelAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := api.DeleteProvider(c.Request.Context(), c.Param("code")); err != nil {
			if isForeignKeyViolation(err) {
				c.JSON(http.StatusConflict, gin.H{"error": "该 provider 仍被档位路由引用，请先改绑档位再删除"})
				return
			}
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// clearProviderKeyHandler 处理 DELETE /models/providers/:code/key：「恢复使用环境变量」。
// 清除 UI 设置的加密密钥后，解析回落到 api_key_env 指向的环境变量；无 env 引用 → 409。
func clearProviderKeyHandler(api ModelAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, err := api.ClearProviderKey(c.Request.Context(), c.Param("code"))
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"provider": providerJSON(p)})
	}
}

// providerJSON 是 provider 响应的单一序列化点。
// key_present：当前能否解析出密钥（已加密落库，或 api_key_env 指向的变量有值），不含密钥值本身。
// key_last4：明文末 4 位（脱敏辨识锚点，非密钥值）；仅 ENV 来源时为空串。
func providerJSON(p llmconfig.Provider) gin.H {
	return gin.H{
		"code": p.Code, "type": p.Type, "base_url": p.BaseURL, "default_model": p.DefaultModel,
		"api_key_env": p.APIKeyEnv, // env 变量名引用（非密钥值）；UI 密文存在时被覆盖
		"key_present": p.HasKey(), "key_last4": p.APIKeyLast4,
		"auth_scheme": p.AuthScheme, "output_limit_param": p.OutputLimitParam,
		"max_tokens": p.MaxTokens, "thinking_budget_tokens": p.ThinkingBudgetTokens,
		"supports_tools": p.SupportsTools, "supports_vision": p.SupportsVision, "context_window": p.ContextWindow,
		"display_name": p.DisplayName, "description": p.Description, "sort_order": p.SortOrder, "enabled": p.Enabled,
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

// ── role route（档位 → provider）──────────────────────────────────────

// listRoutingHandler 处理 GET /models/routing：返回全部路由行，前端「能力路由」全景。
func listRoutingHandler(api ModelAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		routes, err := api.ListRoleRoutes(c.Request.Context())
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		outRoutes := make([]gin.H, 0, len(routes))
		for _, r := range routes {
			outRoutes = append(outRoutes, roleRouteJSON(r))
		}
		c.JSON(http.StatusOK, gin.H{"routes": outRoutes})
	}
}

// roleRouteBody 是 PUT /models/routes/:role 的请求体（role 从路径取）。
type roleRouteBody struct {
	ProviderCode string `json:"provider_code"`
}

// saveRoleRouteHandler 处理 PUT /models/routes/:role（upsert 档 → provider 映射）。
// 档名须为 simple/medium/complex（store 校验 → 400）；provider_code 不存在时撞 FK（23503）→ 409。
func saveRoleRouteHandler(api ModelAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := c.Param("role")
		var b roleRouteBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if b.ProviderCode == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "provider_code 不能为空"})
			return
		}
		rr, err := api.UpsertRoleRoute(c.Request.Context(), role, b.ProviderCode)
		if err != nil {
			if isForeignKeyViolation(err) {
				c.JSON(http.StatusConflict, gin.H{"error": "provider 不存在，请先创建对应 provider"})
				return
			}
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"route": roleRouteJSON(rr)})
	}
}

// deleteRoleRouteHandler 处理 DELETE /models/routes/:role（删后该档按就近降级解析；幂等，
// 不限档名——历史遗留的非法档位行也可借此清理）。
func deleteRoleRouteHandler(api ModelAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := api.DeleteRoleRoute(c.Request.Context(), c.Param("role")); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// roleRouteJSON 是角色路由响应的单一序列化点。
func roleRouteJSON(r llmconfig.RoleRoute) gin.H {
	return gin.H{
		"role": r.Role, "provider_code": r.ProviderCode,
		"created_at": r.CreatedAt, "updated_at": r.UpdatedAt,
	}
}
