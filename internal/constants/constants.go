// Package constants 定义项目级别的常量
//
// 本文件集中管理项目中的魔法数字和配置常量，提高代码可维护性和可读性。
package constants

import "time"

// 数据库操作超时
const (
	// DBQueryTimeout 数据库查询超时（简单查询）
	DBQueryTimeout = 5 * time.Second

	// DBQueryTimeoutLong 数据库查询超时（复杂查询）
	DBQueryTimeoutLong = 10 * time.Second

	// DBQueryTimeoutVeryLong 数据库查询超时（非常复杂的查询，如搜索）
	DBQueryTimeoutVeryLong = 30 * time.Second

	// DBTransactionTimeout 数据库事务超时
	DBTransactionTimeout = 10 * time.Second

	// DBBatchInsertTimeout 批量插入超时
	DBBatchInsertTimeout = 30 * time.Second
)

// 工具执行超时
const (
	// ToolTimeoutQuick 快速工具超时（读取操作）
	ToolTimeoutQuick = 5 * time.Second

	// ToolTimeoutMedium 中等工具超时（写入操作）
	ToolTimeoutMedium = 10 * time.Second

	// ToolTimeoutLong 长时间工具超时（复杂搜索）
	ToolTimeoutLong = 30 * time.Second

	// ToolTimeoutReplay HTTP 流量重放超时
	ToolTimeoutReplay = 60 * time.Second

	// ToolTimeoutCommand 命令执行超时
	ToolTimeoutCommand = 120 * time.Second

	// ToolTimeoutBrowser 浏览器操作超时
	ToolTimeoutBrowser = 180 * time.Second
)

// LLM 调用超时
const (
	// LLMRequestTimeout LLM 请求超时（默认）
	LLMRequestTimeout = 60 * time.Second

	// LLMRequestTimeoutLong LLM 请求超时（长文本）
	LLMRequestTimeoutLong = 120 * time.Second

	// LLMStreamTimeout LLM 流式响应超时
	LLMStreamTimeout = 180 * time.Second
)

// Agent 和任务超时
const (
	// AgentRunTimeout Agent 运行超时
	AgentRunTimeout = 30 * time.Minute

	// AgentStopTimeout Agent 停止超时
	AgentStopTimeout = 10 * time.Second

	// TaskExecutionTimeout 任务执行超时
	TaskExecutionTimeout = 2 * time.Hour

	// MonitorInterval Monitor Agent 检查间隔
	MonitorInterval = 6 * time.Minute

	// PlannerPollInterval Planner Agent 轮询间隔
	PlannerPollInterval = 10 * time.Second
)

// 缓冲区和通道大小
const (
	// ChannelBufferSmall 小缓冲区（用于低频事件）
	ChannelBufferSmall = 1

	// ChannelBufferMedium 中等缓冲区（用于一般事件）
	ChannelBufferMedium = 10

	// ChannelBufferLarge 大缓冲区（用于高频事件）
	ChannelBufferLarge = 32

	// ChannelBufferVeryLarge 超大缓冲区（用于流式数据）
	ChannelBufferVeryLarge = 100
)

// HTTP 和网络相关
const (
	// HTTPClientTimeout HTTP 客户端超时
	HTTPClientTimeout = 30 * time.Second

	// HTTPServerReadTimeout HTTP 服务器读超时
	HTTPServerReadTimeout = 15 * time.Second

	// HTTPServerWriteTimeout HTTP 服务器写超时
	HTTPServerWriteTimeout = 30 * time.Second

	// HTTPServerIdleTimeout HTTP 服务器空闲超时
	HTTPServerIdleTimeout = 60 * time.Second

	// WebSocketReadTimeout WebSocket 读超时
	WebSocketReadTimeout = 60 * time.Second

	// WebSocketWriteTimeout WebSocket 写超时
	WebSocketWriteTimeout = 10 * time.Second
)

// 重试和退避
const (
	// RetryMaxAttempts 最大重试次数
	RetryMaxAttempts = 3

	// RetryInitialDelay 初始重试延迟
	RetryInitialDelay = 100 * time.Millisecond

	// RetryMaxDelay 最大重试延迟
	RetryMaxDelay = 5 * time.Second

	// RetryBackoffMultiplier 退避倍数
	RetryBackoffMultiplier = 2.0
)

// 缓存相关
const (
	// CacheDefaultTTL 默认缓存 TTL
	CacheDefaultTTL = 5 * time.Minute

	// CacheLongTTL 长期缓存 TTL
	CacheLongTTL = 1 * time.Hour

	// CacheCleanupInterval 缓存清理间隔
	CacheCleanupInterval = 10 * time.Minute
)

// 日志和监控
const (
	// LogFlushInterval 日志刷新间隔
	LogFlushInterval = 1 * time.Second

	// MetricsCollectionInterval 指标收集间隔
	MetricsCollectionInterval = 30 * time.Second

	// HealthCheckInterval 健康检查间隔
	HealthCheckInterval = 10 * time.Second
)

// 并发控制
const (
	// MaxConcurrentTasks 最大并发任务数
	MaxConcurrentTasks = 10

	// MaxConcurrentExecutors 最大并发 Executor 数
	MaxConcurrentExecutors = 5

	// WorkerPoolSize Worker 池大小
	WorkerPoolSize = 20
)

// 限流和速率限制
const (
	// RateLimitDefault 默认速率限制（请求/秒）
	RateLimitDefault = 10

	// RateLimitLLM LLM 请求速率限制（请求/分钟）
	RateLimitLLM = 60

	// RateLimitBurst 突发请求数量
	RateLimitBurst = 20
)

// 分页和批处理
const (
	// PageSizeDefault 默认分页大小
	PageSizeDefault = 20

	// PageSizeMax 最大分页大小
	PageSizeMax = 100

	// BatchSizeDefault 默认批处理大小
	BatchSizeDefault = 50

	// BatchSizeMax 最大批处理大小
	BatchSizeMax = 1000
)

// 任务和执行限制
const (
	// MaxExecutionSteps 最大执行步数
	MaxExecutionSteps = 1000

	// MaxActionRetries Action 最大重试次数
	MaxActionRetries = 3

	// MaxObjectivesPerTask 每个任务最大目标数
	MaxObjectivesPerTask = 100
)

// 文件和存储
const (
	// MaxUploadSize 最大上传文件大小（字节）
	MaxUploadSize = 10 * 1024 * 1024 // 10MB

	// MaxLogFileSize 最大日志文件大小（字节）
	MaxLogFileSize = 100 * 1024 * 1024 // 100MB

	// TempFileTTL 临时文件 TTL
	TempFileTTL = 1 * time.Hour
)

// Context 超时（用于各种场景）
const (
	// ContextTimeoutDefault 默认 context 超时
	ContextTimeoutDefault = 30 * time.Second

	// ContextTimeoutShort 短超时（快速操作）
	ContextTimeoutShort = 5 * time.Second

	// ContextTimeoutLong 长超时（复杂操作）
	ContextTimeoutLong = 5 * time.Minute
)
