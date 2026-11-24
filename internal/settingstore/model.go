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
	MaxRequestBodyBytes     int      `json:"max_request_body_bytes"`    // 请求体切片上限字节
	MaxResponseBodyBytes    int      `json:"max_response_body_bytes"`   // 响应体切片上限字节
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
