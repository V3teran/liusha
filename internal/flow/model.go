// Package flow 实现 http_flow 表的持久化层：
// 抓取代理在 owner 内观测到的每一次请求/响应（含 headers + body）。
// 大 body 在 Append/AppendBatch 内按 maxReqBody / maxRespBody 截断（v0010 后不再
// 单独打 truncated flag；body 长度 < max 即未截断，按需 caller 自查 len()）。
package flow

import (
	"encoding/json"
	"time"
)

// Flow 是 http_flow 表行的 Go 表示。
// RequestHeaders / ResponseHeaders 走 jsonb；RequestBody / ResponseBody 走 bytea。
// active scan 不入 http_flow 表，故无 OwnerType 二字段——这里只走 passive。
type Flow struct {
	ID               int64
	PassiveSessionID string
	CreatedAt        time.Time
	Method           string
	URL              string
	RequestHeaders   json.RawMessage
	RequestBody      []byte
	StatusCode       int
	ResponseHeaders  json.RawMessage
	ResponseBody     []byte
}

// FlowSummary 是 ListByOwner 的瘦行：不含 body / headers，
// 避免一次查询把数十 MiB bytea 拖入内存。
type FlowSummary struct {
	ID               int64
	PassiveSessionID string
	CreatedAt        time.Time
	Method           string
	URL              string
	StatusCode       int
}
