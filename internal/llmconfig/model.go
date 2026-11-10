// Package llmconfig 是 LLM 配置（provider 部署 / 复杂度路由）的持久化层。
// 两表对应 migration：llm_provider / llm_role_route。
//
// 事实源是 DB（前端「模型」模块 CRUD 改这里）；yaml 仅首次 insert-only 种子。
// 运行期消费方经 internal/llmstore（多级缓存）读，不直连本包 Store（跨进程一致性）。
//
// 路由模型：agent-role → complexity → provider 两跳。
//   - agent → complexity 的绑定可配置在 DB（agent.complexity），代码内有兜底映射（AgentComplexity）；
//   - complexity → provider 的绑定落 DB（llm_role_route 的 role 列存 complexity 名 simple/medium/complex）。
//
// 未显式归档的 role 落 ComplexityMedium（隐式默认档）。无任何兜底机制：请求重试耗尽即失败。
//
// 为何需要中间层：complexity 是**语义分档**（快速/标准/深度），多个 agent 天然共享同一档，
// 「换一个复杂档位的模型」只改该档配置一处即全量跟随。agent→complexity 可配置实现精细成本控制。
//
// 安全：APIKeyEnv 只存**环境变量名**，密钥值永不落库。
package llmconfig

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/constants"
)

// Provider type 常量（与 llm_provider.type CHECK 一致）。
const (
	ProviderTypeOpenAICompat = "openai_compat"
	ProviderTypeAnthropic    = "anthropic"
)

// 鉴权方式（与 llm_provider.auth_scheme CHECK、framework/llm.AuthScheme 一致）：
// 密钥的携带方式，与协议正交，由连接诊断实测后记录。
const (
	AuthBearer  = "bearer"    // Authorization: Bearer <key>（OpenAI 兼容协议的标准方式）
	AuthXAPIKey = "x-api-key" // x-api-key: <key>（Anthropic 协议的标准方式）
)

// DefaultAuthScheme 返回协议的标准鉴权方式。
func DefaultAuthScheme(protocol string) string {
	if protocol == ProviderTypeAnthropic {
		return AuthXAPIKey
	}
	return AuthBearer
}

// 输出上限字段（与 llm_provider.output_limit_param CHECK、framework/llm.OutputLimitParam 一致）：
// OpenAI 兼容协议下多数服务认 max_tokens，部分推理模型只认 max_completion_tokens。
const (
	OutputLimitMaxTokens           = "max_tokens"
	OutputLimitMaxCompletionTokens = "max_completion_tokens"
)

