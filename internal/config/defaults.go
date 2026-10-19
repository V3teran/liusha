package config

import (
	"fmt"
	"os"
	"time"
)

// ApplyDefaults 把零值配置项填为默认值（进程启动装配前调用一次）。
func (c *Config) ApplyDefaults() {
	c.API = applyAPIDefaults(c.API)
	c.Postgres = applyPostgresDefaults(c.Postgres)
	c.Redis = applyRedisDefaults(c.Redis)
	c.LLM = applyLLMDefaults(c.LLM)
	c.Proxy = applyProxyDefaults(c.Proxy)
	c.Ingestor = applyIngestorDefaults(c.Ingestor)
	c.PromptLimits = applyPromptLimitsDefaults(c.PromptLimits)
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

func applyPromptLimitsDefaults(c PromptLimitsConfig) PromptLimitsConfig {
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
	if c.AgentRunTimeoutSeconds == 0 {
		c.AgentRunTimeoutSeconds = 3600 // 60 分钟（agent_run 整体超时；> step_tool=1800，留 30min buffer 给 agent 主循环收尾）
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
	if c.PerHostConcurrency == 0 {
		c.PerHostConcurrency = 2 // 默认同 host 并发 ≤ 2；显式设 -1 可关限速
	}
	if c.MaxStepsPerTask == 0 {
		c.MaxStepsPerTask = 1000 // 默认单任务最多执行 1000 步；显式设 -1 可无限制
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
// 不再强制 complexity_routes.medium——否则空 yaml + 满 DB 的正常部署会被误判 fail-fast。
// providers 一旦非空则按老规矩校验（medium 档必填 + 各档 api_key_env 在 ENV 非空），保证种子可用。
