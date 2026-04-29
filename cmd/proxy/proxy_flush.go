// proxy_flush 把内嵌 MITM 的 TrafficSnapshot batch 落库 + 切窗 + enqueue sniffer。
// 与原 proxify_consumer 等价，但输入是 in-memory snapshot 而非 JSONL 行。
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/proxy"
	"github.com/V3teran/liusha/internal/window"
	"github.com/V3teran/liusha/internal/worker"
)

// defaultTenant：v1 单租户，统一写死。与原 proxify_consumer 行为一致。
const defaultTenant = "default"

// proxyFlush 实现 proxy.AggregatorSink：
//  1. engagement.LookupOrCreate（按 host 懒建）
//  2. flow.Append（写 http_flow）
//  3. window.OpenOrAppend（切 traffic_window；攒到 batch 即 closed）
//  4. justClosed=true 时 enqueue sniffer 任务
//
// 任意单条 snapshot 失败仅 warn 跳过，不影响整 batch（尽量榨干流量价值）。
type proxyFlush struct {
	engs    *engagement.Store
	flows   *flow.Store
	windows *window.Store
	enq     *worker.Client
	cfg     config.ProxyConfig
	logger  zerolog.Logger
}

// newProxyFlush 构造 sink；所有 store 依赖必填。
func newProxyFlush(
	engs *engagement.Store,
	flows *flow.Store,
	windows *window.Store,
	enq *worker.Client,
	cfg config.ProxyConfig,
	logger zerolog.Logger,
) *proxyFlush {
	return &proxyFlush{engs: engs, flows: flows, windows: windows, enq: enq, cfg: cfg, logger: logger}
}

// Flush 按顺序处理整个 batch；单条失败 warn 后继续。
// ctx 由 Aggregator 注入：周期 flush 走 background ctx，关停时也是 background。
func (p *proxyFlush) Flush(ctx context.Context, snapshots []*proxy.TrafficSnapshot) error {
	for _, snap := range snapshots {
		if snap == nil {
			continue
		}
		p.handle(ctx, snap)
	}
	return nil
}

// handle 单条 snapshot：建/查 engagement → 写 flow → 切窗 → enqueue。
func (p *proxyFlush) handle(ctx context.Context, snap *proxy.TrafficSnapshot) {
	if snap.Host == "" {
		p.logger.Warn().Msg("snapshot host 为空，跳过")
		return
	}

	eng, err := p.engs.LookupOrCreate(ctx, defaultTenant, snap.Host, engagement.ModeProxy)
	if err != nil {
		p.logger.Warn().Err(err).Str("host", snap.Host).Msg("engagement LookupOrCreate 失败")
		return
	}

	flowID, err := p.appendFlow(ctx, eng.ID, snap)
	if err != nil {
		p.logger.Warn().Err(err).Str("eid", eng.ID).Msg("flow 入库失败")
		return
	}

	wid, justClosed, err := p.windows.OpenOrAppend(ctx, eng.ID, window.FlowRef{
		ID:     flowID,
		Method: snap.Method,
		URL:    fullURL(snap),
	}, p.cfg.WindowBatch)
	if err != nil {
		p.logger.Warn().Err(err).Str("eid", eng.ID).Msg("window OpenOrAppend 失败")
		return
	}
	if justClosed {
		p.enqueueSniffer(ctx, eng.ID, wid)
	}
}

// appendFlow 把 snapshot 转成 flow.Flow 并写库，返回 bigserial id。
//
// 截断由 flow.Store 在 Append 内根据 NewStore 时的 maxReqBody / maxRespBody 处理；
// snap 已被 proxy.Server 在 readAndRebuildBody 阶段按 ProxyConfig.MaxXxxBodySize 限过一次，
// 这里再走一次 store 截断属于双层保险。
func (p *proxyFlush) appendFlow(ctx context.Context, eid string, snap *proxy.TrafficSnapshot) (int64, error) {
	reqH, err := json.Marshal(snap.RequestHeaders)
	if err != nil {
		return 0, fmt.Errorf("marshal request headers: %w", err)
	}
	respH, err := json.Marshal(snap.ResponseHeaders)
	if err != nil {
		return 0, fmt.Errorf("marshal response headers: %w", err)
	}
	return p.flows.Append(ctx, flow.Flow{
		EngagementID:    eid,
		Ts:              snap.Timestamp,
		Method:          snap.Method,
		URL:             fullURL(snap),
		RequestHeaders:  reqH,
		RequestBody:     snap.RequestBody,
		StatusCode:      snap.StatusCode,
		ResponseHeaders: respH,
		ResponseBody:    snap.ResponseBody,
	})
}

// enqueueSniffer 把刚关闭的窗口投递为 sniffer 任务（spec §3.2 主链路入口）。
func (p *proxyFlush) enqueueSniffer(ctx context.Context, eid, windowID string) {
	taskID := uuid.NewString()
	input, err := json.Marshal(map[string]string{"window_id": windowID})
	if err != nil {
		p.logger.Warn().Err(err).Msg("序列化 sniffer input 失败")
		return
	}
	if _, _, err := p.enq.Enqueue(ctx, worker.RoleSniffer, worker.Payload{
		TaskID:       taskID,
		EngagementID: eid,
		Input:        input,
	}); err != nil {
		p.logger.Warn().Err(err).Str("eid", eid).Str("window_id", windowID).Msg("sniffer 入队失败")
		return
	}
	p.logger.Info().
		Str("eid", eid).
		Str("window_id", windowID).
		Str("task_id", taskID).
		Msg("窗口关闭，已投递 sniffer 任务")
}

// fullURL 把 snapshot 还原成绝对 URL，用于 flow.URL / window.FlowRef.URL。
// 形如 "https://vulnapp/api/users?uid=2"。scheme/host 缺失时退化为 path 形态。
func fullURL(snap *proxy.TrafficSnapshot) string {
	if snap.Scheme != "" && snap.Host != "" {
		return snap.Scheme + "://" + snap.Host + snap.URI
	}
	if snap.Host != "" {
		return "//" + snap.Host + snap.URI
	}
	return snap.URI
}
