// Package flow 实现 http_flow 表的持久化层：
// 抓取代理在 owner 内观测到的每一次请求/响应（含 headers + body）。
// 大 body 在 Append/AppendBatch 内按 maxReqBody / maxRespBody 截断（v0010 后不再
// 单独打 truncated flag；body 长度 < max 即未截断，按需 caller 自查 len()）。
//
// v35+：http_flow 只存 passive 入口流量（8888 外部代理捕获，Source='external'）。
// 容器内 sandbox 工具（chromium / CLI）流量不再入字典——凭证共享改走 redis credentials key
// （read_credentials / write_credential），endpoint 沉淀走 endpoint 表（write_endpoint）。
// OwnerType/OwnerID 多态关联到 passive_session（active_scan 在 v35+ 已无 http_flow 行）。
// HunterID 字段保留为空（v35+ passive 单 flow 单 hunter，hunter 归属在 agent_run 表）。
package flow

import (
	"encoding/json"
	"time"
)

// Flow 是 http_flow 表行的 Go 表示。
// RequestHeaders / ResponseHeaders 走 jsonb；RequestBody / ResponseBody 走 bytea。
type Flow struct {
	ID              int64
	OwnerType       string // 'passive_session' / 'active_scan'
	OwnerID         string
	HunterID        string // 可选；internal source 必填（细粒度可追溯），external 为空
	Source          string // 'external' / 'internal'
	Host            string
	CreatedAt       time.Time
	Method          string
	URL             string
	Path            string // 0060 加：从 url 抽出，glob 查询索引用
	RequestHeaders  json.RawMessage
	RequestBody     []byte
	StatusCode      int
	ResponseHeaders json.RawMessage
	ResponseBody    []byte
	DurationMs      int
}

// FlowSummary 是 ListByOwner 的瘦行：不含 body / headers，
// 避免一次查询把数十 MiB bytea 拖入内存。
type FlowSummary struct {
	ID         int64
	OwnerType  string
	OwnerID    string
	HunterID   string
	Source     string
	Host       string
	CreatedAt  time.Time
	Method     string
	URL        string
	Path       string
	StatusCode int
	DurationMs int
}
