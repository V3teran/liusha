package einotools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/V3teran/liusha/internal/flow"
)

// flowsource.go：给 replay/list/view_flow 工具一个「归一化 + 已限定 task 范围」的流量视图。
//
// 流量拆两表后（proxy_traffic 属 host、agent_traffic 属 task，见 spec §5），active 与 passive
// 读不同表：active 读 agent_traffic（自产弹药），passive 读 proxy_traffic（这批被消费的捕获流量，
// §13.6 保留 replay 能力）。工具本身不应关心是哪张表——差异收敛到本文件的两个适配器，build 时
// 按 task.mode 注入对应适配器，scope（task_id / consumed_by_task_id）闭包绑定，工具零分支。

// FlowRecord 是单条流量的归一化完整视图（replay/view_flow 用）。
type FlowRecord struct {
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

// FlowSummaryRecord 是流量瘦摘要（list_flows 用）。
type FlowSummaryRecord struct {
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

// FlowQuery 是 list_flows 的过滤条件（归一化，与底层 store filter 解耦）。
type FlowQuery struct {
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

// FlowReader 读单条已限定 task 范围的流量；不在范围内（或不存在）返回 (_, false, nil)。
type FlowReader interface {
	GetInScope(ctx context.Context, id int64) (FlowRecord, bool, error)
}

// FlowLister 列出已限定 task 范围的流量摘要。
type FlowLister interface {
	ListInScope(ctx context.Context, q FlowQuery) ([]FlowSummaryRecord, error)
}

// ── agent 适配器（active：读 agent_traffic，按 task_id） ──

type agentFlowScope struct {
	store  *flow.AgentStore
	taskID string
}

// NewAgentFlowScope 把 AgentStore 限定到某 task，满足 FlowReader + FlowLister（active 用）。
func NewAgentFlowScope(store *flow.AgentStore, taskID string) *agentFlowScope {
	return &agentFlowScope{store: store, taskID: taskID}
}

func (a *agentFlowScope) GetInScope(ctx context.Context, id int64) (FlowRecord, bool, error) {
	f, err := a.store.GetByID(ctx, id)
	if err != nil {
		return FlowRecord{}, false, err
	}
	if f.TaskID != a.taskID {
		return FlowRecord{}, false, nil
	}
	return FlowRecord{
		ID: f.ID, Source: "agent", Identity: f.Identity, Tool: f.Tool,
		Host: f.Host, Method: f.Method, URL: f.URL, Path: f.Path,
		RequestHeaders: f.RequestHeaders, RequestBody: f.RequestBody,
		StatusCode: f.StatusCode, ResponseHeaders: f.ResponseHeaders, ResponseBody: f.ResponseBody,
		DurationMs: f.DurationMs, CreatedAt: f.CreatedAt,
	}, true, nil
}

func (a *agentFlowScope) ListInScope(ctx context.Context, q FlowQuery) ([]FlowSummaryRecord, error) {
	rows, err := a.store.ListByTaskFiltered(ctx, a.taskID, flow.AgentListFilter{
		Host: q.Host, Method: q.Method, Path: q.Path, Identity: q.Identity, Tool: q.Tool,
		StatusMin: q.StatusMin, StatusMax: q.StatusMax, Since: q.Since, Limit: q.Limit, Offset: q.Offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]FlowSummaryRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, FlowSummaryRecord{
			ID: r.ID, Source: "agent", Identity: r.Identity, Tool: r.Tool,
			Host: r.Host, Method: r.Method, Path: r.Path,
			StatusCode: r.StatusCode, DurationMs: r.DurationMs, CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

// ── proxy 适配器（passive：读 proxy_traffic，按 consumed_by_task_id） ──

type proxyFlowScope struct {
	store  *flow.ProxyStore
	taskID string
}

// NewProxyFlowScope 把 ProxyStore 限定到某 passive task（consumed_by_task_id），
// 满足 FlowReader（replay/view）+ FlowLister（list_flows 枚举本批流量）。
func NewProxyFlowScope(store *flow.ProxyStore, taskID string) *proxyFlowScope {
	return &proxyFlowScope{store: store, taskID: taskID}
}

func (p *proxyFlowScope) ListInScope(ctx context.Context, q FlowQuery) ([]FlowSummaryRecord, error) {
	rows, err := p.store.ListByTaskFiltered(ctx, p.taskID, flow.ProxyListFilter{
		Host: q.Host, Method: q.Method, Path: q.Path,
		StatusMin: q.StatusMin, StatusMax: q.StatusMax, Limit: q.Limit, Offset: q.Offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]FlowSummaryRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, FlowSummaryRecord{
			ID: r.ID, Source: "proxy",
			Host: r.Host, Method: r.Method, Path: r.Path,
			StatusCode: r.StatusCode, DurationMs: r.DurationMs, CreatedAt: r.CapturedAt,
		})
	}
	return out, nil
}

func (p *proxyFlowScope) GetInScope(ctx context.Context, id int64) (FlowRecord, bool, error) {
	f, err := p.store.GetByID(ctx, id)
	if err != nil {
		return FlowRecord{}, false, err
	}
	if f.ConsumedByTaskID != p.taskID {
		return FlowRecord{}, false, nil
	}
	return FlowRecord{
		ID: f.ID, Source: "proxy",
		Host: f.Host, Method: f.Method, URL: f.URL, Path: f.Path,
		RequestHeaders: f.RequestHeaders, RequestBody: f.RequestBody,
		StatusCode: f.StatusCode, ResponseHeaders: f.ResponseHeaders, ResponseBody: f.ResponseBody,
		DurationMs: f.DurationMs, CreatedAt: f.CapturedAt,
	}, true, nil
}
