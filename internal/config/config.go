// Package config 用 viper 加载 YAML 配置 + ENV 覆盖 + 启动校验。
//
// 设计原则：
//   - 所有可调参数统一收敛到本文件，避免散落各包的硬编码常量
//   - 每个 sub-struct 字段缺省值由 ApplyDefaults() 兜底，避免 yaml 缺字段时进程拒启动
//   - 仅 LLM provider key（tiers.heavy 等）做强制 validate（密钥读取必须有 provider 信息）
//   - ENV 覆盖前缀 LIUSHA_，二级用 _ 分隔（如 LIUSHA_LLM_FALLBACK）；注意 llm.tiers 是 map，
//     AutomaticEnv 不覆盖单个 map key，改档位 provider 走 yaml 或前端「能力分档」页
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config 是 liusha 顶层配置树。
type Config struct {
	API         APIConfig                 `mapstructure:"api"`
	Postgres    PostgresConfig            `mapstructure:"postgres"`
	Redis       RedisConfig               `mapstructure:"redis"`
	LLM         LLMConfig                 `mapstructure:"llm"`
	Providers   map[string]ProviderConfig `mapstructure:"providers"`
	Proxy       ProxyConfig               `mapstructure:"proxy"`
	Ingestor    IngestorConfig            `mapstructure:"ingestor"`
	Session     SessionConfig             `mapstructure:"session"`
	Credential  CredentialConfig          `mapstructure:"credential"`
	Skills      SkillsConfig              `mapstructure:"skills"`
	Agents     AgentsConfig             `mapstructure:"agents_dir"`
	Runner      RunnerConfig              `mapstructure:"runner"`
	Compaction  CompactionConfig          `mapstructure:"compaction"`
	Sandbox     SandboxConfig             `mapstructure:"sandbox"`
	Toolruntime ToolruntimeConfig         `mapstructure:"toolruntime"`
}

// APIConfig 是 cmd/api 的 HTTP 入口参数。
type APIConfig struct {
	ReadTimeoutSeconds       int    `mapstructure:"read_timeout_seconds"`
	WriteTimeoutSeconds      int    `mapstructure:"write_timeout_seconds"`
	ReadHeaderTimeoutSeconds int    `mapstructure:"read_header_timeout_seconds"` // 也用于 proxy/runner healthz HTTP server
	ListenAddr               string `mapstructure:"listen_addr"`
	ShutdownTimeoutSeconds   int    `mapstructure:"shutdown_timeout_seconds"`
}

// PostgresConfig 是 pgx pool 参数。
type PostgresConfig struct {
	MaxConns               int `mapstructure:"max_conns"`
	MinConns               int `mapstructure:"min_conns"`
	ConnectTimeoutSeconds  int `mapstructure:"connect_timeout_seconds"`
	MaxConnLifetimeSeconds int `mapstructure:"max_conn_lifetime_seconds"`
}

// RedisConfig 是 go-redis client pool 参数（addr 仍由 ENV LIUSHA_REDIS_ADDR 提供）。
//
// 字段为 0 时透传到 go-redis 让其内部默认生效（SDK 默认：PoolSize=10×GOMAXPROCS,
// DialTimeout=5s, ReadTimeout=3s, WriteTimeout=3s）。本配置主要给共享 redis 多服务
// 部署时调小 PoolSize / 调大超时用。
type RedisConfig struct {
	PoolSize            int `mapstructure:"pool_size"`
	MinIdleConns        int `mapstructure:"min_idle_conns"`
	DialTimeoutSeconds  int `mapstructure:"dial_timeout_seconds"`
	ReadTimeoutSeconds  int `mapstructure:"read_timeout_seconds"`
	WriteTimeoutSeconds int `mapstructure:"write_timeout_seconds"`
}

// LLMConfig 是 LLM 能力分档路由 + retry 参数 + llm_invocation 异步 batch 写入参数。
//
// 能力分档（tier）取代旧的 default/light/vision 四槽 + per-agent 摊平路由：
// agent → tier 的绑定固定在代码里（见 llmcfg.AgentTier，运行期不可改）；
// tier → provider 的绑定落 DB（前端「能力分档」页可配），此处 yaml 仅首次 insert-only 种子。
// 三档：complex（深度推理）/ medium（标准推理）/ simple（快速响应）。
type LLMConfig struct {
	Tiers    map[string]string `mapstructure:"tiers"`    // tier(heavy/vision/light) → provider key
	Fallback string            `mapstructure:"fallback"` // 主 provider 重试耗尽后的全局备胎 provider key

	Retry      RetryConfig      `mapstructure:"retry"`
	Invocation InvocationConfig `mapstructure:"invocation"`
}

