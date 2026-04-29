// Package flow 实现 http_flow 表的持久化层：
// 抓取代理在 engagement 内观测到的每一次请求/响应（含 headers + body）。
// 大 body 在 Append/AppendBatch 内按 maxReqBody / maxRespBody 截断，
// 并写入 request_truncated / response_truncated 标志位。
package flow

import (
	"encoding/json"
	"time"
)

// Flow 是 http_flow 表行的 Go 表示。
// RequestHeaders / ResponseHeaders 走 jsonb；RequestBody / ResponseBody 走 bytea。
type Flow struct {
	ID                int64
	EngagementID      string
	Ts                time.Time
	Method            string
	URL               string
	RequestHeaders    json.RawMessage
	RequestBody       []byte
	RequestTruncated  bool
	StatusCode        int
	ResponseHeaders   json.RawMessage
	ResponseBody      []byte
	ResponseTruncated bool
}

// FlowSummary 是 ListByEngagement 的瘦行：不含 body / headers，
// 避免一次查询把数十 MiB bytea 拖入内存。
type FlowSummary struct {
	ID                int64
	EngagementID      string
	Ts                time.Time
	Method            string
	URL               string
	StatusCode        int
	RequestTruncated  bool
	ResponseTruncated bool
}