// Provider 是一个 LLM provider 部署（连接参数 + 能力标志）。
// 对应旧 config.ProviderConfig，但 SupportsVision/ContextWindow 落 DB 为 NOT NULL 非指针
// （建表 CHECK + NOT NULL 已在 schema 层强制，无需 *bool/*int 区分「未填」）。
//
// API Key 双路径（migration 0103）：EncryptedAPIKey 非空时是当前事实源（前端直填 → 后端
// AES-256-GCM 加密落库）；为空时回退 APIKeyEnv 指向的环境变量（旧数据兼容，见 ResolveAPIKey）。
type Provider struct {
	Code             string // 稳定引用标识（角色路由 FK 指向；列名 code）
	Type             string
	BaseURL          string
	DefaultModel     string
	APIKeyEnv        string // 旧模式：ENV 变量名；仅当 EncryptedAPIKey 为空时生效
	EncryptedAPIKey  []byte // AES-256-GCM 密文（nonce||ciphertext）；当前事实源
	APIKeyLast4      string // 明文末 4 位（脱敏辨识用，非密钥值）；旧行或仅 ENV 回退时为空
	AuthScheme       string // 鉴权方式（AuthBearer / AuthXAPIKey）
	OutputLimitParam string // 输出上限字段（OutputLimitMaxTokens / OutputLimitMaxCompletionTokens）
	MaxTokens        int
	SupportsTools    bool
	SupportsVision   bool
	ContextWindow    int
	DisplayName      string // 显示名称：界面上唯一可读的标识（code 是 UUID）；必填、不区分大小写唯一，可随时改
	Description      string
	SortOrder        int
	Enabled          bool
	// ThinkingBudgetTokens 是扩展思考预算（anthropic thinking.budget_tokens）。
	// >0 = 开启扩展思考；0 = 不开启。仅 anthropic 协议消费。
	ThinkingBudgetTokens int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// HasKey 报告该 provider 当前能否解析出密钥：已加密落库，或 api_key_env 指向的
// 环境变量在本进程有值（与 ResolveAPIKey 的判定同口径，只是不解密）。供前端
// 「密钥是否就绪」提示——只填了变量名而变量为空时同样视为缺失，绝不透出密钥值本身。
func (p Provider) HasKey() bool {
	return len(p.EncryptedAPIKey) > 0 || (p.APIKeyEnv != "" && os.Getenv(p.APIKeyEnv) != "")
}

// ErrInvalid 标记不合法的 provider 配置；HTTP 层收敛为 400。
var ErrInvalid = errors.New("llm 配置不合法")

// classifiedError 是带分类哨兵的错误：Error() 只给作者写的中文原文（直接面向
// 操作者），哨兵经 Unwrap 供 errors.Is 分类——分类信息不混进文案。
type classifiedError struct {
	msg  string
	kind error
}

func (e *classifiedError) Error() string { return e.msg }
func (e *classifiedError) Unwrap() error { return e.kind }

// invalidf 构造归类为 ErrInvalid 的错误。
func invalidf(format string, args ...any) error {
	return &classifiedError{msg: fmt.Sprintf(format, args...), kind: ErrInvalid}
}

// conflictf 构造归类为 ErrConflict 的错误。
func conflictf(format string, args ...any) error {
	return &classifiedError{msg: fmt.Sprintf(format, args...), kind: ErrConflict}
}

// Normalize 把配置归一为规范形态（HTTP 写入、启动种子、落库共用，幂等）：Base URL 按
// 协议规则归一（见 NormalizeBaseURL），鉴权方式缺省取协议标准方式，输出上限字段缺省
// max_tokens（anthropic 协议恒为 max_tokens——该协议只有这一个字段）。
func (p *ProviderParams) Normalize() {
	p.BaseURL = NormalizeBaseURL(p.Type, p.BaseURL)
	p.DefaultModel = strings.TrimSpace(p.DefaultModel)
	p.DisplayName = strings.TrimSpace(p.DisplayName)
	if p.AuthScheme == "" {
		p.AuthScheme = DefaultAuthScheme(p.Type)
	}
	if p.OutputLimitParam == "" || p.Type == ProviderTypeAnthropic {
		p.OutputLimitParam = OutputLimitMaxTokens
	}
}

// operationSuffixes 是用户常把完整端点粘进 Base URL 时带上的操作路径——它们由 SDK
// 自行拼接，任何协议下都不属于 Base URL。
var operationSuffixes = []string{"/chat/completions", "/completions", "/messages", "/models"}

// NormalizeBaseURL 按协议规则归一 Base URL：去首尾空白与末尾斜杠、去掉误粘的操作路径；
// Anthropic 协议再去掉末尾的 /v1（SDK 自行拼 /v1/messages）。规则只由协议规范推出，
// 与具体服务无关；无法解析的地址原样返回，交给 Validate 报错。
func NormalizeBaseURL(protocol, raw string) string {
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		return trimmed
	}
	path := strings.TrimRight(u.Path, "/")
	for _, suffix := range operationSuffixes {
		if strings.HasSuffix(strings.ToLower(path), suffix) {
			path = path[:len(path)-len(suffix)]
			break
		}
	}
	if protocol == ProviderTypeAnthropic && strings.HasSuffix(strings.ToLower(path), "/v1") {
		path = path[:len(path)-len("/v1")]
	}
	u.Path = strings.TrimRight(path, "/")
	u.RawPath = ""
	return u.String()
}