// RetryConfig 控制 4 类错误的重试次数和 backoff schedule（spec §8.5）。
//
// 默认（DefaultLLMRetry）：
//   - 429 重试 3 次 1s/4s/16s 指数退避，耗尽切 fallback
//   - 529 重试 1 次（立即），耗尽切 fallback
//   - 5xx 重试 2 次 1s/4s，不切 fallback
//   - 网络超时重试 2 次 1s/3s，不切 fallback
type RetryConfig struct {
	Max429             int   `mapstructure:"max_429"`
	Max529             int   `mapstructure:"max_529"`
	Max5xx             int   `mapstructure:"max_5xx"`
	MaxNet             int   `mapstructure:"max_net"`
	Backoff429Seconds  []int `mapstructure:"backoff_429_seconds"`
	Backoff5xxSeconds  []int `mapstructure:"backoff_5xx_seconds"`
	BackoffNetSeconds  []int `mapstructure:"backoff_net_seconds"`
	Backoff529Millisec int   `mapstructure:"backoff_529_ms"`
}

// InvocationConfig 是 llm_invocation 异步 batch 写入参数。
type InvocationConfig struct {
	BufferSize       int `mapstructure:"buffer_size"`
	BatchSize        int `mapstructure:"batch_size"`
	FlushIntervalMs  int `mapstructure:"flush_interval_ms"`
	InsertTimeoutSec int `mapstructure:"insert_timeout_sec"`
}

// ProviderConfig 一个 LLM provider 的连接参数。
//
//	Type 取值:
//	  "openai_compat" 走 OpenAI 协议族；
//	  "anthropic"     走 Anthropic 原生 /v1/messages；
//	  ""              视为 openai_compat（向后兼容）。
type ProviderConfig struct {
	Type          string `mapstructure:"type"`
	BaseURL       string `mapstructure:"base_url"`
	DefaultModel  string `mapstructure:"default_model"`
	VisionModel   string `mapstructure:"vision_model"`
	APIKeyEnv     string `mapstructure:"api_key_env"`
	MaxTokens     int    `mapstructure:"max_tokens"`
	SupportsTools bool   `mapstructure:"supports_tools"`
	// SupportsVision 用 *bool 区分"未填"（nil）与"显式 false"——validate 强制 yaml 必填，
	// 避免 caller 不知道 provider 能不能 vision 时拿默认值踩坑（例如 deepseek 不支持 vision
	// 却收到含图 message → 服务端 400）。yaml `supports_vision: true/false` 都合法，留空启动报错。
	SupportsVision *bool `mapstructure:"supports_vision"`

	// ContextWindow 是 model 总上下文窗口（input + output 合计 tokens）。
	// 会话历史压缩按此值算阈值（trigger_ratio × ContextWindow）。
	// 同 SupportsVision 模式：*int 区分"未填"（nil）与"显式 0"——validate 强制必填，
	// 避免 caller 用默认值估算导致 prompt 真爆（例如 32k 模型按 128k 算阈值）。
	ContextWindow *int `mapstructure:"context_window"`
}

