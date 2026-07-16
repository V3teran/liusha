package einotools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/V3teran/liusha/internal/traffic"
)

// flowsource.go：给 replay/list/view_traffic 工具一个「归一化 + 已限定 task 范围」的流量视图。
//
// 流量拆两表后（proxy_traffic 属 host、agent_traffic 属 task，见 spec §5），active 与 passive
// 读不同表：active 读 agent_traffic（自产弹药），passive 读 proxy_traffic（这批被消费的捕获流量，
// §13.6 保留 replay 能力）。工具本身不应关心是哪张表——差异收敛到本文件的两个适配器，build 时
// 按 task.mode 注入对应适配器，scope（task_id / consumed_by_task_id）闭包绑定，工具零分支。

// TrafficRecord 是单条流量的归一化完整视图（replay/view_traffic 用）。
type TrafficRecord struct {
	ID              int64
	Source          string // 'proxy' / 'agent'（给 LLM 标注来源）
	Identity        string
	Tool            string
	Host            string
	Method          string
	URL             string
	Path            string
	RequestHeaders  json.RawMessage
	RequestBody     []byte
	StatusCode      int
	ResponseHeaders json.RawMessage
	ResponseBody    []byte
	DurationMs      int
	CreatedAt       time.Time
}

// TrafficSummary 是流量瘦摘要（list_traffic 用）。
type TrafficSummary struct {
	ID         int64
	Source     string
	Identity   string
	Tool       string
	Host       string
	Method     string
	Path       string
	StatusCode int
	DurationMs int
	CreatedAt  time.Time
}

// TrafficQuery 是 list_traffic 的过滤条件（归一化，与底层 store filter 解耦）。
type TrafficQuery struct {
	Host      string
	Method    string
	Path      string
	Identity  string
	Tool      string
	StatusMin int
	StatusMax int
	Since     time.Time
	Limit     int
	Offset    int
}

// TrafficReader 读单条已限定 task 范围的流量；不在范围内（或不存在）返回 (_, false, nil)。
type TrafficReader interface {
	GetInScope(ctx context.Context, id int64) (TrafficRecord, bool, error)
}

// TrafficLister 列出已限定 task 范围的流量摘要。
type TrafficLister interface {
	ListInScope(ctx context.Context, q TrafficQuery) ([]TrafficSummary, error)
}

// ── agent 适配器（active：读 agent_traffic，按 task_id） ──

type agentTrafficScope struct {
	store  *traffic.AgentStore
	taskID string
}

// NewAgentTrafficScope 把 AgentStore 限定到某 task，满足 TrafficReader + TrafficLister（active 用）。
func NewAgentTrafficScope(store *traffic.AgentStore, taskID string) *agentTrafficScope {
	return &agentTrafficScope{store: store, taskID: taskID}
}

func (a *agentTrafficScope) GetInScope(ctx context.Context, id int64) (TrafficRecord, bool, error) {
	f, err := a.store.GetByID(ctx, id)
	if err != nil {
		return TrafficRecord{}, false, err
	}
	if f.TaskID != a.taskID {
		return TrafficRecord{}, false, nil
	}
	return TrafficRecord{
		ID: f.ID, Source: "agent", Identity: f.Identity, Tool: f.Tool,
		Host: f.Host, Method: f.Method, URL: f.URL, Path: f.Path,
		RequestHeaders: f.RequestHeaders, RequestBody: f.RequestBody,
		StatusCode: f.StatusCode, ResponseHeaders: f.ResponseHeaders, ResponseBody: f.ResponseBody,
		DurationMs: f.DurationMs, CreatedAt: f.CreatedAt,
	}, true, nil
}

func (a *agentTrafficScope) ListInScope(ctx context.Context, q TrafficQuery) ([]TrafficSummary, error) {
	rows, err := a.store.ListByTaskFiltered(ctx, a.taskID, traffic.AgentListFilter{
		Host: q.Host, Method: q.Method, Path: q.Path, Identity: q.Identity, Tool: q.Tool,
		StatusMin: q.StatusMin, StatusMax: q.StatusMax, Since: q.Since, Limit: q.Limit, Offset: q.Offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]TrafficSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, TrafficSummary{
			ID: r.ID, Source: "agent", Identity: r.Identity, Tool: r.Tool,
			Host: r.Host, Method: r.Method, Path: r.Path,
			StatusCode: r.StatusCode, DurationMs: r.DurationMs, CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

// ── proxy 适配器（passive：读 proxy_traffic，按 consumed_by_task_id） ──

type proxyTrafficScope struct {
	store  *traffic.ProxyStore
	taskID string
}

// NewProxyTrafficScope 把 ProxyStore 限定到某 passive task（consumed_by_task_id），
// 满足 TrafficReader（replay/view）+ TrafficLister（list_traffic 枚举本批流量）。
func NewProxyTrafficScope(store *traffic.ProxyStore, taskID string) *proxyTrafficScope {
	return &proxyTrafficScope{store: store, taskID: taskID}
}

func (p *proxyTrafficScope) ListInScope(ctx context.Context, q TrafficQuery) ([]TrafficSummary, error) {
	rows, err := p.store.ListByTaskFiltered(ctx, p.taskID, traffic.ProxyListFilter{
		Host: q.Host, Method: q.Method, Path: q.Path,
		StatusMin: q.StatusMin, StatusMax: q.StatusMax, Limit: q.Limit, Offset: q.Offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]TrafficSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, TrafficSummary{
			ID: r.ID, Source: "proxy",
			Host: r.Host, Method: r.Method, Path: r.Path,
			StatusCode: r.StatusCode, DurationMs: r.DurationMs, CreatedAt: r.CapturedAt,
		})
	}
	return out, nil
}

func (p *proxyTrafficScope) GetInScope(ctx context.Context, id int64) (TrafficRecord, bool, error) {
	f, err := p.store.GetByID(ctx, id)
	if err != nil {
		return TrafficRecord{}, false, err
	}
	if f.ConsumedByTaskID != p.taskID {
		return TrafficRecord{}, false, nil
	}
	return TrafficRecord{
		ID: f.ID, Source: "proxy",
		Host: f.Host, Method: f.Method, URL: f.URL, Path: f.Path,
		RequestHeaders: f.RequestHeaders, RequestBody: f.RequestBody,
		StatusCode: f.StatusCode, ResponseHeaders: f.ResponseHeaders, ResponseBody: f.ResponseBody,
		DurationMs: f.DurationMs, CreatedAt: f.CapturedAt,
	}, true, nil
}
