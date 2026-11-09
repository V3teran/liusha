package settingstore

// Group 是设置分组，取值与 setting_override.group_key 的 CHECK 约束一一对应。
type Group string

const (
	GroupCompaction  Group = "compaction"   // 会话历史压缩
	GroupRuntime     Group = "runtime"      // 工具运行时
	GroupProxyFilter Group = "proxy_filter" // 代理流量过滤规则
)

// Groups 是全部分组（固定顺序）。
var Groups = []Group{GroupCompaction, GroupRuntime, GroupProxyFilter}

// CompactionSettings 是 agent 上下文压缩旋钮：ReAct 循环按「历史 token 预算」压
// 上下文——预算 = 比例 × 本角色路由 provider 的 context_window，超预算的历史前缀
// 被裁并蒸馏成纪要。比例越大上下文越完整、token 越贵；按链路特点分角色配置
// （长推理链预算大、短裁决链预算小）。按 token 预算而非消息条数：条数是 token 的
// 粗糙代理，模型窗口差异下无法表达真实预算。
type CompactionSettings struct {
	PlannerContextRatio   float64 `json:"planner_context_ratio"`   // 规划链路历史预算占比
	ExecutorContextRatio  float64 `json:"executor_context_ratio"`  // 执行链路历史预算占比
	EvaluatorContextRatio float64 `json:"evaluator_context_ratio"` // 裁决链路历史预算占比
	MonitorContextRatio   float64 `json:"monitor_context_ratio"`   // 监察链路历史预算占比
}

const (
	// maxContextRatio 封顶历史预算占比：必须给模型输出与 system 开销留余地，
	// 预算贴满 context_window 等于把压缩推迟到 provider 报超限。
	maxContextRatio = 0.9
)

// normalized 把缺省（零值）比例补为内置默认、超封顶钳到 maxContextRatio——
// 只用于构造出厂默认值；用户提交的值在写入时校验，不做静默修正。
func (c CompactionSettings) normalized() CompactionSettings {
	c.PlannerContextRatio = clampRatio(c.PlannerContextRatio, 0.6)
	c.ExecutorContextRatio = clampRatio(c.ExecutorContextRatio, 0.5)
	c.EvaluatorContextRatio = clampRatio(c.EvaluatorContextRatio, 0.5)
	c.MonitorContextRatio = clampRatio(c.MonitorContextRatio, 0.5)
	return c
}

func clampRatio(v, fallback float64) float64 {
	if v <= 0 {
		return fallback
	}
	if v > maxContextRatio {
		return maxContextRatio
	}
	return v
}

// DefaultCompaction 返回内置默认的压缩预算配置。
func DefaultCompaction() CompactionSettings { return CompactionSettings{}.normalized() }

// RuntimeSettings 是工具运行时的活旋钮集合（前端「系统配置」可自定义、热生效；
// 出厂默认中仅 LLM 看门狗取 yaml runner.step_llm_timeout_seconds，其余用内置默认）。
type RuntimeSettings struct {
	StepToolTimeoutSeconds int `json:"step_tool_timeout_seconds"` // run_command 超时钳制上限秒（LLM 自传 timeout_seconds 的上界，注册表级超时同源）
	StepLLMTimeoutSeconds  int `json:"step_llm_timeout_seconds"`  // 单步 LLM 看门狗超时秒（挂死的调用即失败；runner 每次调用现读，热改无需重启）
	RunTailBytes           int `json:"run_tail_bytes"`            // run_command stdout/stderr 保留尾部字节数（输出末段是结果所在，头部横幅/噪音可弃）
	// ToolOutputTailBytes 是任意工具输出进 LLM 上下文前的全局尾部保留字节（运行时
	// 统一收口：个别工具内的领域化裁剪仍先行，本旋钮是最后一道兜底，保证任何单条
	// 工具结果都有界、不会撑爆上下文预算或撞长度校验哨兵弄死整轮）。
	ToolOutputTailBytes int `json:"tool_output_tail_bytes"`
}

// defaultToolOutputTailBytes 是工具输出全局钳制的内置默认。32KiB 对绝大多数
// 工具结果宽裕（run_command 等已有更紧的领域裁剪先行），又远小于长度校验哨兵。
const defaultToolOutputTailBytes = 32768

// normalized 把缺省（零值）字段补为内置默认——只用于构造出厂默认值。
func (r RuntimeSettings) normalized() RuntimeSettings {
	if r.StepToolTimeoutSeconds <= 0 {
		r.StepToolTimeoutSeconds = 1800
	}
	if r.StepLLMTimeoutSeconds <= 0 {
		r.StepLLMTimeoutSeconds = 300
	}
	if r.RunTailBytes <= 0 {
		r.RunTailBytes = 8192
	}
	if r.ToolOutputTailBytes <= 0 {
		r.ToolOutputTailBytes = defaultToolOutputTailBytes
	}
	return r
}

// DefaultRuntime 返回工具运行时的出厂默认（LLM 看门狗取部署值 stepLLMTimeoutSeconds，
// 其余字段用内置默认）。
func DefaultRuntime(stepLLMTimeoutSeconds int) RuntimeSettings {
	return RuntimeSettings{StepLLMTimeoutSeconds: stepLLMTimeoutSeconds}.normalized()
}

// ProxyFilterSettings 是代理流量过滤责任链规则（对应 config.ProxyConfig 的过滤字段）。
// 运行期改这里 → 各进程失效 → proxy 重建过滤链原子替换（真热改）。
type ProxyFilterSettings struct {
	AllowHosts              []string `json:"allow_hosts"`               // 非空则仅放行这些 host（白名单）
	ExcludeMethods          []string `json:"exclude_methods"`           // 排除的 HTTP 方法（OPTIONS/HEAD/CONNECT…）
	ExcludeHosts            []string `json:"exclude_hosts"`             // 排除的 host（黑名单）
	ExcludeUpgradeProtocols []string `json:"exclude_upgrade_protocols"` // 排除的 Upgrade 协议（websocket…）
	ExcludeSuffixes         []string `json:"exclude_suffixes"`          // 排除的 URL 后缀（.css/.js/图片…）
	ExcludeContentTypes     []string `json:"exclude_content_types"`     // 排除的 Content-Type
	ExcludeStatusCodes      []int    `json:"exclude_status_codes"`      // 排除的响应状态码
	MaxRequestBodySize      int      `json:"max_request_body_size"`     // 请求体切片上限字节
	MaxResponseBodySize     int      `json:"max_response_body_size"`    // 响应体切片上限字节
}

// normalized 把 nil 切片归一为空切片：yaml 缺省时为 nil，而过滤链构建、集合
// 自定义与前端编辑都期望空数组语义（无规则 ≠ 缺字段）。
func (p ProxyFilterSettings) normalized() ProxyFilterSettings {
	if p.AllowHosts == nil {
		p.AllowHosts = []string{}
	}
	if p.ExcludeMethods == nil {
		p.ExcludeMethods = []string{}
	}
	if p.ExcludeHosts == nil {
		p.ExcludeHosts = []string{}
	}
	if p.ExcludeUpgradeProtocols == nil {
		p.ExcludeUpgradeProtocols = []string{}
	}
	if p.ExcludeSuffixes == nil {
		p.ExcludeSuffixes = []string{}
	}
	if p.ExcludeContentTypes == nil {
		p.ExcludeContentTypes = []string{}
	}
	if p.ExcludeStatusCodes == nil {
		p.ExcludeStatusCodes = []int{}
	}
	return p
}