// ProxyConfig 控制 in-process MITM 切片 + 责任链过滤 + 聚合参数 + 进程入口。
type ProxyConfig struct {
	WindowBatch             int      `mapstructure:"window_batch"`
	WindowMaxAgeSeconds     int      `mapstructure:"window_max_age_seconds"`
	AllowHosts              []string `mapstructure:"allow_hosts"`
	ExcludeMethods          []string `mapstructure:"exclude_methods"`
	ExcludeHosts            []string `mapstructure:"exclude_hosts"`
	ExcludeUpgradeProtocols []string `mapstructure:"exclude_upgrade_protocols"`
	ExcludeSuffixes         []string `mapstructure:"exclude_suffixes"`
	ExcludeContentTypes     []string `mapstructure:"exclude_content_types"`
	ExcludeStatusCodes      []int    `mapstructure:"exclude_status_codes"`
	MaxRequestBodySize      int      `mapstructure:"max_request_body_size"`
	MaxResponseBodySize     int      `mapstructure:"max_response_body_size"`

	// 进程入口（cmd/proxy）：纯 MITM passive 入口。存活检测探 TCP 8888，无独立 healthz HTTP。
	// active 抓流量 ingest endpoint 已迁到 cmd/runner（沙箱回连 runner :9090）。
	ListenAddr             string `mapstructure:"listen_addr"`   // 0.0.0.0:8888 公开端口（sanitizer 接 raw TCP）
	InternalAddr           string `mapstructure:"internal_addr"` // 127.0.0.1:18888 proxify loopback（sanitizer 转发到这）
	CertSubdir             string `mapstructure:"cert_subdir"`
	ShutdownTimeoutSeconds int    `mapstructure:"shutdown_timeout_seconds"`

	// Redis Stream（proxy → ingestor 之间）
	StreamName   string `mapstructure:"stream_name"`
	StreamMaxLen int    `mapstructure:"stream_max_len"`

	// IngestToken：active 容器内 browser-svc.py CDP Network 抓 chromium 流量 →
	// /internal/v1/flows/ingest endpoint 的 Bearer token。空 = 不强制验证（开发模式，
	// 仅靠 bind 127.0.0.1 + docker bridge 网络隔离）。
	// 生产建议通过 ENV LIUSHA_INGEST_TOKEN 注入，三个进程（cmd/proxy + cmd/runner + sandbox）共享同一值。
	IngestToken string `mapstructure:"ingest_token"`
}

// IngestorConfig 是 Stream 流量摄入器（cmd/runner 内 goroutine）参数。
type IngestorConfig struct {
	ConsumerGroup        string `mapstructure:"consumer_group"`
	ConsumerName         string `mapstructure:"consumer_name"`
	ReadBatch            int    `mapstructure:"read_batch"`
	ReadBlockTimeoutMs   int    `mapstructure:"read_block_timeout_ms"`
	RetryDelayMs         int    `mapstructure:"retry_delay_ms"`
	RecreateGroupDelayMs int    `mapstructure:"recreate_group_delay_ms"`

	// passive 聚合器参数（§6.2）：同 host 攒批建 passive task。
	// AggregateBatchSize：一批多少条 proxy_traffic 触发建 task（默认 20；~一个用户操作单元的 XHR 量级）。
	// AggregateWindowSeconds：距首条超此秒数即触发（默认 10；先到先触发）。
	AggregateBatchSize     int `mapstructure:"aggregate_batch_size"`
	AggregateWindowSeconds int `mapstructure:"aggregate_window_seconds"`
}

// SessionConfig 是 agent prompt 上限参数。
// （合表后 passive task 是有界批分析、跑完即终态，无常驻监控会话，故原 sweeper/TTL 参数已删。）
type SessionConfig struct {
	// agent user prompt 拼装时的上限（避免 prompt 膨胀）。
	// FindingsLimitInPrompt：该 host 已有 finding 段的 DB 读上限安全闸（取够高，正常扫描全量注入；
	// 不在此 top-N 截断，prompt 超长由 ① summarization 统一压缩，agent 仍可 read_findings 取全）。
	// （原 lessons_limit_in_prompt 已删：跨目标知识改 corpus PULL 检索，不再 PUSH 注入。）
	FindingsLimitInPrompt int `mapstructure:"findings_limit_in_prompt"`
}

// CredentialConfig 是 credential.RedisProvider 的 redis key 前缀。
type CredentialConfig struct {
	RedisKeyPrefix string `mapstructure:"redis_key_prefix"`
}

// SkillsConfig 是 SKILL.md 外部目录。
type SkillsConfig struct {
	Root string `mapstructure:"root"`
}

// ExecutorsConfig 是 executor 角色 markdown 外部目录（agents/*.md，planner + 杀伤链子代理）。
type AgentsConfig struct {
	Root string `mapstructure:"root"`
}