// Validate 校验 provider 配置——HTTP 写入与 store 落库共用的唯一口径：
// 协议类型、绝对 http(s) Base URL、显示名称、默认模型、上下文窗口、输出上限，以及扩展思考
// 预算的协议约束（仅 anthropic；开启时 ≥ MinThinkingBudgetTokens 且小于实际 max_tokens——
// 运行期构造 client 时同样校验，不合法的部署会让整档不可用）。
func (p ProviderParams) Validate() error {
	switch p.Type {
	case ProviderTypeOpenAICompat, ProviderTypeAnthropic:
	default:
		return invalidf("非法 provider 协议 %q（应为 openai_compat 或 anthropic）", p.Type)
	}
	if err := ValidateBaseURL(p.BaseURL); err != nil {
		return err
	}
	if strings.TrimSpace(p.DisplayName) == "" {
		return invalidf("显示名称不能为空")
	}
	if strings.TrimSpace(p.DefaultModel) == "" {
		return invalidf("默认模型不能为空")
	}
	if p.ContextWindow <= 0 {
		return invalidf("上下文窗口必须大于 0")
	}
	if p.AuthScheme != AuthBearer && p.AuthScheme != AuthXAPIKey {
		return invalidf("非法鉴权方式 %q（应为 bearer 或 x-api-key）", p.AuthScheme)
	}
	if p.OutputLimitParam != OutputLimitMaxTokens && p.OutputLimitParam != OutputLimitMaxCompletionTokens {
		return invalidf("非法输出上限字段 %q（应为 max_tokens 或 max_completion_tokens）", p.OutputLimitParam)
	}
	if p.MaxTokens < 0 {
		return invalidf("单次最大输出 tokens 不能为负")
	}
	if p.ThinkingBudgetTokens < 0 {
		return invalidf("扩展思考预算不能为负")
	}
	if p.ThinkingBudgetTokens > 0 {
		if p.Type != ProviderTypeAnthropic {
			return invalidf("扩展思考预算仅适用于 Anthropic 协议")
		}
		if p.ThinkingBudgetTokens < MinThinkingBudgetTokens {
			return invalidf("扩展思考预算开启时至少 %d tokens", MinThinkingBudgetTokens)
		}
		if p.ThinkingBudgetTokens >= int64(p.EffectiveMaxTokens()) {
			return invalidf("扩展思考预算（%d）必须小于单次最大输出 tokens（%d）", p.ThinkingBudgetTokens, p.EffectiveMaxTokens())
		}
	}
	return nil
}

// ValidateBaseURL 校验 Base URL 是带主机名的绝对 http(s) 地址。
func ValidateBaseURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return invalidf("Base URL 必须是 http(s) 开头的完整地址（如 https://api.example.com/v1）")
	}
	return nil
}

// ErrConflict 标记与既有状态冲突的写操作（code 已存在、无环境变量可回落时清除
// 已存密钥等）；HTTP 层收敛为 409。
var ErrConflict = errors.New("llm 配置冲突")

// 扩展思考预算的协议约束（anthropic thinking.budget_tokens）：开启时至少 1024，
// 且必须小于单次最大输出 tokens（max_tokens 为 0 时运行期按 4096 发送）。
const (
	MinThinkingBudgetTokens = 1024
	DefaultMaxTokens        = 4096
)

// EffectiveMaxTokens 是运行期实际发送的单次最大输出 tokens（0 = 协议默认值）。
func (p ProviderParams) EffectiveMaxTokens() int {
	if p.MaxTokens > 0 {
		return p.MaxTokens
	}
	return DefaultMaxTokens
}

