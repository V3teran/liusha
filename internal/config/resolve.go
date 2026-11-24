package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/framework/llm"
)

const mib = 1 << 20

// resolve 解析取值由运行环境决定的配置项：data_dir 的 ~/ 展开为当前用户主目录；
// sandbox.browser_python 写空串表示本机平台的惯用名。这是取值本身的语义（主目录、操作系统
// 因机器而异，没法在共用的模板里写死），不是缺键时的默认值——文件缺键在加载时就已报错。
func (c AppConfig) resolve() (AppConfig, error) {
	dir, err := expandHome(strings.TrimSpace(c.DataDir))
	if err != nil {
		return AppConfig{}, err
	}
	c.DataDir = dir
	if c.Sandbox.BrowserPython == "" {
		// Windows 的官方安装包只提供 python.exe；python3 在那里是应用商店的跳转桩。
		c.Sandbox.BrowserPython = "python3"
		if runtime.GOOS == "windows" {
			c.Sandbox.BrowserPython = "python"
		}
	}
	return c, nil
}

// expandHome 把 ~ 或 ~/… 展开为当前用户主目录下的路径，并清理成规范形式。
func expandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return filepath.Clean(p), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("data_dir 写的是 %q，但无法确定当前用户主目录：请写绝对路径", p)
	}
	return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/")), nil
}

// Options 把配置换算为框架的重试参数（api / runner 装配 Router 共用）。
func (c RetryConfig) Options() llm.RetryOptions {
	return llm.RetryOptions{
		MaxRetries429: c.Max429,
		MaxRetries529: c.Max529,
		MaxRetries5xx: c.Max5xx,
		MaxRetriesNet: c.MaxNet,
		Backoff429:    AsDurations(c.Backoff429Seconds),
		Backoff5xx:    AsDurations(c.Backoff5xxSeconds),
		BackoffNet:    AsDurations(c.BackoffNetSeconds),
		Backoff529:    time.Duration(c.Backoff529Ms) * time.Millisecond,
	}
}

// AsDurations 把 []int 秒数列表转 []time.Duration。
func AsDurations(secs []int) []time.Duration {
	out := make([]time.Duration, len(secs))
	for i, s := range secs {
		out[i] = time.Duration(s) * time.Second
	}
	return out
}

// validate 校验部署配置的取值（不碰密钥是否就位：各组件用到时检查）。
func validate(c AppConfig) error {
	if c.DataDir == "" || c.DataDir == "." || !filepath.IsAbs(c.DataDir) {
		return errors.New("data_dir 须为绝对路径或以 ~/ 开头（相对路径依赖进程工作目录，以服务方式运行时不可靠）")
	}
	if invalid := invalidValues(c); len(invalid) > 0 {
		return fmt.Errorf("配置项取值无效：%s", strings.Join(invalid, "；"))
	}
	return nil
}

