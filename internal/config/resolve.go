package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
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

// violation 是一条取值没有意义的配置项：key 是配置键，message 是原因。
type violation struct{ key, message string }

// validate 校验配置的取值（不碰密钥是否就位：各组件用到时检查）。view 非 nil 时只看它覆盖的键：
// 按视角加载的配置里其余键是零值，它们的取值由读它们的进程校验。
func validate(c AppConfig, view View) error {
	invalid := []violation{}
	if dir := c.DataDir; dir == "" || dir == "." || !filepath.IsAbs(dir) {
		invalid = append(invalid, violation{"data_dir", "须为绝对路径或以 ~/ 开头（相对路径依赖进程工作目录，以服务方式运行时不可靠）"})
	}
	invalid = append(invalid, invalidValues(c)...)
	if view != nil {
		invalid = slices.DeleteFunc(invalid, func(v violation) bool { return !view.covers(v.key) })
	}
	if len(invalid) == 0 {
		return nil
	}
	messages := make([]string, len(invalid))
	for i, v := range invalid {
		messages[i] = v.key + " " + v.message
	}
	return fmt.Errorf("配置项取值无效：%s", strings.Join(messages, "；"))
}

// invalidValues 列出取值没有意义的配置项及原因。
func invalidValues(c AppConfig) []violation {
	var invalid []violation
	add := func(key, message string) { invalid = append(invalid, violation{key, message}) }
	nonEmpty := func(key, v string) {
		if strings.TrimSpace(v) == "" {
			add(key, "不能为空")
		}
	}
	positive := func(key string, v int64) {
		if v <= 0 {
			add(key, "须大于 0")
		}
	}
	nonNegative := func(key string, v int64) {
		if v < 0 {
			add(key, "不能为负数")
		}
	}
	oneOf := func(key, v string, allowed ...string) {
		for _, a := range allowed {
			if v == a {
				return
			}
		}
		add(key, fmt.Sprintf("只能是 %s", strings.Join(allowed, " / ")))
	}

	oneOf("log.level", c.Log.Level, "debug", "info", "warn", "error")
	oneOf("log.format", c.Log.Format, "json", "console")
	if c.Log.MaxSizeBytes <= 0 || c.Log.MaxSizeBytes%mib != 0 {
		add("log.max_size_bytes", "须为 1 MiB（1048576）的正整数倍")
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
		add("postgres.min_conns", "不能大于 postgres.max_conns")
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
			add(b.key, fmt.Sprintf("的项数（%d）少于对应的重试次数（%d）", len(b.schedule), b.max))
		}
		for _, s := range b.schedule {
			if s < 0 {
				add(b.key, "不能含负数")
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
		add("proxy.public_addr", "须为「主机:端口」或「:端口」")
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
	positive("runner.action_parallelism", int64(c.Runner.ActionParallelism))
	positive("runner.verification_parallelism", int64(c.Runner.VerificationParallelism))
	positive("runner.monitor_interval_seconds", int64(c.Runner.MonitorIntervalSeconds))
	positive("runner.drain_timeout_seconds", int64(c.Runner.DrainTimeoutSeconds))
	nonNegative("runner.agent_run_timeout_seconds", int64(c.Runner.AgentRunTimeoutSeconds))
	nonNegative("runner.max_actions_per_task", int64(c.Runner.MaxActionsPerTask))

	positive("runtime.step_llm_timeout_seconds", int64(c.Runtime.StepLLMTimeoutSeconds))
	positive("runtime.step_tool_timeout_seconds", int64(c.Runtime.StepToolTimeoutSeconds))
	positive("runtime.run_tail_bytes", int64(c.Runtime.RunTailBytes))
	positive("runtime.tool_output_tail_bytes", int64(c.Runtime.ToolOutputTailBytes))
	for name, r := range map[string]float64{
		"compaction.planner_context_ratio": c.Compaction.PlannerContextRatio, "compaction.executor_context_ratio": c.Compaction.ExecutorContextRatio,
		"compaction.evaluator_context_ratio": c.Compaction.EvaluatorContextRatio, "compaction.monitor_context_ratio": c.Compaction.MonitorContextRatio,
	} {
		if r <= 0 || r > MaxContextRatio {
			add(name, fmt.Sprintf("须在 (0, %.1f] 之间（历史预算占模型上下文窗口的比例）", MaxContextRatio))
		}
	}
	positive("runner.run_recovery_interval_seconds", int64(c.Runner.RunRecoveryIntervalSeconds))
	if v := int64(c.Runner.RunRecoveryTimeoutSeconds); v < MinRunRecoveryTimeoutSeconds {
		add("runner.run_recovery_timeout_seconds", fmt.Sprintf("须不小于 %d（存活心跳每 30 秒续约一次，判定阈值至少容得下三次续约），当前 %d",
			MinRunRecoveryTimeoutSeconds, v))
	}
	positive("runner.run_recovery_max_attempts", int64(c.Runner.RunRecoveryMaxAttempts))

	nonEmpty("sandbox.listen_addr", c.Sandbox.ListenAddr)
	nonEmpty("sandbox.url", c.Sandbox.URL)
	positive("sandbox.viewport_width", int64(c.Sandbox.ViewportWidth))
	positive("sandbox.viewport_height", int64(c.Sandbox.ViewportHeight))
	nonNegative("sandbox.workspace_retention_days", int64(c.Sandbox.WorkspaceRetentionDays))

	positive("conversation.attachment_max_bytes", c.Conversation.AttachmentMaxBytes)

	ws := c.WebSearch
	if ws.URL != "" && !httpURL(ws.URL) {
		add("web_search.url", "须为 http:// 或 https:// 开头的完整地址")
	}
	if ws.URL != "" {
		if h := ws.AuthHeader; h != "" && !validHeaderName(h) {
			add("web_search.auth_header", "须为合法的请求头名（如 X-Subscription-Token）")
		}
	}
	if ws.APIKey != "" && ws.URL == "" {
		add("web_search.api_key", "配了密钥但没有 web_search.url：检索不会启用")
	}
	if ws.AuthHeader != "" && ws.URL == "" {
		add("web_search.auth_header", "配了鉴权头名但没有 web_search.url：检索不会启用")
	}
	return invalid
}

// validHeaderName 报告 s 是不是合法的请求头名（RFC 9110 的 token：字母、数字与少数符号）。
// 启动时就报出写错的头名：运行期它只会表现为每次检索都失败，原因埋在工具输出里。
func validHeaderName(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)) {
			continue
		}
		return false
	}
	return s != ""
}

// httpURL 报告 s 是否是带主机的 http / https 地址。
func httpURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
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
