// Package llmcfg 是 LLM 配置（provider 部署 / 复杂度路由）的持久化层。
// 两表对应 migration：llm_provider / llm_role_route。
//
// 事实源是 DB（前端「模型」模块 CRUD 改这里）；yaml 仅首次 insert-only 种子。
// 运行期消费方经 internal/llmstore（多级缓存）读，不直连本包 Store（跨进程一致性）。
//
// 路由模型：agent-role → complexity → provider 两跳。
//   - agent → complexity 的绑定可配置在 DB（agent.complexity），代码内有兜底映射（AgentComplexity）；
//   - complexity → provider 的绑定落 DB（llm_role_route 的 role 列存 complexity 名 simple/medium/complex）。
//
// 未显式归档的 role 落 ComplexityMedium（隐式默认档）；retry 耗尽切换到保留 role RoleFallback。
//
// 为何需要中间层：complexity 是**语义分档**（快速/标准/深度），多个 agent 天然共享同一档，
// 「换一个复杂档位的模型」只改该档配置一处即全量跟随。agent→complexity 可配置实现精细成本控制。
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
//
// API Key 双路径（migration 0103）：EncryptedAPIKey 非空时是当前事实源（前端直填 → 后端
// AES-256-GCM 加密落库）；为空时回退 APIKeyEnv 指向的环境变量（旧数据兼容，见 ResolveAPIKey）。
type Provider struct {
	Key             string
	Type            string
	BaseURL         string
	DefaultModel    string
	APIKeyEnv       string // 旧模式：ENV 变量名；仅当 EncryptedAPIKey 为空时生效
	EncryptedAPIKey []byte // AES-256-GCM 密文（nonce||ciphertext）；当前事实源
	APIKeyLast4     string // 明文末 4 位（脱敏辨识用，非密钥值）；旧行或仅 ENV 回退时为空
	MaxTokens       int
	SupportsTools   bool
	SupportsVision  bool
	ContextWindow   int
	Description     string
	SortOrder       int
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// HasStoredKey 报告该 provider 是否已有可用密钥来源（加密落库或 ENV 变量名），
// 供前端「是否已设置」提示——绝不透出密钥值本身。
func (p Provider) HasStoredKey() bool {
	return len(p.EncryptedAPIKey) > 0 || p.APIKeyEnv != ""
}

// ProviderParams 是 Provider 的 upsert 入参（Key 是稳定引用键，Update 按 Key 定位）。
//
// llmcfg 包本身不认识加密——明文 API Key 由 internal/httpapi 在落到本包之前用 cryptx 加密好，
// 这里只收密文字节。KeepExistingKey=true 时 Update 不改动 encrypted_api_key 列（前端编辑
// 表单不重新填密钥即保留原值，符合「已设置」不回显明文的交互）。
type ProviderParams struct {
	Key             string
	Type            string
	BaseURL         string
	DefaultModel    string
	LegacyAPIKeyEnv string // 旧模式：ENV 变量名，仅 seed（yaml 首填）路径使用
	EncryptedAPIKey []byte // 已加密的密文；nil 且 KeepExistingKey=false 时清空密钥
	APIKeyLast4     string // 明文末 4 位；随 EncryptedAPIKey 一起写（KeepExistingKey 时同被 COALESCE 保留）
	KeepExistingKey bool   // true：Update 时不改动已存密钥列（忽略 EncryptedAPIKey/APIKeyLast4）
	MaxTokens       int
	SupportsTools   bool
	SupportsVision  bool
	ContextWindow   int
	Description     string
	SortOrder       int
	Enabled         bool
}

// RoleRoute 是「档/保留槽 → provider key」的一行绑定（heavy→qwen、__fallback__→… 等）。
// Role 列存 tier 名（heavy/vision/light）或保留 role（__fallback__）；两者同表同解析。
type RoleRoute struct {
	Role        string
	ProviderKey string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Routing 是路由全景快照：全部「档/保留槽 → provider key」映射（三档 + __fallback__）。
// 运行期解析（agent-role → tier → provider key）一次性读齐，避免多次 DB 往返。
type Routing struct {
	Roles map[string]string // tier(heavy/vision/light) / 保留 role(__fallback__) → provider key
}

// 复杂度分档（complexity）：agent-role 按推理复杂度需求归入三档，路由的实际单元是档而非 agent。
//   - ComplexitySimple  ：快速响应，简单任务（如信息提取、格式化、简单验证）
//   - ComplexityMedium  ：标准推理，常规任务（如漏洞检测、工具调用、常规分析）。**隐式默认档**——未显式归档的 role 落此。
//   - ComplexityComplex ：深度推理，复杂决策（如战略规划、多步分析、多模态处理）
//
// complexity 名直接作为 llm_role_route.role 列的值持久化（DB schema 不区分「档」与「role」，共用一张表）。
const (
	ComplexitySimple  = "simple"
	ComplexityMedium  = "medium"
	ComplexityComplex = "complex"
)

// RoleFallback 是唯一保留 role：不对应任何真实 agent，是**全局**备胎语义槽——
// primary retry 耗尽后切换（等价旧 fallback 别名 / fallback_provider）。
// 用 __双下划线__ 前后缀与 complexity 名（simple/medium/complex）隔离，前端「复杂度分档」单列为「全局备胎」。
//
// 注：ComplexityMedium 即隐式默认档，未命中的 role 落 medium，无需独立兜底槽。
const RoleFallback = "__fallback__"

// agentComplexityTable 是 role → complexity 的**代码内置兜底表**（DB 未配 complexity 的 role 落此）。
// 未在表中的 role 落 ComplexityMedium（隐式默认档，见 AgentComplexity）。
//
// 两类 key 混在此表：
//   - agent（DB 有配置行，用户可在「智能体」页改 complexity，DB 优先于本表）：
//     planner / exploitation：active 链路需 browser-use 截图 + 深度规划 → complex
//     traffic-analysis      ：passive 流量逐批挖洞，标准推理 → medium
//   - 轻量路由 key（非 agent，无 DB 行、无 UI，仅借名路由到 simple 档）：
//     inspector：意图分类 + attack-graph 摘要复用（cmd/api）
//     compactor：ReAct 会话历史压缩（cmd/runner）
var agentComplexityTable = map[string]string{
	"planner":          ComplexityComplex,
	"exploitation":     ComplexityComplex,
	"traffic-analysis": ComplexityMedium,
	"inspector":        ComplexitySimple,
	"compactor":        ComplexitySimple,
}

// AgentComplexity 把 agent-role 映射到复杂度档位；未归档的 role 一律落 ComplexityMedium（隐式默认档）。
func AgentComplexity(role string) string {
	if complexity, ok := agentComplexityTable[role]; ok {
		return complexity
	}
	return ComplexityMedium
}

// ProviderKeyForRole 把 agent-role 两跳解析到 provider key：role → complexity → provider。
// complexity 未在 DB 配置（该档无绑定）时进一步回落 ComplexityMedium 档；medium 亦缺失 → 空
// （调用方据此报错，不静默兜底到任意 provider）。
func (r Routing) ProviderKeyForRole(role string) string {
	return r.ProviderKeyForComplexity(AgentComplexity(role))
}

// ProviderKeyForComplexity 完成第二跳 complexity → provider key（供调用方已自行确定 complexity 时用，
// 如 agent.complexity 覆盖了代码内置 AgentComplexity 的场景）。complexity 未配置回落 ComplexityMedium；
// medium 亦缺失 → 空（调用方据此报错，不静默兜底）。
func (r Routing) ProviderKeyForComplexity(complexity string) string {
	if key, ok := r.Roles[complexity]; ok && key != "" {
		return key
	}
	return r.Roles[ComplexityMedium]
}

// FallbackProviderKey 返回全局备胎 provider key（保留 role RoleFallback）；未配置返回空。
func (r Routing) FallbackProviderKey() string {
	return r.Roles[RoleFallback]
}