// invalidValues 列出取值没有意义的键及原因。
func invalidValues(c AppConfig) []string {
	var invalid []string
	nonEmpty := func(key, v string) {
		if strings.TrimSpace(v) == "" {
			invalid = append(invalid, key+" 不能为空")
		}
	}
	positive := func(key string, v int64) {
		if v <= 0 {
			invalid = append(invalid, key+" 须大于 0")
		}
	}
	nonNegative := func(key string, v int64) {
		if v < 0 {
			invalid = append(invalid, key+" 不能为负数")
		}
	}
	oneOf := func(key, v string, allowed ...string) {
		for _, a := range allowed {
			if v == a {
				return
			}
		}
		invalid = append(invalid, fmt.Sprintf("%s 只能是 %s", key, strings.Join(allowed, " / ")))
	}

	oneOf("log.level", c.Log.Level, "debug", "info", "warn", "error")
	oneOf("log.format", c.Log.Format, "json", "console")
	if c.Log.MaxSizeBytes <= 0 || c.Log.MaxSizeBytes%mib != 0 {
		invalid = append(invalid, "log.max_size_bytes 须为 1 MiB（1048576）的正整数倍")
	}
	nonNegative("log.max_backups", int64(c.Log.MaxBackups))
	nonNegative("log.max_age_days", int64(c.Log.MaxAgeDays))

	nonEmpty("api.listen_addr", c.API.ListenAddr)
	positive("api.read_timeout_seconds", int64(c.API.ReadTimeoutSeconds))
	positive("api.write_timeout_seconds", int64(c.API.WriteTimeoutSeconds))
	positive("api.read_header_timeout_seconds", int64(c.API.ReadHeaderTimeoutSeconds))
	positive("api.shutdown_timeout_seconds", int64(c.API.ShutdownTimeoutSeconds))

	positive("postgres.max_conns", int64(c.Postgres.MaxConns))
	nonNegative("postgres.min_conns", int64(c.Postgres.MinConns))
	if c.Postgres.MinConns > c.Postgres.MaxConns {
		invalid = append(invalid, "postgres.min_conns 不能大于 postgres.max_conns")
	}
	positive("postgres.connect_timeout_seconds", int64(c.Postgres.ConnectTimeoutSeconds))
	positive("postgres.max_conn_lifetime_seconds", int64(c.Postgres.MaxConnLifetimeSeconds))

	positive("redis.pool_size", int64(c.Redis.PoolSize))
	nonNegative("redis.min_idle_conns", int64(c.Redis.MinIdleConns))
	positive("redis.dial_timeout_seconds", int64(c.Redis.DialTimeoutSeconds))
	positive("redis.read_timeout_seconds", int64(c.Redis.ReadTimeoutSeconds))
	positive("redis.write_timeout_seconds", int64(c.Redis.WriteTimeoutSeconds))

	nonNegative("llm.retry.max_429", int64(c.LLM.Retry.Max429))
	nonNegative("llm.retry.max_529", int64(c.LLM.Retry.Max529))
	nonNegative("llm.retry.max_5xx", int64(c.LLM.Retry.Max5xx))
	nonNegative("llm.retry.max_net", int64(c.LLM.Retry.MaxNet))
	nonNegative("llm.retry.backoff_529_ms", int64(c.LLM.Retry.Backoff529Ms))
	// 退避表逐次取值：表比重试次数短，多出的重试会不等待立即发出。
	for _, b := range []struct {
		key      string
		max      int
		schedule []int
	}{
		{"llm.retry.backoff_429_seconds", c.LLM.Retry.Max429, c.LLM.Retry.Backoff429Seconds},
		{"llm.retry.backoff_5xx_seconds", c.LLM.Retry.Max5xx, c.LLM.Retry.Backoff5xxSeconds},
		{"llm.retry.backoff_net_seconds", c.LLM.Retry.MaxNet, c.LLM.Retry.BackoffNetSeconds},
	} {
		if len(b.schedule) < b.max {
			invalid = append(invalid, fmt.Sprintf("%s 的项数（%d）少于对应的重试次数（%d）", b.key, len(b.schedule), b.max))
		}
		for _, s := range b.schedule {
			if s < 0 {
				invalid = append(invalid, b.key+" 不能含负数")
				break
			}
		}
	}
	nonNegative("llm.account_concurrency", int64(c.LLM.AccountConcurrency))
	nonNegative("llm.invocation_retention_days", int64(c.LLM.InvocationRetentionDays))
	nonNegative("tools.invocation_retention_days", int64(c.Tools.InvocationRetentionDays))
	nonNegative("traffic.retention_days", int64(c.Traffic.RetentionDays))

	nonEmpty("proxy.listen_addr", c.Proxy.ListenAddr)
	if a := c.Proxy.PublicAddr; a != "" && !validHostPort(a) {
		invalid = append(invalid, "proxy.public_addr 须为「主机:端口」或「:端口」")
	}
	positive("proxy.stream_max_len", int64(c.Proxy.StreamMaxLen))
	positive("proxy.max_request_body_bytes", int64(c.Proxy.MaxRequestBodyBytes))
	positive("proxy.max_response_body_bytes", int64(c.Proxy.MaxResponseBodyBytes))

	positive("ingestor.aggregate_batch_size", int64(c.Ingestor.AggregateBatchSize))
	positive("ingestor.aggregate_window_seconds", int64(c.Ingestor.AggregateWindowSeconds))

	nonEmpty("runner.listen_addr", c.Runner.ListenAddr)
	nonEmpty("runner.url", c.Runner.URL)
	positive("runner.read_header_timeout_seconds", int64(c.Runner.ReadHeaderTimeoutSeconds))
	positive("runner.shutdown_timeout_seconds", int64(c.Runner.ShutdownTimeoutSeconds))
	positive("runner.concurrency", int64(c.Runner.Concurrency))
	positive("runner.drain_timeout_seconds", int64(c.Runner.DrainTimeoutSeconds))
	nonNegative("runner.agent_run_timeout_seconds", int64(c.Runner.AgentRunTimeoutSeconds))
	nonNegative("runner.max_actions_per_task", int64(c.Runner.MaxActionsPerTask))
	positive("runner.step_llm_timeout_seconds", int64(c.Runner.StepLLMTimeoutSeconds))
	positive("runner.run_recovery_interval_seconds", int64(c.Runner.RunRecoveryIntervalSeconds))
	positive("runner.run_recovery_timeout_seconds", int64(c.Runner.RunRecoveryTimeoutSeconds))
	positive("runner.run_recovery_max_attempts", int64(c.Runner.RunRecoveryMaxAttempts))

	nonEmpty("sandbox.listen_addr", c.Sandbox.ListenAddr)
	nonEmpty("sandbox.url", c.Sandbox.URL)
	positive("sandbox.viewport_width", int64(c.Sandbox.ViewportWidth))
	positive("sandbox.viewport_height", int64(c.Sandbox.ViewportHeight))
	nonNegative("sandbox.workspace_retention_days", int64(c.Sandbox.WorkspaceRetentionDays))

	positive("conversation.attachment_max_bytes", c.Conversation.AttachmentMaxBytes)
	return invalid
}

// validHostPort 判断 addr 是「主机:端口」或「:端口」，端口为 1-65535。
func validHostPort(addr string) bool {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}
