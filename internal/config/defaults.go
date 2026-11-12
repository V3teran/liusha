package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// ApplyDefaults 把零值配置项填为默认值（进程启动装配前调用一次）。
func (c *AppConfig) ApplyDefaults() {
	c.API = applyAPIDefaults(c.API)
	c.Postgres = applyPostgresDefaults(c.Postgres)
	c.Redis = applyRedisDefaults(c.Redis)
	c.LLM = applyLLMDefaults(c.LLM)
	c.Proxy = applyProxyDefaults(c.Proxy)
	c.Ingestor = applyIngestorDefaults(c.Ingestor)
	c.Credential = applyCredentialDefaults(c.Credential)
	c.Runner = applyRunnerDefaults(c.Runner)
	c.Sandbox = applySandboxDefaults(c.Sandbox)
	c.Conversation = applyConversationDefaults(c.Conversation)
}

// DefaultAttachmentMaxBytes 是单附件上传上限默认值（100 MiB：大 pcap 与
// 字典文件不设卡，误传大文件的代价仍可控）。
const DefaultAttachmentMaxBytes = 100 << 20

func applyConversationDefaults(c ConversationConfig) ConversationConfig {
	if c.AttachmentMaxBytes <= 0 {
		c.AttachmentMaxBytes = DefaultAttachmentMaxBytes
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
		c.ListenAddr = "0.0.0.0:8580"
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

// applyRedisDefaults 只补地址；数值字段保持 0，让 go-redis SDK 用其内部默认值。
func applyRedisDefaults(c RedisConfig) RedisConfig {
	if c.Addr == "" {
		c.Addr = "127.0.0.1:6379"
	}
	return c
}

func applyLLMDefaults(c LLMConfig) LLMConfig {
	c.Retry = applyRetryDefaults(c.Retry)
	c.Invocation = applyInvocationDefaults(c.Invocation)
	return c
}

// DefaultLLMRetry 是缺省退避策略：由框架内置表（llm.DefaultRetryOptions）换算为配置
// 单位——默认值只有一处来源，配置层不另持一份。
func DefaultLLMRetry() RetryConfig {
	d := llm.DefaultRetryOptions()
	return RetryConfig{
		Max429:             d.MaxRetries429,
		Max529:             d.MaxRetries529,
		Max5xx:             d.MaxRetries5xx,
		MaxNet:             d.MaxRetriesNet,
		Backoff429Seconds:  asSeconds(d.Backoff429),
		Backoff5xxSeconds:  asSeconds(d.Backoff5xx),
		BackoffNetSeconds:  asSeconds(d.BackoffNet),
		Backoff529Millisec: int(d.Backoff529 / time.Millisecond),
	}
}

// Options 把配置换算为框架的重试参数（api / runner / corpus-import 装配 Router 共用）。
func (c RetryConfig) Options() llm.RetryOptions {
	return llm.RetryOptions{
		MaxRetries429: c.Max429,
		MaxRetries529: c.Max529,
		MaxRetries5xx: c.Max5xx,
		MaxRetriesNet: c.MaxNet,
		Backoff429:    AsDurations(c.Backoff429Seconds),
		Backoff5xx:    AsDurations(c.Backoff5xxSeconds),
		BackoffNet:    AsDurations(c.BackoffNetSeconds),
		Backoff529:    time.Duration(c.Backoff529Millisec) * time.Millisecond,
	}
}

func asSeconds(ds []time.Duration) []int {
	out := make([]int, len(ds))
	for i, d := range ds {
		out[i] = int(d / time.Second)
	}
	return out
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
		c.ListenAddr = "0.0.0.0:8588"
	}
	if c.InternalAddr == "" {
		c.InternalAddr = "127.0.0.1:18888"
	}
	if c.StreamName == "" {
		c.StreamName = "liusha:traffic_snapshots"
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

func applyCredentialDefaults(c CredentialConfig) CredentialConfig {
	if c.RedisKeyPrefix == "" {
		c.RedisKeyPrefix = "credentials:"
	}
	return c
}

func applyRunnerDefaults(c RunnerConfig) RunnerConfig {
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
		c.HealthzAddr = "0.0.0.0:8590"
	}
	if c.QueueAgentWeight == 0 {
		c.QueueAgentWeight = 5
	}
	return c
}

func applySandboxDefaults(c SandboxConfig) SandboxConfig {
	if c.ListenAddr == "" {
		c.ListenAddr = "127.0.0.1:8591"
	}
	if c.URL == "" {
		c.URL = "http://127.0.0.1:8591"
	}
	if c.IngestURL == "" {
		c.IngestURL = "http://127.0.0.1:8590/internal/v1/traffic/ingest"
	}
	if c.BrowserPython == "" {
		c.BrowserPython = "python3"
	}
	// 视口 1280×800——主流桌面宽度 + 视觉模型 token 消耗甜点区；高分屏可覆盖。
	if c.ViewportWidth == 0 {
		c.ViewportWidth = 1280
	}
	if c.ViewportHeight == 0 {
		c.ViewportHeight = 800
	}
	if c.WorkspaceDir == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			c.WorkspaceDir = filepath.Join(home, "data", "liusha", "workspace")
		}
	}
	return c
}

// validate 校验部署配置的完整性（不碰密钥是否就位）。
func validate(c AppConfig) error {
	if c.Sandbox.WorkspaceDir == "" {
		return errors.New("sandbox.workspace_dir 未配置，且无法确定用户主目录作为默认值")
	}
	return validateProviders(c)
}

// validateProviders 校验 provider schema 完整性（supports_vision / context_window 必填）——
// 不碰 LLM key，所有进程（含纯 ingress 的 proxy）都跑。
func validateProviders(c AppConfig) error {
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