// RunnerConfig 是 cmd/runner 进程的运行时参数。
type RunnerConfig struct {
	SoloAgentRunTimeoutSeconds  int    `mapstructure:"solo_agent_run_timeout_seconds"`  // solo 引擎单个 agent task 整体超时（asynq handler 入口 WithTimeout）
	SwarmAgentRunTimeoutSeconds int    `mapstructure:"swarm_agent_run_timeout_seconds"` // swarm 引擎整体超时——站点扫描爬+测耗时长，独立配置（默认 4h，对齐 sandbox max lifetime）
	StepLLMTimeoutSeconds       int    `mapstructure:"step_llm_timeout_seconds"`
	AsynqConcurrency            int    `mapstructure:"asynq_concurrency"`
	AsynqShutdownTimeoutSeconds int    `mapstructure:"asynq_shutdown_timeout_seconds"` // asynq.Shutdown 等 in-flight task 完成的超时
	ShutdownTimeoutSeconds      int    `mapstructure:"shutdown_timeout_seconds"`
	HealthzAddr                 string `mapstructure:"healthz_addr"`
	TrafficMaxRequestBody       int    `mapstructure:"traffic_max_request_body"`
	TrafficMaxResponseBody      int    `mapstructure:"traffic_max_response_body"`

	// asynq queue 优先级权重（数字越大优先级越高）
	QueueAgentWeight   int `mapstructure:"queue_agent_weight"`
	QueueDispatchWeight int `mapstructure:"queue_dispatch_weight"`

	// per-host 并发上限（§4.3）：同 host 同时运行的 task 数 ≤ 此值，防同目标叠打触发 WAF 封 IP。
	// ≤ 0 视为不限制。默认 2（渗透场景保守值）。仅对有 target_host 的 task 生效（active planner
	// host 空时放行——真正打 host 的是其 spawn 的子任务）。
	PerHostConcurrency int `mapstructure:"per_host_concurrency"`

	// 情报黑板（lead，§7）的滚动过期时长（小时）——每次写 lead 刷新该 host 的 TTL：持续写则一直
	// 续命，停写后 TTL 到期自净。active/passive 一视同仁，不再分模式清理。默认 720 小时（30 天）。
	// ≤0 关过期（永不过期，仅靠 LTRIM 兜底）。
	LeadTTLHours int `mapstructure:"lead_ttl_hours"`
}

// CompactionConfig 会话历史压缩参数。
type CompactionConfig struct {
	// HistoryCompact 是 agent msgs 滑窗压缩参数（防 context 爆）。
	// 触发：每步 Generate 前算 total tokens，超 TriggerRatio×provider.ContextWindow 启动压缩。
	// 设计原则：永保 system + 首 user，trailing 反向累加保最近 TrailingBudgetRatio×ctx_window，
	// 候选集一次性送 light_provider 蒸馏成 1 条；失败 head-truncate 兜底。
	// 与 notes/lesson/finding 分层记忆协同——蒸馏 prompt 引导省略"已 write_* 上提"内容。
	HistoryCompact HistoryCompactConfig `mapstructure:"history_compact"`
}

// HistoryCompactConfig 是 compaction.history_compact 段配置。
// 全部字段缺省时 applyCompactionDefaults 兜底——非阻塞启动。
type HistoryCompactConfig struct {
	// TriggerRatio 是触发阈值占 provider.ContextWindow 比例；> 此值触发压缩。
	// 默认 0.75 偏保守：32k 模型阈值 24k，留 8k 给输出 + tools + cushion；
	// 128k 模型阈值 96k，留 32k 余量。
	TriggerRatio float64 `mapstructure:"trigger_ratio"`

	// TrailingBudgetRatio 是 trailing window 占 ContextWindow 比例。
	// 反向累加保最近 TrailingBudgetRatio × ContextWindow tokens，严格按对话 turn 边界。
	// 默认 0.50——业界共识 trailing context 30-50%。
	TrailingBudgetRatio float64 `mapstructure:"trailing_budget_ratio"`

	// CompactorTimeoutSeconds 是单次 light LLM 蒸馏调用超时（含网络 + LLM 推理）。
	// 超时退化为 head-truncate 兜底，不阻断主循环。默认 30s。
	CompactorTimeoutSeconds int `mapstructure:"compactor_timeout_seconds"`
}

// SandboxConfig 容器化执行参数（sandbox.Launcher + external.RunCommand）。
//
// run_command 单次硬超时不再有 yaml 配置——LLM 通过 timeout_seconds 必传（schema required），
// 上限由 ToolruntimeConfig.StepToolTimeoutSeconds 钳。
//
// 容器内存 / CPU / 并发数等运行时参数下放到 sandbox.DockerLauncher 内部硬编码
// （per-agent-run 容器模型下这些参数没有按  owner 调整的需求）。
// 见 docs/superpowers/specs/2026-05-16-sandbox-server-design.md。
type SandboxConfig struct {
	// DefaultImage 是 sandbox 镜像 tag（由 sandbox.NewDockerLauncher 用）。
	DefaultImage string `mapstructure:"default_image"`

	// RunTailBytes 是主进程 RunCommand 对 stdout/stderr 截尾的字节数。
	// sandbox-server 返回完整 stdout，截尾在主进程层（贴近 LLM context 管理）。
	RunTailBytes int `mapstructure:"run_tail_bytes"`

	// ViewportWidth/Height 是沙箱 chromium 视口固定尺寸（像素）。
	// 由 sandbox-server 通过 env 透传给 browser-use wrapper：每次 browser-use-cli 调用都带
	// --window-width/--window-height 全局 flag，确保 chromium daemon 用一致尺寸启动。
	//
	// 默认 1280×720——playwright 主流推荐 + vision encoder token 消耗甜点区：
	//   ~1100-1300 tokens/图（Doubao/GPT-4o），翻倍到 1920×1080 仅边际精度收益但 token x2。
	// 高分屏可调大，但会增加 token 消耗。
	ViewportWidth  int `mapstructure:"viewport_width"`
	ViewportHeight int `mapstructure:"viewport_height"`
}

