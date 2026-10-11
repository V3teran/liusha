// Package config 用 viper 加载 YAML 配置 + ENV 覆盖 + 启动校验。
//
// 设计原则：
//   - 所有可调参数统一收敛到本文件，避免散落各包的硬编码常量
//   - 每个 sub-struct 字段缺省值由 ApplyDefaults() 兜底，避免 yaml 缺字段时进程拒启动
//   - 仅 LLM provider key（complexity_routes.medium 等）做强制 validate（密钥读取必须有 provider 信息）
//   - ENV 覆盖前缀 LIUSHA_，二级用 _ 分隔（如 LIUSHA_LLM_FALLBACK）；注意 llm.complexity_routes 是 map，
//     AutomaticEnv 不覆盖单个 map key，改档位 provider 走 yaml 或前端「能力分档」页
package config

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
	Agents      AgentsConfig              `mapstructure:"agents_dir"`
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

// LLMConfig 是 LLM 复杂度路由 + retry 参数 + llm_invocation 异步 batch 写入参数。
//
// 复杂度三档（complex 深度推理 / medium 标准推理 / simple 快速响应）：
// agent → 档的绑定在 agent.complexity（前端「智能体」页可改，DB 事实源经三级缓存读）；
// 档 → provider 的绑定落 DB（前端「能力分档」页可配），此处 yaml 仅首次 insert-only 种子。
type LLMConfig struct {
	ComplexityRoutes map[string]string `mapstructure:"complexity_routes"` // 档(simple/medium/complex) → provider key
	Fallback         string            `mapstructure:"fallback"`          // 主 provider 重试耗尽后的全局备胎 provider key

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
	RetentionDays    int `mapstructure:"retention_days"` // 留存天数，<=0 表示永久保留
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

// AgentsConfig 是 executor 角色 markdown 外部目录配置（agents/*.md，planner + 子代理）。
type AgentsConfig struct {
	Root string `mapstructure:"root"`
}

// RunnerConfig 是 cmd/runner 进程的运行时参数。
type RunnerConfig struct {
	AgentRunTimeoutSeconds      int    `mapstructure:"agent_run_timeout_seconds"` // agent task 整体超时（asynq handler 入口 WithTimeout）
	StepLLMTimeoutSeconds       int    `mapstructure:"step_llm_timeout_seconds"`
	AsynqConcurrency            int    `mapstructure:"asynq_concurrency"`
	AsynqShutdownTimeoutSeconds int    `mapstructure:"asynq_shutdown_timeout_seconds"` // asynq.Shutdown 等 in-flight task 完成的超时
	ShutdownTimeoutSeconds      int    `mapstructure:"shutdown_timeout_seconds"`
	HealthzAddr                 string `mapstructure:"healthz_addr"`
	TrafficMaxRequestBody       int    `mapstructure:"traffic_max_request_body"`
	TrafficMaxResponseBody      int    `mapstructure:"traffic_max_response_body"`

	// asynq queue 优先级权重（数字越大优先级越高）
	QueueAgentWeight    int `mapstructure:"queue_agent_weight"`
	QueueDispatchWeight int `mapstructure:"queue_dispatch_weight"`

	// per-host 并发上限（§4.3）：同 host 同时运行的 task 数 ≤ 此值，防同目标叠打触发 WAF 封 IP。
	// ≤ 0 视为不限制。默认 2（渗透场景保守值）。仅对有 target_host 的 task 生效（active planner
	// host 空时放行——真正打 host 的是其 spawn 的子任务）。
	PerHostConcurrency int `mapstructure:"per_host_concurrency"`

	// MaxStepsPerTask 是单个 task 的最大执行步数（Action 完成次数）。
	// 达到此上限时任务自动终止，防止无限循环。≤ 0 视为无限制。默认 1000。
	MaxStepsPerTask int `mapstructure:"max_steps_per_task"`
}

// CompactionConfig 会话历史压缩参数。
type CompactionConfig struct {
	// HistoryCompact 是 agent msgs 滑窗压缩参数（防 context 爆）。
	// 触发：每步 Generate 前算 total tokens，超 TriggerRatio×provider.ContextWindow 启动压缩。
	// 设计原则：永保 system + 首 user，trailing 反向累加保最近 TrailingBudgetRatio×ctx_window，
	// 候选集一次性送 light_provider 蒸馏成 1 条；失败 head-truncate 兜底。
	// 蒸馏 prompt 引导省略"已 write_* 上提"的内容，避免重复。
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
