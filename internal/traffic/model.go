// Package traffic 实现两张流量表的持久化层，按「流量的产生者」拆分（见 spec §5）：
//
//   - proxy_traffic（ProxyStore）：代理捕获的真实用户流量，被分析的输入，属于 host，
//     先于任何 task 存在。ingestor external 链路写入；passive traffic-analysis 读它。
//   - agent_traffic（AgentStore）：agent 在 sandbox 自产的流量，干活副产物/弹药，属于 task。
//     ingestor internal 链路写入（不触发分析，防自激震荡）；active 的 replay/list/view_traffic 工具读它。
//
// 两表唯一共同点是「长得像 HTTP 请求」——共享的持久化机制（body 截断、header 归一、
// URL 抽取）在 httputil.go，领域差异（归属、消费标记、身份戳）在各自 store。
//
// body 大字段在 Append 内按 maxReqBody / maxRespBody 截断（32 KiB）。
package traffic

import (
	"encoding/json"
	"time"
)

// ProxyTraffic 是 proxy_traffic 表行——代理捕获流量，按 host 归属。
//
// v0100+：报文改「raw 单一 canonical 源」——RequestRaw/ResponseRaw 存完整报文文本
// （request-line/status-line + 头 + body），展示直接吐、replay 从 raw 反解（http.ReadRequest），
// 不再拆 headers(jsonb)+body(bytea) 冗余存储。消费关系改多对多（traffic_task 关联表），
// 单条被哪些 task 消费见 ConsumedBy（GetByID 时 JOIN 装填；列表/弹药读不填）。
type ProxyTraffic struct {
	ID          int64
	Host        string // 归属轴（先于 task）
	Method      string
	Scheme      string
	URL         string
	Path        string
	StatusCode  int
	RequestRaw  []byte // 完整请求报文文本（request-line + 头 + body）
	ResponseRaw []byte // 完整响应报文文本（status-line + 头 + body，body 已解压/截断）
	ContentType string // 响应 Content-Type 主类型（前端 Pretty 判定）
	HTTPVersion string // 协议版本 token（HTTP/1.1 等）
	RespLen     int64  // 响应体字节数（已解压/截断后），前端「长度」列
	CapturedAt  time.Time
	// ConsumedBy 是消费本条流量的 passive task 列表（M:N）；仅 GetByID JOIN 装填，其余读为空。
	ConsumedBy []ConsumerTask
}

// ConsumerTask 是消费某条代理流量的 passive task 摘要（前端消费关系 chip 展示用）。
type ConsumerTask struct {
	TaskID     string
	Host       string
	Status     string
}

// AgentTraffic 是 agent_traffic 表行——agent 自产流量，按 task 归属。
type AgentTraffic struct {
	ID              int64
	TaskID          string
	AgentID         string // 哪个 agent 发的（可空）
	Identity        string // 身份戳（browser_use identity / 登录账号；CLI 为空）
	Tool            string // 工具戳（browser / curl / sqlmap…；external 为空）
	Host            string
	Method          string
	URL             string
	Path            string
	StatusCode      int
	RequestHeaders  json.RawMessage
	RequestBody     []byte
	ResponseHeaders json.RawMessage
	ResponseBody    []byte
	DurationMs      int
	CreatedAt       time.Time
}

// AgentSummary 是 agent_traffic 的瘦行（list_traffic 用）：不含 body / headers，避免大 payload。
type AgentSummary struct {
	ID         int64
	TaskID     string
	AgentID    string
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
//
// 前端全局浏览新增维度（task 内检索不用，保持零值）：
//   - ContentType：响应 Content-Type 主类型等值（下拉 facet）
//   - Search     ：host OR url 子串通配（'*'→'%'，大小写不敏感），一个搜索框跨两列
//   - Since/Until：captured_at 闭区间（任一为零值则该端不限），驱动时间范围搜索
type ProxyListFilter struct {
	Host        string
	Method      string
	Path        string
	ContentType string
	Search      string
	StatusMin   int
	StatusMax   int
	Since       time.Time
	Until       time.Time
	Limit       int
	Offset      int
}
