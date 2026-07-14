// Package flow 实现两张流量表的持久化层，按「流量的产生者」拆分（见 spec §5）：
//
//   - proxy_traffic（ProxyStore）：代理捕获的真实用户流量，被分析的输入，属于 host，
//     先于任何 task 存在。ingestor external 链路写入；passive traffic-analysis 读它。
//   - agent_traffic（AgentStore）：agent 在 sandbox 自产的流量，干活副产物/弹药，属于 task。
//     ingestor internal 链路写入（不触发分析，防自激震荡）；active 的 replay/list/view_flow
//     工具与 sitemap 投影读它。
//
// 两表唯一共同点是「长得像 HTTP 请求」——共享的持久化机制（body 截断、header 归一、
// URL 抽取）在 httputil.go，领域差异（归属、消费标记、身份戳）在各自 store。
//
// body 大字段在 Append 内按 maxReqBody / maxRespBody 截断（32 KiB）。
package flow

import (
	"encoding/json"
	"time"
)

// ProxyTraffic 是 proxy_traffic 表行——代理捕获流量，按 host 归属。
type ProxyTraffic struct {
	ID     int64
	Host   string // 归属轴（先于 task）
	Method string
	Scheme string
	URL    string
	Path   string
	// ConsumedByTaskID 是消费本条流量的 passive task；聚合成 task 时回填，未消费为空。
	ConsumedByTaskID string
	StatusCode       int
	RequestHeaders   json.RawMessage
	RequestBody      []byte
	ResponseHeaders  json.RawMessage
	ResponseBody     []byte
	DurationMs       int
	CapturedAt       time.Time
}

// AgentTraffic 是 agent_traffic 表行——agent 自产流量，按 task 归属。
type AgentTraffic struct {
	ID       int64
	TaskID   string
	HunterID string // 哪个 agent 发的（可空）
	Identity string // 身份戳（browser_use identity / 登录账号；CLI 为空）
	Tool     string // 工具戳（browser / curl / sqlmap…；external 为空）
	Host     string
	Method   string
	URL      string
	Path     string
	StatusCode      int
	RequestHeaders  json.RawMessage
	RequestBody     []byte
	ResponseHeaders json.RawMessage
	ResponseBody    []byte
	DurationMs      int
	CreatedAt       time.Time
}

// AgentSummary 是 agent_traffic 的瘦行（list_flows 用）：不含 body / headers，避免大 payload。
type AgentSummary struct {
	ID         int64
	TaskID     string
	HunterID   string
	Identity   string
	Tool       string
	Host       string
	Method     string
	URL        string
	Path       string
	StatusCode int
	DurationMs int
	CreatedAt  time.Time
}

// RouteRepr 是 DistinctRoutesWithRepresentative 派生的去重攻击面路由（host+method+path），
// 附一条代表 flow 的响应体片段（前 16KiB，含 <head>）。sitemap 投影用它派生攻击面 + 抽 <title>。
type RouteRepr struct {
	Host     string // 裸 host（聚合 key，对齐存储键）
	HostPort string // 真实 host:port（sitemap root 显示用）
	Method   string
	Path     string
	BodyHead []byte
}

// AgentListFilter 是 AgentStore.ListByTaskFiltered 的可选过滤条件；零值不过滤。
// Path 支持 glob（'*'→SQL '%'）。时间倒序（最新优先）。
type AgentListFilter struct {
	Host      string    // 等值
	Method    string    // 等值，自动 upper-case
	Path      string    // glob，支持 '*'
	Identity  string    // 身份名等值
	Tool      string    // 发起工具等值
	StatusMin int       // 状态码下界
	StatusMax int       // 状态码上界
	Since     time.Time // 仅看此后
	Limit     int       // 调用方控制 ≤ 200
	Offset    int       // 分页
}

// ProxyListFilter 是 ProxyStore.ListByTaskFiltered 的可选过滤条件；零值不过滤。
// proxy_traffic 无 identity/tool 戳（代理捕获无身份维度），故比 AgentListFilter 少那两字段。
type ProxyListFilter struct {
	Host      string
	Method    string
	Path      string
	StatusMin int
	StatusMax int
	Limit     int
	Offset    int
}
