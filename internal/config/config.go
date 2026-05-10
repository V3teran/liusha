// Package config 用 viper 加载 YAML 配置 + ENV 覆盖 + 启动校验。
//
// v1.3 改造：把散落在各包的硬编码常量统一收敛到本文件。
// 设计原则：
//   - 每个 sub-struct 字段缺省值由 ApplyDefaults() 兜底，避免 yaml 缺字段时进程拒启动
//   - 仅 LLM provider key（DefaultProvider 等）做强制 validate（密钥读取必须有 provider 信息）
//   - ENV 覆盖前缀 LIUSHA_，二级用 _ 分隔（如 LIUSHA_LLM_DEFAULT_PROVIDER）
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
	Pricing     PricingConfig             `mapstructure:"pricing"`
	Proxy       ProxyConfig               `mapstructure:"proxy"`
	Ingestor    IngestorConfig            `mapstructure:"ingestor"`
	Engagement  EngagementConfig          `mapstructure:"engagement"`
	Credential  CredentialConfig          `mapstructure:"credential"`
	Skills      SkillsConfig              `mapstructure:"skills"`
	Scanner     ScannerConfig             `mapstructure:"scanner"`
	React       ReactConfig               `mapstructure:"react"`
	Sandbox     SandboxConfig             `mapstructure:"sandbox"`
	Toolruntime ToolruntimeConfig         `mapstructure:"toolruntime"`
	Lesson      LessonConfig              `mapstructure:"lesson"`
}

// APIConfig 是 cmd/api 的 HTTP 入口参数。
type APIConfig struct {
	ReadTimeoutSeconds       int    `mapstructure:"read_timeout_seconds"`
	WriteTimeoutSeconds      int    `mapstructure:"write_timeout_seconds"`
	ReadHeaderTimeoutSeconds int    `mapstructure:"read_header_timeout_seconds"` // 也用于 proxy/scanner healthz HTTP server
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
	PoolSize             int `mapstructure:"pool_size"`
	MinIdleConns         int `mapstructure:"min_idle_conns"`
	DialTimeoutSeconds   int `mapstructure:"dial_timeout_seconds"`
	ReadTimeoutSeconds   int `mapstructure:"read_timeout_seconds"`
	WriteTimeoutSeconds  int `mapstructure:"write_timeout_seconds"`
}

// LLMConfig 包含主/轻/视觉/降级 4 个 provider 字段 + 双 namespace 路由表 + retry 参数 +
// llm_invocation 异步 batch 写入参数。
type LLMConfig struct {
	DefaultProvider  string            `mapstructure:"default_provider"`
	LightProvider    string            `mapstructure:"light_provider"`
	VisionProvider   string            `mapstructure:"vision_provider"`
	FallbackProvider string            `mapstructure:"fallback_provider"`
	MaxSteps         int               `mapstructure:"max_steps"`
	MaxTokensPerCall int               `mapstructure:"max_tokens_per_call"`
	Agents           map[string]string `mapstructure:"agents"`
	Utilities        map[string]string `mapstructure:"utilities"`

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
	Type           string `mapstructure:"type"`
	BaseURL        string `mapstructure:"base_url"`
	DefaultModel   string `mapstructure:"default_model"`
	VisionModel    string `mapstructure:"vision_model"`
	APIKeyEnv      string `mapstructure:"api_key_env"`
	MaxTokens      int    `mapstructure:"max_tokens"`
	SupportsTools  bool   `mapstructure:"supports_tools"`
	SupportsVision bool   `mapstructure:"supports_vision"`
}

// PricingConfig 是 LLM 模型单价表（USD per 1M tokens）。
//
// 单价频繁变动（provider 季度降价）+ 多环境策略不同 → yaml 化便于不重编更新。
// key 形如 "deepseek/deepseek-chat" / "anthropic/claude-sonnet-4-6"。
type PricingConfig struct {
	Models map[string]ModelPriceConfig `mapstructure:"models"`
}