// ToolruntimeConfig 是工具执行的运行时参数（run_command 等本地工具的兜底超时）。
type ToolruntimeConfig struct {
	StepToolTimeoutSeconds int `mapstructure:"step_tool_timeout_seconds"` // 单次工具执行兜底超时上限（run_command timeout_seconds 的钳制上界，防本地工具卡死）
}

// Load 从 path 读取 YAML，应用 LIUSHA_ ENV 覆盖，反序列化、应用默认值并校验。
// Load 读配置 + 应用默认 + 完整校验（含 LLM provider key 在环境变量里非空）。
// 调 LLM 的进程（runner / api）用它。
func Load(path string) (Config, error) { return load(path, true) }

// LoadWithoutLLMKeys 与 Load 同，但跳过 LLM provider key 校验。
// 给纯 ingress 进程（cmd/proxy 仅 MITM + XADD，从不调 LLM）用——避免强塞一堆用不到的 key 才能启动。
func LoadWithoutLLMKeys(path string) (Config, error) { return load(path, false) }

func load(path string, requireLLMKeys bool) (Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetEnvPrefix("LIUSHA")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	if err := v.ReadInConfig(); err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}
	c.ApplyDefaults()
	if err := validate(c); err != nil {
		return Config{}, err
	}
	if requireLLMKeys {
		if err := validateLLMKeys(c); err != nil {
			return Config{}, err
		}
	}
	return c, nil
}

// ApplyDefaults 把所有 sub-struct 的 0 值字段补默认。可重复调用幂等。
//
// 单独导出便于测试：测试用例 `c := Config{}; c.ApplyDefaults()` 即可拿到全默认配置。
func (c *Config) ApplyDefaults() {
	c.API = applyAPIDefaults(c.API)
	c.Postgres = applyPostgresDefaults(c.Postgres)
	c.Redis = applyRedisDefaults(c.Redis)
	c.LLM = applyLLMDefaults(c.LLM)
	c.Proxy = applyProxyDefaults(c.Proxy)
	c.Ingestor = applyIngestorDefaults(c.Ingestor)
	c.Session = applySessionDefaults(c.Session)
	c.Credential = applyCredentialDefaults(c.Credential)
	c.Skills = applySkillsDefaults(c.Skills)
	c.Agents = applyAgentsDefaults(c.Agents)
	c.Runner = applyRunnerDefaults(c.Runner)
	c.Compaction = applyCompactionDefaults(c.Compaction)
	c.Sandbox = applySandboxDefaults(c.Sandbox)
	c.Toolruntime = applyToolruntimeDefaults(c.Toolruntime)
}

func applyAPIDefaults(c APIConfig) APIConfig {
	if c.ReadTimeoutSeconds == 0 {
		c.ReadTimeoutSeconds = 15
	}
	if c.WriteTimeoutSeconds == 0 {
		c.WriteTimeoutSeconds = 30
	}
	if c.ReadHeaderTimeoutSeconds == 0 {
		c.ReadHeaderTimeoutSeconds = 5
	}
	if c.ListenAddr == "" {
		c.ListenAddr = "0.0.0.0:8090"
	}
	if c.ShutdownTimeoutSeconds == 0 {
		c.ShutdownTimeoutSeconds = 5
	}
	return c
}

func applyPostgresDefaults(c PostgresConfig) PostgresConfig {
	if c.MaxConns == 0 {
		c.MaxConns = 20
	}
	// MinConns 允许为 0
	if c.ConnectTimeoutSeconds == 0 {
		c.ConnectTimeoutSeconds = 5
	}
	if c.MaxConnLifetimeSeconds == 0 {
		c.MaxConnLifetimeSeconds = 3600
	}
	return c
}

