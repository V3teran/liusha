// Package config 加载部署配置：配置文件（必须存在且列出全部键）→ LIUSHA_ 环境变量，再校验。
//
// 设计原则：
//   - 配置文件是唯一的取值来源：程序里不内置配置、不为缺键补值；缺键、未知键都拒绝启动。
//     唯一的例外是与配置文件可能不同版本的进程：它按自己读的范围加载（见 View），别的段不读不查
//   - 文件只写字面值；环境变量按键名覆盖（键 a.b_c ↔ LIUSHA_A_B_C），文件列出全部键，
//     因此每个键都能被覆盖。LIUSHA_ 命名空间里不对应配置键的变量（LIUSHA_CONFIG、.env 的部署变量、
//     开发工具参数）不得与配置键同名，见 envnames_test.go
//   - 只放部署方需要决定的事：拓扑（地址、路径）、容量与资源上限、可调的行为策略、密钥
//     （文件里留空，经环境变量注入）。组件之间的内部约定（队列、Stream、键名等）是代码常量
//   - 时长、大小、天数的键名以单位结尾：_seconds / _ms / _bytes / _days
package config

import "path/filepath"

// AppConfig 是 liusha 顶层配置树。
type AppConfig struct {
	// DataDir 是持久数据的根目录：任务工作区（workspace/）、日志（logs/）、MITM 根证书
	// （certs/）都在其下。支持 ~/ 开头（当前用户主目录），加载时解析为绝对路径。
	DataDir      string             `mapstructure:"data_dir"`
	Log          LogConfig          `mapstructure:"log"`
	API          APIConfig          `mapstructure:"api"`
	Admin        AdminConfig        `mapstructure:"admin"`
	Postgres     PostgresConfig     `mapstructure:"postgres"`
	Redis        RedisConfig        `mapstructure:"redis"`
	LLM          LLMConfig          `mapstructure:"llm"`
	Proxy        ProxyConfig        `mapstructure:"proxy"`
	Ingestor     IngestorConfig     `mapstructure:"ingestor"`
	Runner       RunnerConfig       `mapstructure:"runner"`
	Runtime      RuntimeConfig      `mapstructure:"runtime"`
	Compaction   CompactionConfig   `mapstructure:"compaction"`
	Sandbox      SandboxConfig      `mapstructure:"sandbox"`
	Conversation ConversationConfig `mapstructure:"conversation"`
	Tools        ToolsConfig        `mapstructure:"tools"`
	Traffic      TrafficConfig      `mapstructure:"traffic"`

	WebSearch WebSearchConfig `mapstructure:"web_search"`
}

// ToolsConfig 是工具调用记录的留存参数。
type ToolsConfig struct {
	// InvocationRetentionDays 是工具调用记录的留存天数；0 = 永久留存。
	// 记录是运行过程流水（诊断），按时间清理；任务证据与审计链不在此列。
	InvocationRetentionDays int `mapstructure:"invocation_retention_days"`
}

// WebSearchConfig 是公开网页检索的后端（web_search 工具用它取回网页结果）。
//
// 程序只认约定、不认服务：这里定的是「接到哪、怎么鉴权」，不定「是哪一家」。接一家搜索服务
// 通常只需填地址和密钥——两种事实上的响应惯例程序都认（见 internal/websearch）。
type WebSearchConfig struct {
	// URL 是搜索接口的地址：程序在它后面追加查询词 q 发起 GET。接口自带的固定参数（语言、
	// 条数……）写进地址里。空 = 不启用：web_search 工具不注册。
	URL string `mapstructure:"url"`
	// AuthHeader 是密钥放在哪个请求头里。空 = Authorization（值写成 Bearer <密钥>，标准做法）；
	// 接口要求别的头时填它的名字（如 X-Subscription-Token），此时头里放裸密钥。
	AuthHeader string `mapstructure:"auth_header"`
	// APIKey 是接口的鉴权密钥；接口不鉴权时留空。
	// 密钥，经 LIUSHA_WEB_SEARCH_API_KEY 注入。
	APIKey string `mapstructure:"api_key"`
}

// TrafficConfig 是流量记录的留存参数。
type TrafficConfig struct {
	// RetentionDays 是流量记录（代理捕获 + agent 自产，连同消费关联行）的留存天数；
	// 0 = 永久留存。两类流量同生命周期，一个键管两张表。
	RetentionDays int `mapstructure:"retention_days"`
}