// ProviderParams 是 Provider 的 upsert 入参（Key 是稳定引用键，Update 按 Key 定位）。
//
// llmconfig 包本身不认识加密——明文 API Key 由 internal/httpapi 在落到本包之前用 cryptx 加密好，
// 这里只收密文字节。KeepExistingKey=true 时 Update 不改动 encrypted_api_key 列（前端编辑
// 表单不重新填密钥即保留原值，符合「已设置」不回显明文的交互）。
type ProviderParams struct {
	Code                 string // 稳定引用标识（角色路由 FK 指向；列名 code）
	Type                 string
	BaseURL              string
	DefaultModel         string
	APIKeyEnv            string // 旧模式：ENV 变量名，仅 seed（yaml 首填）路径使用
	EncryptedAPIKey      []byte // 已加密的密文；nil 且 KeepExistingKey=false 时清空密钥
	APIKeyLast4          string // 明文末 4 位；随 EncryptedAPIKey 一起写（KeepExistingKey 时同被 COALESCE 保留）
	KeepExistingKey      bool   // true：Update 时不改动已存密钥列（忽略 EncryptedAPIKey/APIKeyLast4）
	AuthScheme           string // 鉴权方式；空 = 协议标准方式（Normalize 填充）
	OutputLimitParam     string // 输出上限字段；空 = max_tokens（Normalize 填充；anthropic 恒为 max_tokens）
	DisplayName          string // 显示名称：必填、不区分大小写唯一（见 Provider.DisplayName）
	MaxTokens            int
	SupportsTools        bool
	SupportsVision       bool
	ContextWindow        int
	Description          string
	SortOrder            int
	Enabled              bool
	ThinkingBudgetTokens int64
}

// RoleRoute 是「档 → provider code」的一行绑定（complex→glm 等）。
// Role 列存复杂度档名（simple/medium/complex）。
type RoleRoute struct {
	Role         string
	ProviderCode string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Routing 是路由全景快照：全部「档 → provider code」映射（三档）。
// 运行期解析（role → 档 → provider code）一次性读齐，避免多次 DB 往返。
type Routing struct {
	Roles map[string]string // 档(simple/medium/complex) → provider code
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

// IsComplexity 判断 tier 是否是三档之一（路由只能绑定到这三档）。
func IsComplexity(tier string) bool {
	switch tier {
	case ComplexitySimple, ComplexityMedium, ComplexityComplex:
		return true
	}
	return false
}

// agentComplexityTable 是 role → complexity 的**代码内置兜底表**（DB 未配 complexity 的 role 落此）。
// 未在表中的 role 落 ComplexityMedium（隐式默认档，见 AgentComplexity）。
//
// 两类 key 混在此表：
//   - agent（DB 有配置行，用户可在「智能体」页改 complexity，DB 优先于本表）：
//     planner：认知循环规划链路需深度规划 → complex
//   - 轻量路由 key（非 agent，无 DB 行、无 UI，仅借名路由到 simple 档）：
//     inspector：意图分类 + 探索图摘要复用（cmd/api）
//     compactor：ReAct 会话历史压缩（cmd/runner）
var agentComplexityTable = map[string]string{
	constants.AgentCodePlanner: ComplexityComplex,
	"inspector":                ComplexitySimple,
	"compactor":                ComplexitySimple,
}

// AgentComplexity 把 agent-role 映射到复杂度档位；未归档的 role 一律落 ComplexityMedium（隐式默认档）。
func AgentComplexity(role string) string {
	if complexity, ok := agentComplexityTable[role]; ok {
		return complexity
	}
	return ComplexityMedium
}

// ProviderCodeForRole 把 agent-role 两跳解析到 provider code：role → complexity → provider。
// complexity 未在 DB 配置（该档无绑定）时进一步回落 ComplexityMedium 档；medium 亦缺失 → 空
// （调用方据此报错，不静默兜底到任意 provider）。
func (r Routing) ProviderCodeForRole(role string) string {
	return r.ProviderCodeForComplexity(AgentComplexity(role))
}

// ProviderCodeForComplexity 完成第二跳 complexity → provider code（供调用方已自行确定 complexity 时用，
// 如 agent.complexity 覆盖了代码内置 AgentComplexity 的场景）。complexity 未配置回落 ComplexityMedium；
// medium 亦缺失 → 空（调用方据此报错，不静默兜底）。
func (r Routing) ProviderCodeForComplexity(complexity string) string {
	if key, ok := r.Roles[complexity]; ok && key != "" {
		return key
	}
	return r.Roles[ComplexityMedium]
}
