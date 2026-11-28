// Package llmconfig 是模型配置（provider / 复杂度路由）的持久化层。
// 两表对应 migration：llm_provider / llm_role_route。
//
// 事实源是 DB（前端「模型」页改这里），配置文件不涉及 LLM 接入。
// 运行期消费方经 internal/llmstore（多级缓存）读，不直连本包 Store（跨进程一致性）。
//
// 路由模型：调用方 → 档位 → provider 两跳。
//   - agent 的档位取其生效配置的 complexity；api 侧非 agent 调用者经 RoleComplexity 归档；
//   - 档位 → provider 的绑定落 DB（llm_role_route 的 role 列存档名 simple/medium/complex），
//     缺档或档位 provider 不可用时由 framework/llm.Router 就近降级到相邻档。
//
// 为何需要中间层：complexity 是**语义分档**（快速/标准/深度），多个 agent 天然共享同一档，
// 「换一个复杂档位的模型」只改该档配置一处即全量跟随。agent→complexity 可配置实现精细成本控制。
//
// 安全：provider 密钥只以 AES-256-GCM 密文落库（加密在 httpapi 层完成），明文永不落库。
package llmconfig

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	fwllm "github.com/V3teran/liusha/internal/framework/llm"
)

// 协议、鉴权方式与输出上限字段的取值是 wire 契约，唯一的出处在框架层（framework/llm）——
// 库表 CHECK、HTTP 契约与前端都以它为准。这里只是取用，不再各抄一份（各抄一份时协议改名
// 要改两处，漏一处就对不上）。

// Provider type 常量（与 llm_provider.type CHECK 一致）。
const (
	ProviderTypeOpenAIChatCompletions = fwllm.ProviderTypeOpenAIChatCompletions
	ProviderTypeOpenAIResponses       = fwllm.ProviderTypeOpenAIResponses
	ProviderTypeAnthropicMessages     = fwllm.ProviderTypeAnthropicMessages
)

// ProviderTypes 是全部协议取值，校验提示与校验本身共用这一个出处。
var ProviderTypes = fwllm.ProviderTypes

// ProtocolLabel 是协议的展示名（厂商 + 接口名），界面文案与提示共用这一个出处。
func ProtocolLabel(protocol string) string {
	switch protocol {
	case ProviderTypeOpenAIChatCompletions:
		return "OpenAI Chat Completions"
	case ProviderTypeOpenAIResponses:
		return "OpenAI Responses"
	case ProviderTypeAnthropicMessages:
		return "Anthropic Messages"
	}
	return protocol
}

// 鉴权方式：密钥的携带方式，与协议正交，由测试连接实测后记录。
const (
	AuthBearer  = string(fwllm.AuthBearer)  // Authorization: Bearer <key>（OpenAI 协议的标准方式）
	AuthXAPIKey = string(fwllm.AuthXAPIKey) // x-api-key: <key>（Anthropic 协议的标准方式）
)

// DefaultAuthScheme 返回协议的标准鉴权方式。
func DefaultAuthScheme(protocol string) string {
	if protocol == ProviderTypeAnthropicMessages {
		return AuthXAPIKey
	}
	return AuthBearer
}

// 输出上限字段：OpenAI Chat Completions 协议下多数服务认 max_tokens，部分推理模型只认
// max_completion_tokens。
const (
	OutputLimitMaxTokens           = string(fwllm.OutputLimitMaxTokens)
	OutputLimitMaxCompletionTokens = string(fwllm.OutputLimitMaxCompletionTokens)
	OutputLimitMaxOutputTokens     = string(fwllm.OutputLimitMaxOutputTokens)
)

