// Package proxy hosts liusha 的 MITM 代理与 TrafficSnapshot 类型。
//
// 数据流（Stream-based 业界最佳实践）：
//
//	cmd/proxy onResponse → filter → publisher.Publish (XADD liusha:flow_events)
//	cmd/runner ingestor → XREADGROUP → proxy_traffic 落库(按 host) + 聚合器攒批建 passive task + 入 agent 队列
//
// proxy 进程无状态、可水平扩展；proxy_traffic 落库 + passive task 生成逻辑集中在 runner 端。
package proxy

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// TrafficSnapshot 一条 HTTP 流量在 Redis Stream / 消费者侧的可序列化快照。
//
// 身份关联：
//
//	ExecutorID             internal 入口填（sandbox 自产流量归属的 agent run；ingestor 反查 task_id）。
//	                     external（passive）入口为空。
//	Source               'external'（8888 passive 入口）/ 'internal'（sandbox 自产）。
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
//	Host            host header（去端口）；finding/lesson/note 按 host 切分；proxy_traffic 也 per-host
//	HostPort        host:port 原文（用于 fullURL 重放定位真实端口；空则由消费者退化到 Host）
//	Method          GET/POST/...
//	Scheme          http / https
//	HTTPVersion     协议版本 token（HTTP/1.1 等），忠实重建 request/status line
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
	ExecutorID        string              `json:"agent_run_id,omitempty"`
	Source          string              `json:"source"`
	Identity        string              `json:"identity,omitempty"`
	Tool            string              `json:"tool,omitempty"`
	Host            string              `json:"host"`
	HostPort        string              `json:"host_port,omitempty"`
	Method          string              `json:"method"`
	Scheme          string              `json:"scheme"`
	HTTPVersion     string              `json:"http_version,omitempty"`
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

// protoOrDefault 返回协议版本 token，空则兜底 HTTP/1.1（拆解自解析结构时协议不可得）。
func (s *TrafficSnapshot) protoOrDefault() string {
	if s.HTTPVersion != "" {
		return s.HTTPVersion
	}
	return "HTTP/1.1"
}

// RequestRaw 把快照拼成完整请求报文文本（request-line + 头 + 空行 + body）。
//
// 目标是「可再解析」：header 名按字典序稳定输出（便于 diff/展示），content-length 按实际 body
// 长度重算，剥除 content-encoding/transfer-encoding（body 已在捕获时解压/去分块），使
// http.ReadRequest 能忠实反解回 header map + body 供 replay 用——raw 即单一 canonical 源。
func (s *TrafficSnapshot) RequestRaw() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s\r\n", s.Method, s.uriForLine(), s.protoOrDefault())
	writeHeaderLines(&b, singleValueHeaders(s.RequestHeaders), len(s.RequestBody), s.Host)
	b.WriteString("\r\n")
	out := append([]byte(b.String()), s.RequestBody...)
	return out
}

// ResponseRaw 把快照拼成完整响应报文文本（status-line + 头 + 空行 + body）。规则同 RequestRaw。
func (s *TrafficSnapshot) ResponseRaw() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %d %s\r\n", s.protoOrDefault(), s.StatusCode, http.StatusText(s.StatusCode))
	writeHeaderLines(&b, s.ResponseHeaders, len(s.ResponseBody), "")
	b.WriteString("\r\n")
	out := append([]byte(b.String()), s.ResponseBody...)
	return out
}

// ContentType 抽响应 Content-Type 主类型（去 charset 等参数），供前端 Pretty 判定。空则空串。
func (s *TrafficSnapshot) ContentType() string {
	for k, vs := range s.ResponseHeaders {
		if strings.EqualFold(k, "content-type") && len(vs) > 0 {
			if i := strings.IndexByte(vs[0], ';'); i >= 0 {
				return strings.TrimSpace(vs[0][:i])
			}
			return strings.TrimSpace(vs[0])
		}
	}
	return ""
}

// uriForLine 返回 request-line 的 request-target（URI 优先，空则退化 Path，再空退 "/"）。
func (s *TrafficSnapshot) uriForLine() string {
	if s.URI != "" {
		return s.URI
	}
	if s.Path != "" {
		return s.Path
	}
	return "/"
}

// singleValueHeaders 把请求头（map[string]string）升成多值形态，与响应头统一走 writeHeaderLines。
func singleValueHeaders(h map[string]string) map[string][]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string][]string, len(h))
	for k, v := range h {
		out[k] = []string{v}
	}
	return out
}

// writeHeaderLines 稳定输出 header 行：跳过逐跳/编码相关头（body 已解码），按需补 Host / Content-Length。
// key 按字典序输出保证可复现；每个多值 header 独立成行（Set-Cookie 等 RFC 6265 语义）。
func writeHeaderLines(b *strings.Builder, headers map[string][]string, bodyLen int, host string) {
	keys := make([]string, 0, len(headers))
	hasHost, hasCL := false, false
	for k := range headers {
		lk := strings.ToLower(k)
		switch lk {
		case "content-encoding", "transfer-encoding":
			continue // body 已解压/去分块，保留会让再解析误判
		case "content-length":
			continue // 按实际 body 重算，避免与截断后长度不符
		case "host":
			hasHost = true
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		canonical := http.CanonicalHeaderKey(k)
		if strings.EqualFold(k, "content-length") {
			hasCL = true
		}
		for _, v := range headers[k] {
			fmt.Fprintf(b, "%s: %s\r\n", canonical, v)
		}
	}
	if host != "" && !hasHost {
		fmt.Fprintf(b, "Host: %s\r\n", host)
	}
	if !hasCL && bodyLen > 0 {
		fmt.Fprintf(b, "Content-Length: %d\r\n", bodyLen)
	}
}