// ModelPriceConfig 是单个模型的计价参数。
//
// CachedInIn 标记 Usage.CachedTokens 是否已被计入 InTokens：
//   - true（OpenAI/DeepSeek）：CachedTokens ⊆ InTokens；Estimate 先减再分别计价
//   - false（Anthropic 默认）：CachedTokens 与 InTokens 独立返回；Estimate 加项处理
type ModelPriceConfig struct {
	InputPerMUSD  float64 `mapstructure:"input_per_m_usd"`
	OutputPerMUSD float64 `mapstructure:"output_per_m_usd"`
	CacheDiscount float64 `mapstructure:"cache_discount"`
	CachedInIn    bool    `mapstructure:"cached_in_in"`
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

	// 进程入口（cmd/proxy）
	ListenAddr             string `mapstructure:"listen_addr"`
	InternalAddr           string `mapstructure:"internal_addr"`
	HealthzAddr            string `mapstructure:"healthz_addr"`
	CertSubdir             string `mapstructure:"cert_subdir"`
	ShutdownTimeoutSeconds int    `mapstructure:"shutdown_timeout_seconds"`

	// Redis Stream（proxy → ingestor 之间）
	StreamName   string `mapstructure:"stream_name"`
	StreamMaxLen int    `mapstructure:"stream_max_len"`
}

// IngestorConfig 是 Stream 流量摄入器（cmd/scanner 内 goroutine）参数。
type IngestorConfig struct {
	ConsumerGroup        string `mapstructure:"consumer_group"`
	ConsumerName         string `mapstructure:"consumer_name"`
	ReadBatch            int    `mapstructure:"read_batch"`
	ReadBlockTimeoutMs   int    `mapstructure:"read_block_timeout_ms"`
	RetryDelayMs         int    `mapstructure:"retry_delay_ms"`
	RecreateGroupDelayMs int    `mapstructure:"recreate_group_delay_ms"`
}

// EngagementConfig 是 engagement 懒创建 + 滚动归档参数。
type EngagementConfig struct {
	IdleTimeoutHours       int `mapstructure:"idle_timeout_hours"`
	SweeperIntervalSeconds int `mapstructure:"sweeper_interval_seconds"`

	// Rotator 三阈值（proxy 模式）
	MaxAgeHours       int `mapstructure:"max_age_hours"`
	MaxStateSizeBytes int `mapstructure:"max_state_size_bytes"`
	MaxFindings       int `mapstructure:"max_findings"`

	// memory_notes 截断
	MaxMemoryNotesEntries int `mapstructure:"max_memory_notes_entries"`
	DefaultNotesLimit     int `mapstructure:"default_notes_limit"`

	// DefaultTenant 是单租户场景下的兜底租户名；多租户后由 caller 显式传入。
	// 影响 ingestor、engagement 自动创建、lesson 默认 tenant 等。
	DefaultTenant string `mapstructure:"default_tenant"`
}

// CredentialConfig 是 credential.RedisProvider 的 redis key 前缀。
type CredentialConfig struct {
	RedisKeyPrefix string `mapstructure:"redis_key_prefix"`
}

// SkillsConfig 是 SKILL.md 外部目录。
type SkillsConfig struct {
	Root string `mapstructure:"root"`
}

// ScannerConfig 是 cmd/scanner 进程的运行时参数。
type ScannerConfig struct {
	MainMaxSteps               int    `mapstructure:"main_max_steps"`
	MainTaskTimeoutSeconds     int    `mapstructure:"main_task_timeout_seconds"`     // 单个主 ReAct 任务整体超时（asynq handler 入口 WithTimeout）
	MainWatchdogSeconds        int    `mapstructure:"main_watchdog_seconds"`
	AsynqConcurrency           int    `mapstructure:"asynq_concurrency"`
	AsynqShutdownTimeoutSeconds int   `mapstructure:"asynq_shutdown_timeout_seconds"` // asynq.Shutdown 等 in-flight task 完成的超时
	ShutdownTimeoutSeconds     int    `mapstructure:"shutdown_timeout_seconds"`
	HealthzAddr            string `mapstructure:"healthz_addr"`
	FlowMaxRequestBody     int    `mapstructure:"flow_max_request_body"`
	FlowMaxResponseBody    int    `mapstructure:"flow_max_response_body"`

	// asynq queue 优先级权重（数字越大优先级越高）
	QueueHunterWeight int `mapstructure:"queue_hunter_weight"`
	QueueDispatchWeight     int `mapstructure:"queue_dispatch_weight"`
}

