// Package config 加载部署配置：内置默认 → 覆盖文件 → LIUSHA_ 环境变量，再补默认值并校验。
//
// 设计原则：
//   - 所有启动期参数收敛到配置树，避免散落各包的硬编码常量
//   - 内置默认（config.yaml，编进程序）列出全部键，环境变量因此对每个键都生效；
//     键名 a.b_c 对应环境变量 LIUSHA_A_B_C
//   - 每个 sub-struct 字段缺省值由 ApplyDefaults() 兜底，覆盖文件写成零值也不拒启动
//   - 只校验 LLM 种子的形状（声明了 providers 时路由须引用已声明的块，见 validateLLMSeed）
//   - llm.complexity_routes 与 providers 是 map，环境变量不覆盖单个 map key，改档位走前端「LLM 配置」页
package config

// AppConfig 是 liusha 顶层配置树。
type AppConfig struct {
	API          APIConfig                 `mapstructure:"api"`
	Postgres     PostgresConfig            `mapstructure:"postgres"`
	Redis        RedisConfig               `mapstructure:"redis"`
	LLM          LLMConfig                 `mapstructure:"llm"`
	Providers    map[string]ProviderConfig `mapstructure:"providers"`
	Proxy        ProxyConfig               `mapstructure:"proxy"`
	Ingestor     IngestorConfig            `mapstructure:"ingestor"`
	Credential   CredentialConfig          `mapstructure:"credential"`
	Runner       RunnerConfig              `mapstructure:"runner"`
	Sandbox      SandboxConfig             `mapstructure:"sandbox"`
	Conversation ConversationConfig        `mapstructure:"conversation"`
}

// ConversationConfig 是会话域参数（api 消费；runner 不经会话收发附件）。
type ConversationConfig struct {
	// AttachmentMaxBytes 是单附件上传上限（multipart 流式拷贝按此截断）。
	AttachmentMaxBytes int64 `mapstructure:"attachment_max_bytes"`
}

// APIConfig 是 api 组件的 HTTP 入口参数。
type APIConfig struct {
	ReadTimeoutSeconds       int    `mapstructure:"read_timeout_seconds"`
	WriteTimeoutSeconds      int    `mapstructure:"write_timeout_seconds"`
	ReadHeaderTimeoutSeconds int    `mapstructure:"read_header_timeout_seconds"` // runner HTTP（healthz + ingest）的请求头读取超时
	ListenAddr               string `mapstructure:"listen_addr"`
	ShutdownTimeoutSeconds   int    `mapstructure:"shutdown_timeout_seconds"`
	// CookieSecure 给会话 Cookie 加 Secure 属性：经 HTTPS 访问时设 true；
	// 纯 http 访问必须 false，否则浏览器不保存会话。
	CookieSecure bool `mapstructure:"cookie_secure"`
}

// PostgresConfig 是 PostgreSQL 连接与连接池参数。
type PostgresConfig struct {
	// DSN 是连接串（postgres://user:pass@host:port/db?sslmode=...），含密码，经
	// LIUSHA_POSTGRES_DSN 注入，不写进配置文件。
	DSN                    string `mapstructure:"dsn"`
	MaxConns               int    `mapstructure:"max_conns"`
	MinConns               int    `mapstructure:"min_conns"`
	ConnectTimeoutSeconds  int    `mapstructure:"connect_timeout_seconds"`
	MaxConnLifetimeSeconds int    `mapstructure:"max_conn_lifetime_seconds"`
}

// RedisConfig 是 Redis 连接与连接池参数。
//
// 数值字段为 0 时透传到 go-redis 让其内部默认生效（SDK 默认：PoolSize=10×GOMAXPROCS,
// DialTimeout=5s, ReadTimeout=3s, WriteTimeout=3s）。本配置主要给共享 redis 多服务
// 部署时调小 PoolSize / 调大超时用。
type RedisConfig struct {
	Addr string `mapstructure:"addr"` // host:port
	// Password 经 LIUSHA_REDIS_PASSWORD 注入，不写进配置文件；空 = 无密码。
	Password            string `mapstructure:"password"`
	PoolSize            int    `mapstructure:"pool_size"`
	MinIdleConns        int    `mapstructure:"min_idle_conns"`
	DialTimeoutSeconds  int    `mapstructure:"dial_timeout_seconds"`
	ReadTimeoutSeconds  int    `mapstructure:"read_timeout_seconds"`
	WriteTimeoutSeconds int    `mapstructure:"write_timeout_seconds"`
}