// WorkspaceDir 是任务工作区根目录（按 批次 / 任务 两级分目录）。沙盒在其中执行命令、写产物，
// api 在其中读写文件与附件——两者必须看到同一个目录（多镜像部署以同一绝对路径挂进两个容器）。
func (c AppConfig) WorkspaceDir() string { return filepath.Join(c.DataDir, "workspace") }

// LogDir 是日志文件目录（每个进程一个 <命令>.log）。
func (c AppConfig) LogDir() string { return filepath.Join(c.DataDir, "logs") }

// CertDir 是 MITM 根证书目录（首次启动生成，客户端需信任其中的 CA）。
func (c AppConfig) CertDir() string { return filepath.Join(c.DataDir, "certs") }

// LogConfig 是日志输出参数。
type LogConfig struct {
	Level    string `mapstructure:"level"`     // debug / info / warn / error
	Format   string `mapstructure:"format"`    // 标准输出的格式：json / console（文件永远是 json）
	ToStdout bool   `mapstructure:"to_stdout"` // 写标准输出
	ToFile   bool   `mapstructure:"to_file"`   // 写 <data_dir>/logs/<命令>.log（控制台「服务器日志」页读它）
	// MaxSizeBytes 是单个日志文件的轮转大小，须为 1 MiB 的整数倍（按 MiB 轮转）。
	MaxSizeBytes int64 `mapstructure:"max_size_bytes"`
	MaxBackups   int   `mapstructure:"max_backups"`  // 保留的轮转文件数；0 = 不按个数清理
	MaxAgeDays   int   `mapstructure:"max_age_days"` // 轮转文件保留天数；0 = 不按天数清理
	Compress     bool  `mapstructure:"compress"`     // 轮转文件 gzip 压缩
}

// APIConfig 是 api 组件的 HTTP 入口参数。
type APIConfig struct {
	ListenAddr               string `mapstructure:"listen_addr"`
	ReadTimeoutSeconds       int    `mapstructure:"read_timeout_seconds"`
	WriteTimeoutSeconds      int    `mapstructure:"write_timeout_seconds"`
	ReadHeaderTimeoutSeconds int    `mapstructure:"read_header_timeout_seconds"`
	ShutdownTimeoutSeconds   int    `mapstructure:"shutdown_timeout_seconds"`
	// CookieSecure 给会话 Cookie 加 Secure 属性：经 HTTPS 访问时设 true；
	// 纯 http 访问必须 false，否则浏览器不保存会话。
	CookieSecure bool `mapstructure:"cookie_secure"`
}

// AdminConfig 是初始管理员。
type AdminConfig struct {
	// InitialPassword 是账号表为空时 api 创建 admin 所用的密码（8-72 字节），之后在控制台改密。
	// 密钥，经 LIUSHA_ADMIN_INITIAL_PASSWORD 注入。
	InitialPassword string `mapstructure:"initial_password"`
}

// PostgresConfig 是 PostgreSQL 连接与连接池参数。
type PostgresConfig struct {
	// URL 是连接串 postgres://用户:密码@主机:端口/库名?sslmode=...，含密码，经 LIUSHA_POSTGRES_URL 注入。
	URL                    string `mapstructure:"url"`
	MaxConns               int    `mapstructure:"max_conns"`
	MinConns               int    `mapstructure:"min_conns"` // 常驻暖连接：闲置后的请求不必付一次新拨号
	ConnectTimeoutSeconds  int    `mapstructure:"connect_timeout_seconds"`
	MaxConnLifetimeSeconds int    `mapstructure:"max_conn_lifetime_seconds"`
}

// RedisConfig 是 Redis 连接与连接池参数。
type RedisConfig struct {
	// URL 是连接串 redis://[用户名]:密码@主机:端口/库号（TLS 用 rediss://），含密码，经
	// LIUSHA_REDIS_URL 注入。
	URL                 string `mapstructure:"url"`
	PoolSize            int    `mapstructure:"pool_size"`
	MinIdleConns        int    `mapstructure:"min_idle_conns"`
	DialTimeoutSeconds  int    `mapstructure:"dial_timeout_seconds"`
	ReadTimeoutSeconds  int    `mapstructure:"read_timeout_seconds"`
	WriteTimeoutSeconds int    `mapstructure:"write_timeout_seconds"`
}