// applyRedisDefaults：所有字段保持 0 不补默认——0 让 go-redis SDK 用其内部默认值。
// caller 通过 yaml 显式覆盖时才生效。
func applyRedisDefaults(c RedisConfig) RedisConfig {
	return c
}

func applyLLMDefaults(c LLMConfig) LLMConfig {
	c.Retry = applyRetryDefaults(c.Retry)
	c.Invocation = applyInvocationDefaults(c.Invocation)
	return c
}

// DefaultLLMRetry 是 spec §8.5 官方退避表，用作 RetryConfig 兜底。
func DefaultLLMRetry() RetryConfig {
	return RetryConfig{
		Max429:             3,
		Max529:             1,
		Max5xx:             2,
		MaxNet:             2,
		Backoff429Seconds:  []int{1, 4, 16},
		Backoff5xxSeconds:  []int{1, 4},
		BackoffNetSeconds:  []int{1, 3},
		Backoff529Millisec: 0,
	}
}

func applyRetryDefaults(c RetryConfig) RetryConfig {
	d := DefaultLLMRetry()
	if c.Max429 == 0 {
		c.Max429 = d.Max429
	}
	if c.Max529 == 0 {
		c.Max529 = d.Max529
	}
	if c.Max5xx == 0 {
		c.Max5xx = d.Max5xx
	}
	if c.MaxNet == 0 {
		c.MaxNet = d.MaxNet
	}
	if len(c.Backoff429Seconds) == 0 {
		c.Backoff429Seconds = d.Backoff429Seconds
	}
	if len(c.Backoff5xxSeconds) == 0 {
		c.Backoff5xxSeconds = d.Backoff5xxSeconds
	}
	if len(c.BackoffNetSeconds) == 0 {
		c.BackoffNetSeconds = d.BackoffNetSeconds
	}
	// Backoff529Millisec=0 视为合法（立即重试），不补默认
	return c
}

// AsDurations 把 []int 秒数列表转 []time.Duration。
func AsDurations(secs []int) []time.Duration {
	out := make([]time.Duration, len(secs))
	for i, s := range secs {
		out[i] = time.Duration(s) * time.Second
	}
	return out
}

func applyInvocationDefaults(c InvocationConfig) InvocationConfig {
	if c.BufferSize == 0 {
		c.BufferSize = 1024
	}
	if c.BatchSize == 0 {
		c.BatchSize = 100
	}
	if c.FlushIntervalMs == 0 {
		c.FlushIntervalMs = 1000
	}
	if c.InsertTimeoutSec == 0 {
		c.InsertTimeoutSec = 5
	}
	return c
}

func applyProxyDefaults(c ProxyConfig) ProxyConfig {
	if c.WindowBatch == 0 {
		c.WindowBatch = 20
	}
	if c.WindowMaxAgeSeconds == 0 {
		c.WindowMaxAgeSeconds = 30
	}
	if len(c.ExcludeUpgradeProtocols) == 0 {
		c.ExcludeUpgradeProtocols = []string{"websocket"}
	}
	if c.MaxRequestBodySize == 0 {
		c.MaxRequestBodySize = 2 << 20 // 2 MiB
	}
	if c.MaxResponseBodySize == 0 {
		c.MaxResponseBodySize = 8 << 20 // 8 MiB
	}
	if c.ListenAddr == "" {
		c.ListenAddr = "0.0.0.0:8888"
	}
	if c.InternalAddr == "" {
		c.InternalAddr = "127.0.0.1:18888"
	}
	if c.CertSubdir == "" {
		c.CertSubdir = ".liusha"
	}
	if c.ShutdownTimeoutSeconds == 0 {
		c.ShutdownTimeoutSeconds = 5
	}
	if c.StreamName == "" {
		c.StreamName = "flow_events"
	}
	if c.StreamMaxLen == 0 {
		c.StreamMaxLen = 100_000
	}
	return c
}

// defaultConsumerName 生成每实例唯一的 Stream 消费者名。
//
// 消费组（ConsumerGroup）是负载均衡单元、多副本共享；消费者名（本值）是每实例身份，
// 同组内必须唯一——否则多副本用同名进同组，Redis 无法区分 pending 归属，投递/ack 记账错乱
// （且不报错，属静默故障）。故派生自 hostname + pid：容器/Pod 场景 hostname 通常已唯一，
// 附加 pid 再兜住"同主机多进程"。取不到 hostname 时退化为 pid，仍保证同主机各进程互不撞名。
func defaultConsumerName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("ingestor-%s-%d", host, os.Getpid())
}