// LLMConfig 是 LLM 复杂度路由 + retry 参数 + llm_invocation 异步 batch 写入参数。
//
// 复杂度三档（complex 深度推理 / medium 标准推理 / simple 快速响应）：
// agent → 档的绑定在 agent 生效配置的 complexity（出厂值 + 前端「智能体」页自定义）；
// 档 → provider 的绑定落 DB（前端「LLM 配置」页），此处 yaml 仅首次 insert-only 种子。
type LLMConfig struct {
	ComplexityRoutes map[string]string `mapstructure:"complexity_routes"` // 档(simple/medium/complex) → provider key

	Retry      RetryConfig      `mapstructure:"retry"`
	Invocation InvocationConfig `mapstructure:"invocation"`
}

// RetryConfig 控制 4 类错误的重试次数和 backoff schedule。
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
//	  ""              缺省值，seed 首填时补为 openai_compat（DB 约束只收上述两种取值）。
type ProviderConfig struct {
	Type         string `mapstructure:"type"`
	BaseURL      string `mapstructure:"base_url"`
	DefaultModel string `mapstructure:"default_model"`
	APIKeyEnv    string `mapstructure:"api_key_env"`
	// AuthScheme 是密钥的携带方式（bearer / x-api-key）；缺省取协议标准方式
	// （openai_compat → bearer，anthropic → x-api-key）。中转网关只认另一种时显式填写。
	AuthScheme string `mapstructure:"auth_scheme"`
	// OutputLimitParam 是 OpenAI 兼容协议声明输出上限的字段（max_tokens /
	// max_completion_tokens）；缺省 max_tokens，运行期遇上游明确拒绝会自适应切换。
	OutputLimitParam string `mapstructure:"output_limit_param"`
	MaxTokens        int    `mapstructure:"max_tokens"`
	SupportsTools    bool   `mapstructure:"supports_tools"`
	// SupportsVision 用 *bool 区分"未填"（nil）与"显式 false"——validate 强制 yaml 必填，
	// 避免 caller 不知道 provider 能不能 vision 时拿默认值踩坑（例如 deepseek 不支持 vision
	// 却收到含图 message → 服务端 400）。yaml `supports_vision: true/false` 都合法，留空启动报错。
	SupportsVision *bool `mapstructure:"supports_vision"`

	// ContextWindow 是 model 总上下文窗口（input + output 合计 tokens）。
	// 会话历史压缩按此值算阈值（trigger_ratio × ContextWindow）。
	// 同 SupportsVision 模式：*int 区分"未填"（nil）与"显式 0"——validate 强制必填，
	// 避免 caller 用默认值估算导致 prompt 真爆（例如 32k 模型按 128k 算阈值）。
	ContextWindow *int `mapstructure:"context_window"`

	// ThinkingBudgetTokens 是扩展思考预算（anthropic thinking.budget_tokens）。
	// >0 = 开启扩展思考（模型先流式产出内部 CoT 再产正文）；0/缺省 = 不开启。
	// 仅 anthropic 协议 provider 消费。
	ThinkingBudgetTokens int64 `mapstructure:"thinking_budget_tokens"`
}