// LLMConfig 是调用 LLM 的部署参数。LLM 接入（provider）与档位路由是用户数据，在前端「模型」
// 页维护、存于数据库，不在配置文件里。
type LLMConfig struct {
	// EncryptionKey 加密数据库里保存的 provider 密钥（AES-256-GCM），32 字节 hex（64 个字符）。
	// 密钥，经 LIUSHA_LLM_ENCRYPTION_KEY 注入；生成后不可更改，否则已保存的密钥无法解密。
	EncryptionKey string `mapstructure:"encryption_key"`
	// AccountConcurrency 是每个模型账户同时在途的请求数上限（同一服务地址 + 同一密钥为一个
	// 账户，各 provider、各任务、各 agent 共用）；0 = 不限。限流随在途请求数而来：超出上限的
	// 调用在队列里等名额，不发出去被拒。
	AccountConcurrency int         `mapstructure:"account_concurrency"`
	Retry              RetryConfig `mapstructure:"retry"`
	// InvocationRetentionDays 是 LLM 调用记录（含完整 prompt / response）的留存天数；0 = 永久留存。
	InvocationRetentionDays int `mapstructure:"invocation_retention_days"`
}

// RetryConfig 控制 4 类错误（429 / 529 / 5xx / 网络超时）的重试次数和退避间隔。
type RetryConfig struct {
	Max429            int   `mapstructure:"max_429"`
	Max529            int   `mapstructure:"max_529"`
	Max5xx            int   `mapstructure:"max_5xx"`
	MaxNet            int   `mapstructure:"max_net"`
	Backoff429Seconds []int `mapstructure:"backoff_429_seconds"`
	Backoff5xxSeconds []int `mapstructure:"backoff_5xx_seconds"`
	BackoffNetSeconds []int `mapstructure:"backoff_net_seconds"`
	Backoff529Ms      int   `mapstructure:"backoff_529_ms"`
}

// ProxyConfig 是 MITM 代理入口与流量过滤规则的部署默认（过滤规则可在前端「系统」自定义）。
type ProxyConfig struct {
	ListenAddr string `mapstructure:"listen_addr"`
	// PublicAddr 是客户端设置代理时填写的地址，控制台「代理接入」显示它：「主机:端口」，或只写
	// 「:端口」表示主机即控制台所在的主机。空 = 由 listen_addr 推出（监听全部网卡时只取端口，
	// 否则即监听地址）。容器部署时代理端口映射到宿主的另一个端口，须显式配置。
	PublicAddr string `mapstructure:"public_addr"`
	// StreamMaxLen 是代理流量在 Redis 中等待摄入的缓冲上限（条，近似裁剪）。
	StreamMaxLen int `mapstructure:"stream_max_len"`

	AllowHosts              []string `mapstructure:"allow_hosts"`
	ExcludeHosts            []string `mapstructure:"exclude_hosts"`
	ExcludeMethods          []string `mapstructure:"exclude_methods"`
	ExcludeUpgradeProtocols []string `mapstructure:"exclude_upgrade_protocols"`
	ExcludeSuffixes         []string `mapstructure:"exclude_suffixes"`
	ExcludeContentTypes     []string `mapstructure:"exclude_content_types"`
	ExcludeStatusCodes      []int    `mapstructure:"exclude_status_codes"`
	MaxRequestBodyBytes     int      `mapstructure:"max_request_body_bytes"`
	MaxResponseBodyBytes    int      `mapstructure:"max_response_body_bytes"`
}

// IngestorConfig 是代理流量攒批建任务的参数（runner 内的流量摄入）：同主机的流量攒够条数或
// 距首条超过窗口，先到先触发，建一个代理捕获任务。
type IngestorConfig struct {
	AggregateBatchSize     int `mapstructure:"aggregate_batch_size"`
	AggregateWindowSeconds int `mapstructure:"aggregate_window_seconds"`
}