func applyIngestorDefaults(c IngestorConfig) IngestorConfig {
	if c.ConsumerGroup == "" {
		c.ConsumerGroup = "liusha-ingestor"
	}
	if c.ConsumerName == "" {
		c.ConsumerName = defaultConsumerName()
	}
	if c.ReadBatch == 0 {
		c.ReadBatch = 16
	}
	if c.ReadBlockTimeoutMs == 0 {
		c.ReadBlockTimeoutMs = 1000
	}
	if c.RetryDelayMs == 0 {
		c.RetryDelayMs = 500
	}
	if c.RecreateGroupDelayMs == 0 {
		c.RecreateGroupDelayMs = 500
	}
	if c.AggregateBatchSize == 0 {
		c.AggregateBatchSize = 20
	}
	if c.AggregateWindowSeconds == 0 {
		c.AggregateWindowSeconds = 10
	}
	return c
}

func applySessionDefaults(c SessionConfig) SessionConfig {
	if c.FindingsLimitInPrompt == 0 {
		c.FindingsLimitInPrompt = 1000 // 安全闸（正常扫描全量注入，不截断；超长交 ① 压缩）
	}
	return c
}

func applyCredentialDefaults(c CredentialConfig) CredentialConfig {
	if c.RedisKeyPrefix == "" {
		c.RedisKeyPrefix = "credentials:"
	}
	return c
}

func applySkillsDefaults(c SkillsConfig) SkillsConfig {
	if c.Root == "" {
		c.Root = "./skills"
	}
	return c
}

func applyAgentsDefaults(c AgentsConfig) AgentsConfig {
	if c.Root == "" {
		c.Root = "./agents"
	}
	return c
}

func applyRunnerDefaults(c RunnerConfig) RunnerConfig {
	if c.SoloAgentRunTimeoutSeconds == 0 {
		c.SoloAgentRunTimeoutSeconds = 3600 // 60 分钟（solo 引擎 agent_run 整体超时；> step_tool=1800，留 30min buffer 给 agent 主循环收尾）
	}
	if c.SwarmAgentRunTimeoutSeconds == 0 {
		c.SwarmAgentRunTimeoutSeconds = 14400 // 4 小时（swarm 引擎 agent_run 整体超时；站点扫描爬+测耗时长；对齐 sandbox max lifetime 4h）
	}
	if c.StepLLMTimeoutSeconds == 0 {
		c.StepLLMTimeoutSeconds = 300
	}
	if c.AsynqConcurrency == 0 {
		c.AsynqConcurrency = 6
	}
	if c.AsynqShutdownTimeoutSeconds == 0 {
		c.AsynqShutdownTimeoutSeconds = 30 // graceful 等 in-flight 主循环落地，超时强制中断
	}
	if c.ShutdownTimeoutSeconds == 0 {
		c.ShutdownTimeoutSeconds = 5
	}
	if c.HealthzAddr == "" {
		c.HealthzAddr = ":9090"
	}
	if c.TrafficMaxRequestBody == 0 {
		c.TrafficMaxRequestBody = 2 << 20
	}
	if c.TrafficMaxResponseBody == 0 {
		c.TrafficMaxResponseBody = 8 << 20
	}
	if c.QueueAgentWeight == 0 {
		c.QueueAgentWeight = 5
	}
	if c.QueueDispatchWeight == 0 {
		c.QueueDispatchWeight = 1
	}
	if c.PerHostConcurrency == 0 {
		c.PerHostConcurrency = 2 // 默认同 host 并发 ≤ 2；显式设 -1 可关限速
	}
	if c.LeadTTLHours == 0 {
		c.LeadTTLHours = 720 // 默认 30 天滚动过期；显式设 -1 可关（永不过期，交给 LTRIM 兜底）
	}
	return c
}

func applyCompactionDefaults(c CompactionConfig) CompactionConfig {
	c.HistoryCompact = applyHistoryCompactDefaults(c.HistoryCompact)
	return c
}

// applyHistoryCompactDefaults 给 compaction.history_compact 段缺省字段兜底。
// 设计取舍：所有比例/超时都允许 yaml 显式 0 → 仍兜默认（不让用户误填 0 关掉压缩）。
// 想关压缩走 TriggerRatio 设极大值（如 99）让永不触发；或在 runtime 层注入 NoopHistoryCompactor。
func applyHistoryCompactDefaults(c HistoryCompactConfig) HistoryCompactConfig {
	if c.TriggerRatio <= 0 {
		c.TriggerRatio = 0.75
	}
	if c.TrailingBudgetRatio <= 0 {
		c.TrailingBudgetRatio = 0.50
	}
	if c.CompactorTimeoutSeconds <= 0 {
		c.CompactorTimeoutSeconds = 30
	}
	return c
}