// ProxyConfig 控制 in-process MITM 切片 + 责任链过滤 + 聚合参数 + 进程入口。
type ProxyConfig struct {
	AllowHosts              []string `mapstructure:"allow_hosts"`
	ExcludeMethods          []string `mapstructure:"exclude_methods"`
	ExcludeHosts            []string `mapstructure:"exclude_hosts"`
	ExcludeUpgradeProtocols []string `mapstructure:"exclude_upgrade_protocols"`
	ExcludeSuffixes         []string `mapstructure:"exclude_suffixes"`
	ExcludeContentTypes     []string `mapstructure:"exclude_content_types"`
	ExcludeStatusCodes      []int    `mapstructure:"exclude_status_codes"`
	MaxRequestBodySize      int      `mapstructure:"max_request_body_size"`
	MaxResponseBodySize     int      `mapstructure:"max_response_body_size"`

	// 组件入口：纯 MITM 代理入口。存活检测探 TCP 监听口，无独立 healthz HTTP。
	ListenAddr   string `mapstructure:"listen_addr"`   // 公开端口（sanitizer 接 raw TCP）
	InternalAddr string `mapstructure:"internal_addr"` // proxify 回环监听（sanitizer 转发到这）
	// CertDir 是 MITM 根证书目录（首次启动生成，客户端需信任其中的 CA）。空 = $HOME 下默认子目录。
	CertDir string `mapstructure:"cert_dir"`

	// Redis Stream（proxy → ingestor 之间）
	StreamName   string `mapstructure:"stream_name"`
	StreamMaxLen int    `mapstructure:"stream_max_len"`
}

// IngestorConfig 是 Stream 流量摄入器（runner 组件内 goroutine）参数。
type IngestorConfig struct {
	ConsumerGroup        string `mapstructure:"consumer_group"`
	ConsumerName         string `mapstructure:"consumer_name"`
	ReadBatch            int    `mapstructure:"read_batch"`
	ReadBlockTimeoutMs   int    `mapstructure:"read_block_timeout_ms"`
	RetryDelayMs         int    `mapstructure:"retry_delay_ms"`
	RecreateGroupDelayMs int    `mapstructure:"recreate_group_delay_ms"`

	// 流量聚合器参数：同 host 攒批建 task。
	// AggregateBatchSize：一批多少条 proxy_traffic 触发建 task（默认 20；~一个用户操作单元的 XHR 量级）。
	// AggregateWindowSeconds：距首条超此秒数即触发（默认 10；先到先触发）。
	AggregateBatchSize     int `mapstructure:"aggregate_batch_size"`
	AggregateWindowSeconds int `mapstructure:"aggregate_window_seconds"`
}

// CredentialConfig 是 credential.RedisProvider 的 redis key 前缀。
type CredentialConfig struct {
	RedisKeyPrefix string `mapstructure:"redis_key_prefix"`
}

// RunnerConfig 是 runner 组件的运行时参数。
type RunnerConfig struct {
	AgentRunTimeoutSeconds      int    `mapstructure:"agent_run_timeout_seconds"` // 单个 agent run 整体秒数上限（handler 入口 WithTimeout）；<=0 不限时
	StepLLMTimeoutSeconds       int    `mapstructure:"step_llm_timeout_seconds"`
	AsynqConcurrency            int    `mapstructure:"asynq_concurrency"`
	AsynqShutdownTimeoutSeconds int    `mapstructure:"asynq_shutdown_timeout_seconds"` // asynq.Shutdown 等 in-flight task 完成的超时
	ShutdownTimeoutSeconds      int    `mapstructure:"shutdown_timeout_seconds"`
	HealthzAddr                 string `mapstructure:"healthz_addr"` // healthz + 沙盒流量回灌端点的监听地址

	// asynq queue 优先级权重（数字越大优先级越高）
	QueueAgentWeight int `mapstructure:"queue_agent_weight"`

	// 僵尸 run 回收（runner 硬杀后自动重投 planner；详见 internal/runner/run_recovery.go）。
	// interval：扫描周期；timeout：任务级 LLM/工具活动停摆判定窗口（与探索图
	// action 恢复同口径）；max_attempts：单任务自动重投上限（含首次，超限落
	// aborted 防崩溃循环烧 LLM 账单）。<=0 用内置默认（30s / 6m / 4 次）。
	RunRecoveryIntervalSeconds int `mapstructure:"run_recovery_interval_seconds"`
	RunRecoveryTimeoutSeconds  int `mapstructure:"run_recovery_timeout_seconds"`
	RunRecoveryMaxAttempts     int `mapstructure:"run_recovery_max_attempts"`

	// MaxStepsPerTask 是单个 task 的最大执行步数（一次 agent 推理 = 一步）。
	// ≤ 0 = 不限制（默认）：持续探索模式的正解——收敛由完成检测器判定，步数只是
	// 可选护栏（防失控任务烧 LLM 账单），达到上限时任务自动终止。
	MaxStepsPerTask int `mapstructure:"max_steps_per_task"`
}