// ReactConfig 主 ReAct 循环参数。
type ReactConfig struct {
	ObserverEverySteps   int `mapstructure:"observer_every_steps"`
	DoneForceMaxRejects  int `mapstructure:"done_force_max_rejects"`
	ObserverArgsTruncate int `mapstructure:"observer_args_truncate"` // 喂 observer LLM 的 tool args 截断字节数
	ObserverObsTruncate  int `mapstructure:"observer_obs_truncate"`  // 喂 observer LLM 的 ObsSummary 截断字节数
}

// SandboxConfig 容器化执行参数（external.RunCommand + DockerRunner）。
type SandboxConfig struct {
	DefaultImage             string  `mapstructure:"default_image"`
	RunMinTimeoutSeconds     int     `mapstructure:"run_min_timeout_seconds"`
	RunMaxTimeoutSeconds     int     `mapstructure:"run_max_timeout_seconds"`
	RunDefaultTimeoutSeconds int     `mapstructure:"run_default_timeout_seconds"`
	RunDefaultMemMB          int     `mapstructure:"run_default_mem_mb"`
	RunDefaultCPUs           float64 `mapstructure:"run_default_cpus"`
	RunTailBytes             int     `mapstructure:"run_tail_bytes"`
	RunnerConcurrency        int     `mapstructure:"runner_concurrency"`

	// ScanNetwork 限制扫描容器只能访问 scope hosts（如 docker network 名 "liusha_scan_net"）。
	// 空字符串 → docker 默认 bridge（可访问公网）。
	ScanNetwork string `mapstructure:"scan_network"`
}

// ToolruntimeConfig 是 toolruntime/middleware 参数。
type ToolruntimeConfig struct {
	ResultCompressThreshold   int `mapstructure:"result_compress_threshold"`
	ResultCompressSnippet     int `mapstructure:"result_compress_snippet"`
	ResultCompressSummary     int `mapstructure:"result_compress_summary"`
	ToolExecuteTimeoutSeconds int `mapstructure:"tool_execute_timeout_seconds"` // 单次 tool Execute 兜底超时（middleware 层 WithTimeout，防 fetch_credentials/check_heuristics 等本地工具卡死）
}

// LessonConfig 是 lesson 提取与 touch 重试参数（react/lesson_extract）。
type LessonConfig struct {
	ExtractedPriority      int `mapstructure:"extracted_priority"`        // LessonExtract 写入 lesson 时的固定优先级（中-高）
	ExtractTimeoutSeconds  int `mapstructure:"extract_timeout_seconds"`   // extractLesson 整体超时（hook 用 context.Background()，业务级兜底）
	TouchMaxRetries        int `mapstructure:"touch_max_retries"`         // hit_count 重试次数（首发 lesson 异步写入存在时序竞争）
	TouchInitialBackoffMs  int `mapstructure:"touch_initial_backoff_ms"`  // 首次重试 backoff
	TouchMaxBackoffMs      int `mapstructure:"touch_max_backoff_ms"`      // 重试 backoff 上限（几何递增 cap）
}

// Load 从 path 读取 YAML，应用 LIUSHA_ ENV 覆盖，反序列化、应用默认值并校验。
func Load(path string) (Config, error) {
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
	c.Pricing = applyPricingDefaults(c.Pricing)
	c.Proxy = applyProxyDefaults(c.Proxy)
	c.Ingestor = applyIngestorDefaults(c.Ingestor)
	c.Engagement = applyEngagementDefaults(c.Engagement)
	c.Credential = applyCredentialDefaults(c.Credential)
	c.Skills = applySkillsDefaults(c.Skills)
	c.Scanner = applyScannerDefaults(c.Scanner)
	c.React = applyReactDefaults(c.React)
	c.Sandbox = applySandboxDefaults(c.Sandbox)
	c.Toolruntime = applyToolruntimeDefaults(c.Toolruntime)
	c.Lesson = applyLessonDefaults(c.Lesson)
}