// Provider 是一个 LLM provider（连接参数 + 能力标志）。SupportsVision/ContextWindow
// 在 DB 为 NOT NULL（schema 层强制），故用非指针类型，无需区分「未填」。
//
// API Key 在前端填写、AES-256-GCM 加密落库（见 ResolveAPIKey）。
type Provider struct {
	Code             string // 稳定引用标识（角色路由 FK 指向；列名 code）
	Type             string
	BaseURL          string
	DefaultModel     string
	EncryptedAPIKey  []byte // AES-256-GCM 密文（nonce||ciphertext）；空 = 尚未设置密钥
	APIKeyLast4      string // 明文末 4 位（脱敏辨识用，非密钥值）
	AuthScheme       string // 鉴权方式（AuthBearer / AuthXAPIKey）
	OutputLimitParam string // 输出上限字段（max_tokens / max_completion_tokens / max_output_tokens，按协议落定）
	MaxTokens        int
	SupportsTools    bool
	SupportsVision   bool
	ContextWindow    int
	DisplayName      string // 显示名称：界面上唯一可读的标识（code 是 UUID）；必填、不区分大小写唯一，可随时改
	Description      string
	SortOrder        int
	Enabled          bool
	// ReasoningEffort 是推理模型的思考深度（low / medium / high / max）；空 = 没设置，
	// 不下发这个参数、按模型自己的默认。各协议按自己的字段下发。
	ReasoningEffort string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// HasKey 报告该 provider 是否已设置密钥（与 ResolveAPIKey 的判定同口径，只是不解密）。
// 供前端「密钥是否就绪」提示，绝不透出密钥值本身。
func (p Provider) HasKey() bool {
	return len(p.EncryptedAPIKey) > 0
}

// ErrInvalid 标记不合法的 provider 配置；HTTP 层收敛为 400。
var ErrInvalid = errors.New("模型配置不合法")

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
// 协议规则归一（见 NormalizeBaseURL），鉴权方式缺省取协议标准方式，输出上限字段按协议落定
// （只有一个字段的协议随协议定；OpenAI Chat Completions 由实测决定，未记录时按多数服务认的
// max_tokens），没填的上下文窗口与
// 单次最大输出落成缺省值。缺省在入口落定：落库的行恒为正数，库里没有「未设置」的行，
// 各级缓存读到的都是同一个确定的值。
func (p *ProviderParams) Normalize() {
	p.BaseURL = NormalizeBaseURL(p.Type, p.BaseURL)
	p.DefaultModel = strings.TrimSpace(p.DefaultModel)
	p.DisplayName = strings.TrimSpace(p.DisplayName)
	if p.AuthScheme == "" {
		p.AuthScheme = DefaultAuthScheme(p.Type)
	}
	p.OutputLimitParam = string(fwllm.OutputLimitParamOf(p.Type, fwllm.OutputLimitParam(p.OutputLimitParam)))
	if p.ContextWindow == 0 {
		p.ContextWindow = DefaultContextWindow
	}
	if p.MaxTokens == 0 {
		p.MaxTokens = DefaultMaxTokens
	}
}

// operationSuffixes 是用户常把完整端点粘进 Base URL 时带上的操作路径——它们由适配器
// 自行拼接，任何协议下都不属于 Base URL。
var operationSuffixes = []string{"/chat/completions", "/completions", "/responses", "/messages", "/models"}

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
	if protocol == ProviderTypeAnthropicMessages && strings.HasSuffix(strings.ToLower(path), "/v1") {
		path = path[:len(path)-len("/v1")]
	}
	u.Path = strings.TrimRight(path, "/")
	u.RawPath = ""
	return u.String()
}