// SandboxConfig 是沙盒服务参数：沙盒自身的监听与鉴权、runner 的访问地址、浏览器与
// 工作区、对外回连契约。
//
// run_command 单次硬超时不在这里：LLM 调用时必传 timeout_seconds，上限由系统设置
// runtime.step_tool_timeout_seconds 钳制（前端「系统配置」可热改）。
type SandboxConfig struct {
	// Image 是 runner 经宿主 docker 拉起的沙盒镜像。
	// TODO(single-binary 第 3 批)：沙盒服务化后删除。
	Image string `mapstructure:"image"`

	// ListenAddr 是沙盒服务（liusha sandbox）的监听地址。
	ListenAddr string `mapstructure:"listen_addr"`

	// URL 是 runner 与 api 访问沙盒服务的地址。
	URL string `mapstructure:"url"`

	// Token 是 runner 与沙盒之间的共享密钥，两个方向都用它鉴权：runner 调沙盒的
	// /exec 等接口、沙盒内浏览器回灌流量到 runner 的 ingest 接口。必填，
	// 经环境变量 LIUSHA_SANDBOX_TOKEN 注入，不写进配置文件。
	Token string `mapstructure:"token"`

	// IngestURL 是沙盒内浏览器抓到的流量回灌到 runner 的地址
	// （runner 的 /internal/v1/traffic/ingest）。
	IngestURL string `mapstructure:"ingest_url"`

	// BrowserPython 是运行浏览器服务（browser-use）的 Python 解释器。沙盒镜像里是
	// 独立 venv；二进制模式用本机装了 browser-use 的 Python。
	BrowserPython string `mapstructure:"browser_python"`

	// ViewportWidth/Height 是浏览器视口固定尺寸（像素），每次启动 chromium 都带上，
	// 截图尺寸稳定。默认 1280×800：主流桌面宽度，也是视觉模型 token 消耗的甜点区。
	ViewportWidth  int `mapstructure:"viewport_width"`
	ViewportHeight int `mapstructure:"viewport_height"`

	// ListenerPorts 是沙盒对外监听段（"起-止"，含两端）：反弹 shell / 代理 /
	// payload 托管绑段内端口，目标经「回连地址:同号端口」可达。端口如何暴露由部署
	// 方式负责（compose 在 sandbox 服务上发布同号端口）；这里只告诉 agent 可用哪些端口。
	// 空 = 不提供对外监听。段须避开业务端口与内核临时端口段（Linux 32768+）。
	ListenerPorts string `mapstructure:"listener_ports"`

	// CallbackHost 是目标回连地址（payload 里写它）。空 = 自动探测默认路由源地址
	// （单网卡 / 同网段正确；NAT、多网卡、域名、容器化部署须显式配置）。
	CallbackHost string `mapstructure:"callback_host"`

	// WorkspaceDir 是任务工作区根目录（按 批次 / 任务 两级分目录）。沙盒在其中执行
	// 命令、写产物，api 在其中读写文件与附件——两者必须看到同一个目录。
	// 空 = ~/data/liusha/workspace。
	WorkspaceDir string `mapstructure:"workspace_dir"`

	// WorkspaceRetentionDays 是任务工作区的留存天数（按目录 mtime）。
	// 0 = 永久保留（持续探索模式的默认：工件是证据）；正数由沙盒定期清扫。
	WorkspaceRetentionDays int `mapstructure:"workspace_retention_days"`
}
