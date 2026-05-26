// Package proxy hosts liusha 的 MITM 代理与 TrafficSnapshot 类型。
//
// 数据流（Stream-based 业界最佳实践）：
//
//	cmd/proxy onResponse → filter → publisher.Publish (XADD liusha:flow_events)
//	cmd/scanner ingestor → XREADGROUP → passive_session.LookupOrCreate(host) + flow.Append + 入 hunter 队列
//
// proxy 进程无状态、可水平扩展；passive_session 创建/轮转逻辑集中在 scanner 端。
package proxy

import "time"

// TrafficSnapshot 一条 HTTP 流量在 Redis Stream / 消费者侧的可序列化快照。
//
// owner 关联（0060+ / 0061 简化为 owner_id 单一标识）：
//
//	OwnerType / OwnerID  passive_session.id 或 active_scan.id（cmd/proxy 关联机制填）
//	                     external listener 按 host 查 passive_session；
//	                     internal listener 解析 Proxy-Auth user=owner_<uuid> 直接填，
//	                     owner_type 按 source 派生（external→passive_session / internal→active_scan）
//	Source               'external'（8888 入口）/ 'internal'（8890 入口，agent 工具发起）
//
// URI 拆解：
//
//	URI    保留原始字符串（含 query，重放时需要原顺序，对签名服务端友好）
//	Path   path 部分（不含 query），用作 dedup_key 模板化 + sniffer 路由判断
//	Query  parsed query 多值 map，sniffer 直接结构化操作（IDOR 改 user_id 等）
//
// 字段构成：
//
//	ID              全局唯一 id（uuid 等，由 proxy.Server 生成）
//	Host            host header（去端口）；finding/lesson/note 按 host 切分；passive_session 也 per-host
//	HostPort        host:port 原文（用于 fullURL 重放定位真实端口；空则由消费者退化到 Host）
//	Method          GET/POST/...
//	Scheme          http / https
//	URI             原始 path+query（保留顺序，重放/审计用）
//	Path            URI 去掉 query 部分
//	Query           url.ParseQuery 后的多值 map（无 query 时为 nil）
//	StatusCode      响应状态码
//	RequestHeaders  请求头（key 小写化；多值用 "," join — HTTP/1.1 请求头无多 header 行场景）
//	ResponseHeaders 响应头（key 小写化；保留 []string 多值切片，与 http.Header 原 shape 对齐，
//	                正确处理 Set-Cookie 等 RFC 6265 强制独立多行的 header）
//	RequestBody     请求体（已截断到 MaxRequestBodySize）
//	ResponseBody    响应体（已截断到 MaxResponseBodySize）
//	Duration        请求-响应耗时（proxify OnResponseCallback 算 wall time）
//	Timestamp       捕获时间（host 本地时钟，UTC）
type TrafficSnapshot struct {
	ID              string              `json:"id"`
	OwnerType       string              `json:"owner_type,omitempty"`
	OwnerID         string              `json:"owner_id,omitempty"`
	Source          string              `json:"source"`
	Host            string              `json:"host"`
	HostPort        string              `json:"host_port,omitempty"`
	Method          string              `json:"method"`
	Scheme          string              `json:"scheme"`
	URI             string              `json:"uri"`
	Path            string              `json:"path"`
	Query           map[string][]string `json:"query,omitempty"`
	StatusCode      int                 `json:"status_code"`
	RequestHeaders  map[string]string   `json:"request_headers,omitempty"`
	ResponseHeaders map[string][]string `json:"response_headers,omitempty"`
	RequestBody     []byte              `json:"request_body,omitempty"`
	ResponseBody    []byte              `json:"response_body,omitempty"`
	Duration        time.Duration       `json:"duration_ns,omitempty"`
	Timestamp       time.Time           `json:"timestamp"`
}
