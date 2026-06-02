// Package flow 实现 http_flow 表的持久化层：
// 落存 owner 内观测到的每一次请求/响应（含 headers + body）。
// 大 body 在 Append/AppendBatch 内按 maxReqBody / maxRespBody 截断（v0010 后不再
// 单独打 truncated flag；body 长度 < max 即未截断，caller 按需自查 len()）。
//
// 双来源（B1，ingestor 按 snap.Source 分流，见 ingestor.Traffic.handleMessage）：
//   - Source='external'：passive 入口流量（8888 外部代理捕获）→ owner=passive_session、
//     HunterID 空，ingestor 入主 ReAct 队列触发 tracker。
//   - Source='internal'：active 容器内 browser-svc.py CDP 抓的 chromium 真实请求
//     （含认证凭证位置）→ 反查 hunter 得 owner=active_scan、HunterID 必填，不入队
//     （active 自己挖的流量回头再触发 tracker 会自激震荡）。
//
// CLI 工具直连不入字典；endpoint 沉淀走 endpoint 表（write_endpoint），
// curl 链路凭证共享走 redis credentials key（read_credentials / write_credential）。
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
