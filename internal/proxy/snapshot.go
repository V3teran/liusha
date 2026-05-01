// Package proxy hosts liusha 的 MITM 代理与 TrafficSnapshot 类型。
//
// 数据流（Stream-based 业界最佳实践）：
//
//	cmd/proxy onResponse → filter → publisher.Publish (XADD liusha:flow_events)
//	cmd/scanner flowconsumer → XREADGROUP → eng.LookupOrCreate + flow.Append + window.OpenOrAppend → enqueue sniffer
//
// proxy 进程无状态、可水平扩展；窗口逻辑集中在消费者。
package proxy

import "time"

// TrafficSnapshot 一条 HTTP 流量在 Redis Stream / 消费者侧的可序列化快照。
//
// 字段构成：
//
//	ID              全局唯一 id（uuid 等，由 proxy.Server 生成）
//	Host            host header（去端口，用作 engagement 索引：scope_host）
//	HostPort        host:port 原文（用于 fullURL 重放定位真实端口；空则由消费者退化到 Host）
//	Method          GET/POST/...
//	Scheme          http / https
//	URI             含 query 的完整 path
//	StatusCode      响应状态码
//	RequestHeaders  请求头（key 小写化；多值用 "," join — HTTP/1.1 请求头无多 header 行场景）
//	ResponseHeaders 响应头（key 小写化；保留 []string 多值切片，与 http.Header 原 shape 对齐，
//	                正确处理 Set-Cookie 等 RFC 6265 强制独立多行的 header）
//	RequestBody     请求体（已截断到 MaxRequestBodySize）
//	ResponseBody    响应体（已截断到 MaxResponseBodySize）
//	Timestamp       捕获时间（host 本地时钟，UTC）
type TrafficSnapshot struct {
	ID              string              `json:"id"`
	Host            string              `json:"host"`
	HostPort        string              `json:"host_port,omitempty"`
	Method          string              `json:"method"`
	Scheme          string              `json:"scheme"`
	URI             string              `json:"uri"`
	StatusCode      int                 `json:"status_code"`
	RequestHeaders  map[string]string   `json:"request_headers,omitempty"`
	ResponseHeaders map[string][]string `json:"response_headers,omitempty"`
	RequestBody     []byte              `json:"request_body,omitempty"`
	ResponseBody    []byte              `json:"response_body,omitempty"`
	Timestamp       time.Time           `json:"timestamp"`
}