// RuntimeConfig 是工具与模型调用的运行时旋钮（前端「系统配置 · 运行时」那一组）。
//
// 这里是它的出厂值，也是唯一的一处：配置键与设置页上的字段同名（json 标签即设置文档的字段名，
// 设置页的自定义以这份出厂值为底）。所以这个结构体同时带 mapstructure 与 json 两种标签。
type RuntimeConfig struct {
	// StepLLMTimeoutSeconds 是单步 LLM 看门狗：一次模型调用的上限，挂死的调用即失败。规划一整轮的
	// 预算、压缩链的时限都由它推导。
	StepLLMTimeoutSeconds int `mapstructure:"step_llm_timeout_seconds" json:"step_llm_timeout_seconds"`
	// StepToolTimeoutSeconds 是 command_run 超时的上限（模型自传的 timeout_seconds 不得超过它）。
	StepToolTimeoutSeconds int `mapstructure:"step_tool_timeout_seconds" json:"step_tool_timeout_seconds"`
	// ToolOutputWindowBytes 是单条工具结果进模型上下文的窗口（字节）：超过时留开头与结尾各一半，
	// 中间省略并写明省掉多少。工具自己不裁剪，这是工具结果进上下文前唯一的尺寸关口；一步里各条
	// 结果的合计另有限额（至多相当于四条顶格的结果）。不得超过 MaxToolOutputWindowBytes。
	ToolOutputWindowBytes int `mapstructure:"tool_output_window_bytes" json:"tool_output_window_bytes"`
}

// MaxToolOutputWindowBytes 是单条工具结果窗口的上限：单条消息的硬上限（运行时的长度哨兵，按字节
// 计）。窗口之内的结果连说明在内不超过窗口，所以窗口不超过它就撞不上哨兵。config 不引用运行时，
// 两者的关系由 internal/runner 的测试守住。
const MaxToolOutputWindowBytes = 100000

// CompactionConfig 是各角色会话历史的预算占比（前端「系统配置 · 会话历史压缩」那一组）：
// 预算 = 比例 × 本角色所用模型的上下文窗口，超出预算的历史前缀被裁并蒸馏成纪要。比例越大上下文
// 越完整、token 越贵。出厂值只在这里；配置键与设置页上的字段同名。
type CompactionConfig struct {
	PlannerContextRatio   float64 `mapstructure:"planner_context_ratio" json:"planner_context_ratio"`
	ExecutorContextRatio  float64 `mapstructure:"executor_context_ratio" json:"executor_context_ratio"`
	EvaluatorContextRatio float64 `mapstructure:"evaluator_context_ratio" json:"evaluator_context_ratio"`
	MonitorContextRatio   float64 `mapstructure:"monitor_context_ratio" json:"monitor_context_ratio"`
}

// MaxContextRatio 是历史预算占比的上限：必须给模型输出与 system 开销留余地，预算贴满上下文
// 窗口等于把压缩推迟到 provider 报超限。
const MaxContextRatio = 0.9

// MinRunRecoveryTimeoutSeconds 是 runner.run_recovery_timeout_seconds 的下界：run 的存活心跳每
// 30 秒续约一次（internal/runner 的 heartbeatInterval），判定阈值至少要容得下三次续约，否则健康
// 运行的 run 也会在两次续约之间被当成僵尸。internal/runner 的测试守住这层关系。
const MinRunRecoveryTimeoutSeconds = 90