// Validate 校验 provider 配置——HTTP 写入与 store 落库共用的唯一口径：协议类型、绝对 http(s)
// Base URL、显示名称、默认模型、上下文窗口、输出上限、鉴权方式、输出上限字段与思考深度。
func (p ProviderParams) Validate() error {
	if !slices.Contains(ProviderTypes, p.Type) {
		return invalidf("非法 provider 协议 %q（应为 %s）", p.Type, strings.Join(ProviderTypes, " 或 "))
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
	if want := fwllm.OutputLimitParamOf(p.Type, fwllm.OutputLimitParam(p.OutputLimitParam)); p.OutputLimitParam != string(want) {
		return invalidf("协议 %s 的输出上限字段应为 %s，实际 %q", ProtocolLabel(p.Type), want, p.OutputLimitParam)
	}
	if p.MaxTokens <= 0 {
		return invalidf("单次最大输出 tokens 必须大于 0")
	}
	if err := ReasoningEffortError(p.ReasoningEffort); err != nil {
		return err
	}
	return nil
}

// ReasoningEffortError 校验思考深度的取值：空（不设置）或档位表里的一个。provider 保存与
// 测试连接的入口共用。
func ReasoningEffortError(effort string) error {
	if !fwllm.ReasoningEffort(effort).Valid() {
		return invalidf("非法思考深度 %q（应为 %s 之一或不设置）", effort, strings.Join(fwllm.ReasoningEffortNames(), " / "))
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
var ErrConflict = errors.New("模型配置冲突")

// DefaultMaxTokens 与 DefaultContextWindow 是 provider 没填上下文窗口、单次最大输出时的缺省，
// 也是全系统唯一的出处：表单初值、库列缺省（迁移 0266）、这里落定缺省值，三处同值——
// 库列缺省由 internal/dbtest 的迁移测试与本常量对照。
//
// 取当前旗舰模型的官方上限：上下文 1M、单次输出 128K（OpenAI / Anthropic / GLM / Qwen 旗舰的
// 共同上限，DeepSeek 的 384K 是个例，按它做缺省会被其余全部拒绝）。它们只是上限，不多花钱。
// 此前的 16384 偏小：规划者一次输出的思考加 JSON 可达 4k–8k token，任务变大后更高；实测在
// 16384 上仍有约 0.5% 的输出顶到上限。上限低于缺省的模型（部分模型是 64K 或 32K），上游拒绝
// 输出上限时运行期自动下调并记住（framework/llm 的 ClientPool）。
const (
	DefaultMaxTokens     = 131072
	DefaultContextWindow = 1048576
)

// ProviderParams 是 Provider 的 upsert 入参（Key 是稳定引用键，Update 按 Key 定位）。
//
// llmconfig 包本身不认识加密——明文 API Key 由 internal/httpapi 在落到本包之前用 cryptx 加密好，
// 这里只收密文字节。KeepExistingKey=true 时 Update 不改动 encrypted_api_key 列（前端编辑
// 表单不重新填密钥即保留原值，符合「已设置」不回显明文的交互）。
type ProviderParams struct {
	Code             string // 稳定引用标识（角色路由 FK 指向；列名 code）
	Type             string
	BaseURL          string
	DefaultModel     string
	EncryptedAPIKey  []byte // 已加密的密文；nil 且 KeepExistingKey=false 时清空密钥
	APIKeyLast4      string // 明文末 4 位；随 EncryptedAPIKey 一起写（KeepExistingKey 时同被 COALESCE 保留）
	KeepExistingKey  bool   // true：Update 时不改动已存密钥列（忽略 EncryptedAPIKey/APIKeyLast4）
	AuthScheme       string // 鉴权方式；空 = 协议标准方式（Normalize 填充）
	OutputLimitParam string // 输出上限字段；Normalize 按协议落定（Chat Completions 未记录时按 max_tokens）
	DisplayName      string // 显示名称：必填、不区分大小写唯一（见 Provider.DisplayName）
	MaxTokens        int
	SupportsTools    bool
	SupportsVision   bool
	ContextWindow    int
	Description      string
	SortOrder        int
	Enabled          bool
	ReasoningEffort  string
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

// roleComplexityTable 是非 agent 的 LLM 调用者 → 档位。agent 的档位取自其生效配置的
// complexity，由认知循环直传 Router，不经此表。
//   - inspector：api 进程的意图分类、会话标题、闲聊与问答（dispatch.RoleRouter）
var roleComplexityTable = map[string]string{
	"inspector": ComplexitySimple,
}

// RoleComplexity 把非 agent 的调用者映射到档位；未列出的调用者落 ComplexityMedium（隐式默认档）。
func RoleComplexity(role string) string {
	if complexity, ok := roleComplexityTable[role]; ok {
		return complexity
	}
	return ComplexityMedium
}
