// Package llmcfg 是 LLM 配置（provider 部署 / 角色路由）的持久化层。
// 两表对应 migration 0097+0099：llm_provider / llm_role_route（0099 拆掉别名中间层）。
//
// 事实源是 DB（前端「模型」模块 CRUD 改这里）；yaml 仅首次 insert-only 种子。
// 运行期消费方经 internal/llmstore（多级缓存）读，不直连本包 Store（跨进程一致性，见 D7）。
//
// 路由模型（0099 起）：role → provider **一跳直连**（取代旧 role → 别名 → provider 两跳）。
// 未命中任何显式 role 时兜底到保留 role RoleDefault；retry 耗尽切换到保留 role RoleFallback。
//
// 安全：APIKeyEnv 只存**环境变量名**，密钥值永不落库。
package llmcfg

import "time"

// Provider type 常量（与 llm_provider.type CHECK 一致）。
const (
	ProviderTypeOpenAICompat = "openai_compat"
	ProviderTypeAnthropic    = "anthropic"
)

// Provider 是一个 LLM provider 部署（连接参数 + 能力标志）。
// 对应旧 config.ProviderConfig，但 SupportsVision/ContextWindow 落 DB 为 NOT NULL 非指针
// （建表 CHECK + NOT NULL 已在 schema 层强制，无需 *bool/*int 区分「未填」）。
type Provider struct {
	Key            string
	Type           string
	BaseURL        string
	DefaultModel   string
	APIKeyEnv      string // 仅 ENV 变量名
	MaxTokens      int
	SupportsTools  bool
	SupportsVision bool
	ContextWindow  int
	Description    string
	SortOrder      int
	Enabled        bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ProviderParams 是 Provider 的 upsert 入参（Key 是稳定引用键，Update 按 Key 定位）。
type ProviderParams struct {
	Key            string
	Type           string
	BaseURL        string
	DefaultModel   string
	APIKeyEnv      string
	MaxTokens      int
	SupportsTools  bool
	SupportsVision bool
	ContextWindow  int
	Description    string
	SortOrder      int
	Enabled        bool
}

// RoleRoute 是角色 → provider key 直连（orchestrator→qwen 等）；role 未命中走 RoleDefault。
type RoleRoute struct {
	Role        string
	ProviderKey string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Routing 是路由全景快照：全部角色 → provider key 直连映射（含两个保留 role）。
// 运行期解析（role → provider key）一次性读齐，避免多次 DB 往返。
type Routing struct {
	Roles map[string]string // role → provider key（含保留 role RoleDefault/RoleFallback）
}

// 保留 role：不对应任何真实 agent，是两个**全局**语义槽（0099 拆别名层后由保留 role 承载）。
//   - RoleDefault  ：role 未命中任何显式路由时的兜底（等价旧 default 别名 / default_provider）
//   - RoleFallback ：primary retry 耗尽后切换的备胎（等价旧 fallback 别名 / fallback_provider）
//
// 用 __双下划线__ 前后缀与真实 role（traffic-analysis/orchestrator/inspector/compactor…）隔离，
// 前端「角色指派」把它们单列为「全局兜底 / 全局备胎」，不与业务 role 混排。
const (
	RoleDefault  = "__default__"
	RoleFallback = "__fallback__"
)

// ProviderKeyForRole 把 role 一跳解析到 provider key：role → provider（0099 起无别名中间层）。
// role 未命中 → RoleDefault 兜底；仍未命中 → 空（调用方据此报错，不静默兜底到任意 provider）。
func (r Routing) ProviderKeyForRole(role string) string {
	if key, ok := r.Roles[role]; ok {
		return key
	}
	return r.Roles[RoleDefault]
}

// FallbackProviderKey 返回全局备胎 provider key（保留 role RoleFallback）；未配置返回空。
func (r Routing) FallbackProviderKey() string {
	return r.Roles[RoleFallback]
}