func applyLessonDefaults(c LessonConfig) LessonConfig {
	if c.ExtractedPriority == 0 {
		c.ExtractedPriority = 7
	}
	if c.ExtractTimeoutSeconds == 0 {
		c.ExtractTimeoutSeconds = 60 // hook 用 context.Background()；防 LLM 调用永久挂起
	}
	if c.TouchMaxRetries == 0 {
		c.TouchMaxRetries = 7
	}
	if c.TouchInitialBackoffMs == 0 {
		c.TouchInitialBackoffMs = 500
	}
	if c.TouchMaxBackoffMs == 0 {
		c.TouchMaxBackoffMs = 4000
	}
	return c
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
		c.ListenAddr = "0.0.0.0:8080"
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
	if c.MaxSteps == 0 {
		c.MaxSteps = 30
	}
	if c.MaxTokensPerCall == 0 {
		c.MaxTokensPerCall = 4096
	}
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

// DefaultPricing 是兜底单价表（spec §8.2）。yaml 未填 pricing.models 时使用。
func DefaultPricing() PricingConfig {
	return PricingConfig{
		Models: map[string]ModelPriceConfig{
			"deepseek/deepseek-chat":      {InputPerMUSD: 0.27, OutputPerMUSD: 1.10, CacheDiscount: 0.10, CachedInIn: true},
			"anthropic/claude-sonnet-4-6": {InputPerMUSD: 3.00, OutputPerMUSD: 15.00, CacheDiscount: 0.30},
			"anthropic/claude-haiku-4-5":  {InputPerMUSD: 1.00, OutputPerMUSD: 5.00, CacheDiscount: 0.30},
		},
	}
}

func applyPricingDefaults(c PricingConfig) PricingConfig {
	if len(c.Models) == 0 {
		return DefaultPricing()
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
	if c.HealthzAddr == "" {
		c.HealthzAddr = ":9091"
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

func applyIngestorDefaults(c IngestorConfig) IngestorConfig {
	if c.ConsumerGroup == "" {
		c.ConsumerGroup = "liusha-ingestor"
	}
	if c.ConsumerName == "" {
		c.ConsumerName = "ingestor-1"
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
	return c
}

func applyEngagementDefaults(c EngagementConfig) EngagementConfig {
	if c.IdleTimeoutHours == 0 {
		c.IdleTimeoutHours = 24
	}
	if c.SweeperIntervalSeconds == 0 {
		c.SweeperIntervalSeconds = 600
	}
	if c.MaxAgeHours == 0 {
		c.MaxAgeHours = 24
	}
	if c.MaxStateSizeBytes == 0 {
		c.MaxStateSizeBytes = 2 << 20 // 2 MiB
	}
	if c.MaxFindings == 0 {
		c.MaxFindings = 300
	}
	if c.MaxMemoryNotesEntries == 0 {
		c.MaxMemoryNotesEntries = 300
	}
	if c.DefaultNotesLimit == 0 {
		c.DefaultNotesLimit = 200
	}
	if c.DefaultTenant == "" {
		c.DefaultTenant = "default"
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

func applyScannerDefaults(c ScannerConfig) ScannerConfig {
	if c.MainMaxSteps == 0 {
		c.MainMaxSteps = 60
	}
	if c.MainTaskTimeoutSeconds == 0 {
		c.MainTaskTimeoutSeconds = 4200 // 70 分钟（≥ sub_task_timeout=3600 + 主 ReAct 自身收尾；与 tool_execute=1800 联动放大）
	}
	if c.MainWatchdogSeconds == 0 {
		c.MainWatchdogSeconds = 300
	}
	if c.AsynqConcurrency == 0 {
		c.AsynqConcurrency = 6
	}
	if c.AsynqShutdownTimeoutSeconds == 0 {
		c.AsynqShutdownTimeoutSeconds = 30 // graceful 等 in-flight 主 ReAct 落地，超时强制中断
	}
	if c.ShutdownTimeoutSeconds == 0 {
		c.ShutdownTimeoutSeconds = 5
	}
	if c.HealthzAddr == "" {
		c.HealthzAddr = ":9090"
	}
	if c.FlowMaxRequestBody == 0 {
		c.FlowMaxRequestBody = 2 << 20
	}
	if c.FlowMaxResponseBody == 0 {
		c.FlowMaxResponseBody = 8 << 20
	}
	if c.QueueHunterWeight == 0 {
		c.QueueHunterWeight = 5
	}
	if c.QueueDispatchWeight == 0 {
		c.QueueDispatchWeight = 1
	}
	return c
}

func applyReactDefaults(c ReactConfig) ReactConfig {
	if c.ObserverEverySteps == 0 {
		c.ObserverEverySteps = 5
	}
	if c.DoneForceMaxRejects == 0 {
		c.DoneForceMaxRejects = 3
	}
	if c.ObserverArgsTruncate == 0 {
		c.ObserverArgsTruncate = 256
	}
	if c.ObserverObsTruncate == 0 {
		c.ObserverObsTruncate = 400
	}
	return c
}

func applySandboxDefaults(c SandboxConfig) SandboxConfig {
	if c.DefaultImage == "" {
		c.DefaultImage = "liusha/pentools:latest"
	}
	if c.RunMinTimeoutSeconds == 0 {
		c.RunMinTimeoutSeconds = 30
	}
	if c.RunMaxTimeoutSeconds == 0 {
		c.RunMaxTimeoutSeconds = 300
	}
	if c.RunDefaultTimeoutSeconds == 0 {
		c.RunDefaultTimeoutSeconds = 90
	}
	if c.RunDefaultMemMB == 0 {
		c.RunDefaultMemMB = 512
	}
	if c.RunDefaultCPUs == 0 {
		c.RunDefaultCPUs = 1.0
	}
	if c.RunTailBytes == 0 {
		c.RunTailBytes = 8192
	}
	if c.RunnerConcurrency == 0 {
		c.RunnerConcurrency = 5
	}
	return c
}

func applyToolruntimeDefaults(c ToolruntimeConfig) ToolruntimeConfig {
	if c.ResultCompressThreshold == 0 {
		c.ResultCompressThreshold = 64 * 1024 // 64KB；与 sandbox.run_tail_bytes×2 + run_replay 5 variant matrix 留余量
	}
	if c.ResultCompressSnippet == 0 {
		c.ResultCompressSnippet = 16 * 1024 // 16KB；截断后喂 LLM 的概览大小
	}
	if c.ResultCompressSummary == 0 {
		c.ResultCompressSummary = 1024 // 1KB；Result.Summary（Observer 滑动窗）
	}
	if c.ToolExecuteTimeoutSeconds == 0 {
		// 1800s = 30 分钟。给 sqlmap 升级 + 多 variant 重放充足上限。
		c.ToolExecuteTimeoutSeconds = 1800
	}
	return c
}

// validate 强制：default_provider 必填，light/vision/fallback 选填但配了就必须 check 通过。
func validate(c Config) error {
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
	if c.LLM.DefaultProvider == "" {
		return fmt.Errorf("llm.default_provider required")
	}
	if err := check(c.LLM.DefaultProvider, "default_provider"); err != nil {
		return err
	}
	for _, pair := range []struct{ name, role string }{
		{c.LLM.LightProvider, "light_provider"},
		{c.LLM.VisionProvider, "vision_provider"},
		{c.LLM.FallbackProvider, "fallback_provider"},
	} {
		if pair.name != "" {
			if err := check(pair.name, pair.role); err != nil {
				return err
			}
		}
	}
	return nil
}