// RunnerConfig 是 runner 组件的运行参数。
type RunnerConfig struct {
	// ListenAddr 是 runner 的 HTTP 监听地址（healthz 与沙盒浏览器流量回灌）。
	ListenAddr string `mapstructure:"listen_addr"`
	// URL 是沙盒访问 runner 的地址（回灌流量），与 sandbox.url 对称。
	URL                      string `mapstructure:"url"`
	ReadHeaderTimeoutSeconds int    `mapstructure:"read_header_timeout_seconds"`
	ShutdownTimeoutSeconds   int    `mapstructure:"shutdown_timeout_seconds"` // HTTP 服务停机等待

	// Concurrency 是同时执行的任务数。
	Concurrency int `mapstructure:"concurrency"`
	// ActionParallelism 是一个任务同时执行的动作数上限；1 = 串行。
	ActionParallelism int `mapstructure:"action_parallelism"`
	// VerificationParallelism 是评估者同时验证的假设数上限。
	VerificationParallelism int `mapstructure:"verification_parallelism"`
	// MonitorIntervalSeconds 是监察者两次全局评估之间的间隔。
	MonitorIntervalSeconds int `mapstructure:"monitor_interval_seconds"`
	// DrainTimeoutSeconds 是停机时等在途 run 中断收尾（终态回写、释放沙盒）的上限。
	DrainTimeoutSeconds int `mapstructure:"drain_timeout_seconds"`

	// AgentRunTimeoutSeconds 是单次 run 的墙钟上限；0 = 不限（收敛交给完成检测）。
	AgentRunTimeoutSeconds int `mapstructure:"agent_run_timeout_seconds"`
	// MaxActionsPerTask 是动作预算：一个任务最多执行多少个动作（被认领执行过即算）；0 = 不限。
	MaxActionsPerTask int `mapstructure:"max_actions_per_task"`

	// 僵尸 run 回收（详见 internal/runner/run_recovery.go 与 heartbeat.go）：巡检周期；一轮 run 的
	// 存活心跳多久没有续约判为僵尸；单任务自动重投上限（按出错的 run 计数，超限任务终止，防
	// 崩溃循环烧 LLM 额度）。
	RunRecoveryIntervalSeconds int `mapstructure:"run_recovery_interval_seconds"`
	RunRecoveryTimeoutSeconds  int `mapstructure:"run_recovery_timeout_seconds"`
	RunRecoveryMaxAttempts     int `mapstructure:"run_recovery_max_attempts"`
}

// SandboxConfig 是沙盒服务参数：沙盒自身的监听与鉴权、浏览器、工作区留存、对外回连契约。
//
// command_run 单次硬超时不在这里：LLM 调用时必传 timeout_seconds，上限由系统设置
// runtime.step_tool_timeout_seconds 钳制（前端「系统」可热改）。
type SandboxConfig struct {
	ListenAddr string `mapstructure:"listen_addr"`
	// URL 是 runner 与 api 访问沙盒的地址。
	URL string `mapstructure:"url"`
	// Token 是沙盒与 runner / api 之间的共享密钥，两个方向都用它鉴权：调用沙盒接口、沙盒回灌
	// 流量到 runner。密钥，经 LIUSHA_SANDBOX_TOKEN 注入。
	Token string `mapstructure:"token"`

	// Shell 是执行 agent 命令的 POSIX shell（命令按 sh 语法编写，全平台同一种命令语言），名字
	// 或绝对路径。空 = 在本机查找：PATH 中的 sh；Windows 没有自带的 sh，再找 Git for Windows 的
	// sh.exe（见 internal/app/sandbox 的 resolveShell）。
	Shell string `mapstructure:"shell"`
	// BrowserPython 是运行浏览器服务（browser-use）的 Python 解释器。空 = 本机平台的惯用名
	// （python3；Windows 为 python）。
	BrowserPython string `mapstructure:"browser_python"`
	// ViewportWidth/Height 是浏览器视口固定尺寸（像素），截图尺寸稳定。
	ViewportWidth  int `mapstructure:"viewport_width"`
	ViewportHeight int `mapstructure:"viewport_height"`

	// WorkspaceRetentionDays 是任务工作区的留存天数（按目录 mtime）；0 = 永久保留（工件是证据）。
	WorkspaceRetentionDays int `mapstructure:"workspace_retention_days"`

	// ListenerPorts 是沙盒对外监听段（"起-止"，含两端）：反弹 shell / 代理 / payload 托管绑段内
	// 端口，目标经「回连地址:同号端口」可达。端口如何暴露由部署方式负责；这里只告诉 agent
	// 可用哪些端口。空 = 不提供对外监听。
	ListenerPorts string `mapstructure:"listener_ports"`
	// CallbackHost 是目标回连地址（payload 里写它）。空 = 探测本机默认路由的源地址（单网卡 /
	// 同网段正确；NAT、多网卡、域名、容器部署须显式配置）。
	CallbackHost string `mapstructure:"callback_host"`
}

// ConversationConfig 是会话参数。
type ConversationConfig struct {
	// AttachmentMaxBytes 是单附件上传上限（multipart 流式拷贝按此截断）。
	AttachmentMaxBytes int64 `mapstructure:"attachment_max_bytes"`
}