func applySandboxDefaults(c SandboxConfig) SandboxConfig {
	if c.DefaultImage == "" {
		c.DefaultImage = "ghcr.io/v3teran/liusha-pentools:latest"
	}
	if c.RunTailBytes == 0 {
		c.RunTailBytes = 8192
	}
	// 视口 1280×720——playwright 主流推荐 + vision encoder sweet spot；高分屏可 yaml 覆盖。
	if c.ViewportWidth == 0 {
		c.ViewportWidth = 1280
	}
	if c.ViewportHeight == 0 {
		c.ViewportHeight = 720
	}
	return c
}

func applyToolruntimeDefaults(c ToolruntimeConfig) ToolruntimeConfig {
	if c.StepToolTimeoutSeconds == 0 {
		// 1800s = 30 分钟。给 sqlmap 升级 + 多 variant 重放充足上限。
		c.StepToolTimeoutSeconds = 1800
	}
	return c
}

// validate 校验 provider schema 完整性（supports_vision / context_window 必填）——
// 不碰 LLM key，所有进程（含纯 ingress 的 proxy）都跑。
func validate(c Config) error {
	// 所有 provider 必须显式声明 supports_vision——nil 视为未填，启动 fail-fast。
	// 设计原则：caller（runtime / openai_compat）路由含图 message 时依赖此 flag，
	// 默认零值（false）会让 deepseek 等 OpenAI 协议族在 yaml 漏填时被当成不支持 vision，
	// 实际可能反过来（如 gpt-4o）——强制显式声明消除歧义。
	for name, p := range c.Providers {
		if p.SupportsVision == nil {
			return fmt.Errorf("provider %q: supports_vision 必填（yaml 必须显式写 true 或 false）", name)
		}
		// context_window 同强制必填——会话历史压缩按此算阈值；漏填会用 0 兜底导致一直触发或永不触发。
		if p.ContextWindow == nil || *p.ContextWindow <= 0 {
			return fmt.Errorf("provider %q: context_window 必填且 > 0（model 总上下文窗口 tokens 数）", name)
		}
	}
	return nil
}

// validateLLMKeys 校验 yaml 内 LLM 种子的自洽性。仅调 LLM 的进程（runner / api）需要——
// proxy 用 LoadWithoutLLMKeys 跳过。
//
// 事实源是 DB（前端「模型/能力分档」模块 CRUD 改 llm_provider/llm_role_route），yaml 仅首次
// insert-only 种子（见 seed.ImportLLM）。故 providers 留空 = 完全依赖 DB，此时跳过全部校验，
// 不再强制 tiers.heavy——否则空 yaml + 满 DB 的正常部署会被误判 fail-fast。
// providers 一旦非空则按老规矩校验（heavy 档必填 + 各档 api_key_env 在 ENV 非空），保证种子可用。
func validateLLMKeys(c Config) error {
	if len(c.Providers) == 0 {
		return nil
	}
	check := func(name, role string) error {
		p, ok := c.Providers[name]
		if !ok {
			return fmt.Errorf("%s %q not in providers", role, name)
		}
		if p.APIKeyEnv == "" {
			return fmt.Errorf("%s %q: api_key_env not configured", role, name)
		}
		if os.Getenv(p.APIKeyEnv) == "" {
			return fmt.Errorf("env %s empty (required for %s=%s)", p.APIKeyEnv, role, name)
		}
		return nil
	}
	// medium 是隐式默认档（agent 未显式归档即落 medium），故种子必须配它。
	medium := c.LLM.Tiers["medium"]
	if medium == "" {
		return fmt.Errorf("llm.tiers.medium required")
	}
	if err := check(medium, "tiers.medium"); err != nil {
		return err
	}
	for _, pair := range []struct{ name, role string }{
		{c.LLM.Tiers["simple"], "tiers.simple"},
		{c.LLM.Tiers["complex"], "tiers.complex"},
		{c.LLM.Fallback, "fallback"},
	} {
		if pair.name != "" {
			if err := check(pair.name, pair.role); err != nil {
				return err
			}
		}
	}
	return nil
}
