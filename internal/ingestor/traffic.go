// Package ingestor 是 Stream 流量摄入器：
//
//	proxy 责任链放行 → XADD → <stream_name>
//	ingestor.Traffic XREADGROUP → 落库 http_flow →
//	    tasks.Create(role=main) → Asynq react queue → scanner 主 ReAct
//
// 这里不再做二次过滤：是否丢弃流量完全由 proxy 端 filter chain 决定，
// ingestor 只负责把 proxy 已放行的流量入库 + 入主队列。
//
// 所有运行参数（consumer group/name、batch、block、retry 延迟、tenant）
// 通过 config.IngestorConfig + 注入；零值由 caller 走 ApplyDefaults 兜底。
package ingestor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/hunter"
	"github.com/V3teran/liusha/internal/owner"
	"github.com/V3teran/liusha/internal/passivesession"
	"github.com/V3teran/liusha/internal/proxy"
	"github.com/V3teran/liusha/internal/worker"
)

// ConversationCreator 建被动会话的对话流（阶段2）。traffic 入口首次见到某 host 时建一条
// conversation，passive agent 过程事件落进去，前端可打开实时观察 + 插话。nil 时跳过（向后兼容）。
type ConversationCreator interface {
	CreateConversation(ctx context.Context, title, scanID, roleID string) (conversation.Conversation, error)
}

// internalQueueSize 是进程内 internal 流量队列容量。
// active 沙箱抓的流量经 SubmitInternal 入队，由 drain goroutine 调 handleInternalSnap 落库——
// 不再绕 redis（ingest_handler 与 ingestor 同进程，自环 broker 是冗余）。
// 有界 → 满时 SubmitInternal 返 false，handler 回 503 给沙箱明确背压信号（顶替原 redis stream 的削峰）。
const internalQueueSize = 1024

// Traffic 是流量入口的 Stream 消费者。
type Traffic struct {
	rdb           *redis.Client
	stream        string
	group         string
	name          string
	internalCh    chan *proxy.TrafficSnapshot // 进程内 internal 流量队列（沙箱抓的流量直送，跳过 redis）
	readBatch     int64
	readBlock     time.Duration
	retryDelay    time.Duration
	recreateDelay time.Duration
	passive       *passivesession.Store // 流量入口 LookupOrCreate by host
	passiveTTL    time.Duration
	conversations ConversationCreator // 阶段2：首流量建 passive 会话对话流（nil 跳过）
	flows         *flow.Store
	tasks         *hunter.Store
	enq           *worker.Client
	logger        zerolog.Logger
}

// Deps 注入。
//
// Stream 必填（来自 cfg.Proxy.StreamName，proxy/ingestor 之间约定）；
// Cfg 提供 group/consumer/batch/block/retry 等运行参数（缺省值已由 ApplyDefaults 兜底）；
// Passive + PassiveTTL：流量入口按 host LookupOrCreate passive_session（1 host 1 active）。
type Deps struct {
	Redis         *redis.Client
	Cfg           config.IngestorConfig
	Stream        string
	Tenant        string
	Passive       *passivesession.Store // 流量入口 LookupOrCreate by host
	PassiveTTL    time.Duration         // passive session 滑动 idle 窗口（阶段4：每流量续命 expires_at）
	Conversations ConversationCreator   // 阶段2：建 passive 会话对话流（nil 跳过，向后兼容）
	Flows         *flow.Store
	Tasks         *hunter.Store
	Enqueuer      *worker.Client
	Logger        zerolog.Logger
}

// NewTraffic 构造并 ensure consumer group 存在。
func NewTraffic(ctx context.Context, deps Deps) (*Traffic, error) {
	if deps.Redis == nil || deps.Passive == nil ||
		deps.Flows == nil || deps.Tasks == nil || deps.Enqueuer == nil {
		return nil, errors.New("ingestor.NewTraffic: redis/passive/flows/tasks/enqueuer 必填")
	}
	if deps.PassiveTTL <= 0 {
		return nil, errors.New("ingestor.NewTraffic: passiveTTL 必填且 > 0")
	}
	if strings.TrimSpace(deps.Stream) == "" {
		return nil, errors.New("ingestor.NewTraffic: stream 必填（应来自 cfg.Proxy.StreamName）")
	}
	t := &Traffic{
		rdb:           deps.Redis,
		stream:        deps.Stream,
		group:         deps.Cfg.ConsumerGroup,
		name:          deps.Cfg.ConsumerName,
		readBatch:     int64(deps.Cfg.ReadBatch),
		readBlock:     time.Duration(deps.Cfg.ReadBlockTimeoutMs) * time.Millisecond,
		retryDelay:    time.Duration(deps.Cfg.RetryDelayMs) * time.Millisecond,
		recreateDelay: time.Duration(deps.Cfg.RecreateGroupDelayMs) * time.Millisecond,
		passive:       deps.Passive,
		passiveTTL:    deps.PassiveTTL,
		conversations: deps.Conversations,
		flows:         deps.Flows,
		tasks:         deps.Tasks,
		enq:           deps.Enqueuer,
		logger:        deps.Logger,
		internalCh:    make(chan *proxy.TrafficSnapshot, internalQueueSize),
	}
	if err := t.rdb.XGroupCreateMkStream(ctx, t.stream, t.group, "$").Err(); err != nil {
		if !strings.Contains(err.Error(), "BUSYGROUP") {
			return nil, fmt.Errorf("create consumer group: %w", err)
		}
	}
	return t, nil
}

// SubmitInternal 把 active 沙箱抓的 internal 流量直接入进程内队列（非阻塞），跳过 redis。
// 返回 false 表示队列已满——调用方（scanner ingest_handler）据此回 503，给沙箱背压信号。
// snap.Source 必须已是 "internal"（由 ingest_handler 设置）。
func (t *Traffic) SubmitInternal(snap *proxy.TrafficSnapshot) bool {
	select {
	case t.internalCh <- snap:
		return true
	default:
		return false // 队列满，背压
	}
}

// drainInternal 消费进程内 internal 队列 → handleInternalSnap 落库。ctx 取消即返回。
// 单 goroutine 串行消费：与原 redis 单消费者语义一致，handleInternalSnap 无需并发安全改造。
func (t *Traffic) drainInternal(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case snap := <-t.internalCh:
			t.handleInternalSnap(ctx, snap)
		}
	}
}

// Run 阻塞读 stream → 处理每条 snap → ack；ctx 取消即返回。
// 同时启动 drainInternal goroutine 消费进程内 internal 队列（passive 走 redis，active 走 channel）。
func (t *Traffic) Run(ctx context.Context) error {
	t.logger.Info().Str("stream", t.stream).Str("group", t.group).Msg("ingestor.traffic 已启动")
	go t.drainInternal(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		streams, err := t.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    t.group,
			Consumer: t.name,
			Streams:  []string{t.stream, ">"},
			Count:    t.readBatch,
			Block:    t.readBlock,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) || errors.Is(err, context.Canceled) {
				continue
			}
			// 自愈：FLUSHDB / redis 容器重建会让 stream + group 一起消失，
			// 启动期的 XGroupCreateMkStream 不再生效。检测到 NOGROUP 主动重建。
			if isNoGroupErr(err) {
				t.recreateGroup(ctx)
				time.Sleep(t.recreateDelay)
				continue
			}
			t.logger.Warn().Err(err).Msg("XREADGROUP 失败")
			time.Sleep(t.retryDelay)
			continue
		}
		for _, s := range streams {
			for _, msg := range s.Messages {
				t.handleMessage(ctx, msg)
			}
		}
	}
}

// handleMessage 处理单条 stream entry：落 http_flow + 按 source 分流是否入主任务。
//
// B1 双来源模型：
//   - source=external（passive 入口）: LookupOrCreate passive_session + flow.Append + enqueue trafficAnalysis
//   - source=internal（active 容器内 browser-svc.py CDP capture）: hunter.GetByID → owner +
//     flow.Append（不 enqueue，防自激震荡——active LLM 自己挖的流量不该回头再触发 trafficAnalysis）
//
// 不做二次过滤：proxy 端 filter chain 已经把无关流量（静态资源、心跳、
// websocket、超大 body 等）拦在外面，能进 stream 的都直接处理。
func (t *Traffic) handleMessage(ctx context.Context, msg redis.XMessage) {
	defer func() {
		if err := t.rdb.XAck(ctx, t.stream, t.group, msg.ID).Err(); err != nil {
			t.logger.Warn().Err(err).Str("msg_id", msg.ID).Msg("XACK 失败")
		}
	}()

	raw, ok := msg.Values["snap"].(string)
	if !ok {
		return
	}
	var snap proxy.TrafficSnapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.logger.Warn().Err(err).Msg("snap 解析失败")
		return
	}

	// 默认 source=external（兼容老 snap 无 source 字段）
	source := snap.Source
	if source == "" {
		source = "external"
	}

	switch source {
	case "internal":
		t.handleInternalSnap(ctx, &snap)
	default:
		t.handleExternalSnap(ctx, &snap)
	}
}

// handleExternalSnap 处理 passive 入口流量：LookupOrCreate passive_session + 入 trafficAnalysis 队列。
func (t *Traffic) handleExternalSnap(ctx context.Context, snap *proxy.TrafficSnapshot) {
	sess, lkErr := t.passive.LookupOrCreate(ctx, snap.Host, t.passiveTTL)
	if lkErr != nil {
		t.logger.Warn().Err(lkErr).Str("host", snap.Host).Msg("passive_session LookupOrCreate 失败，跳过本流量")
		return
	}
	passSessID := sess.ID
	// 阶段2：确保 passive 会话绑定对话流（首流量建会话）。阶段4：每条流量滑动续命 expires_at
	// （idle 释放——持续有流量则永不过期，真闲置 idle 后才被 Sweep 关闭）。
	convID := t.ensureConversation(ctx, &sess)
	if err := t.passive.BumpExpiry(ctx, passSessID, t.passiveTTL); err != nil {
		t.logger.Warn().Err(err).Str("passive_session_id", passSessID).Msg("续命 expires_at 失败（不阻塞流量）")
	}

	flowID, err := t.appendFlow(ctx, passSessID, snap)
	if err != nil {
		t.logger.Warn().Err(err).Msg("flow.Append 失败")
		return
	}

	if err := t.enqueueMain(ctx, passSessID, convID, flowID, snap); err != nil {
		t.logger.Warn().Err(err).Str("passive_session_id", passSessID).Int64("flow_id", flowID).Msg("主任务入队失败")
		return
	}
	t.logger.Info().
		Str("passive_session_id", passSessID).
		Int64("flow_id", flowID).
		Str("method", snap.Method).Str("url", snap.URI).
		Msg("流量已入主 ReAct 队列")
}

// handleInternalSnap 处理 active 容器内 browser-svc.py 抓的 chromium 真实流量：
// 反查 hunter→owner 后落双字段 http_flow，但不 enqueue trafficAnalysis。
//
// source=internal 唯一来源（B1）：browser-svc.py 持单一 CDP 连接，内建 Network observer 把
// chromium 的 Document/XHR/Fetch（含真实认证凭证位置）→ POST /internal/v1/flows/ingest →
// cmd/proxy/ingest_handler 构造 snap.Source="internal" + snap.HunterID（payload body 内自带，
// browser-svc.py 按 session→tab→hunter 逐请求归属）。
//
// 这里反查 hunter 表得 owner_type/owner_id 写双字段（hunter_id 细粒度可追溯 + owner_id 顶层归档）。
// 不 enqueue：active LLM 自己用浏览器挖的流量回头再触发 trafficAnalysis 会自激震荡。
// hunter_id 缺失 / 反查失败时丢弃（browser-svc.py 归属异常 / hunter 已被清理）。
func (t *Traffic) handleInternalSnap(ctx context.Context, snap *proxy.TrafficSnapshot) {
	if snap.HunterID == "" {
		t.logger.Warn().Str("host", snap.Host).Str("uri", snap.URI).
			Msg("internal 流量缺 hunter_id，丢弃（browser-svc.py session→hunter 归属异常？）")
		return
	}

	// 反查 hunter 表得 owner_type/owner_id（每流量 1 indexed PK 查询 < 1ms，未来可加 LRU cache）
	run, err := t.tasks.GetByID(ctx, snap.HunterID)
	if err != nil {
		t.logger.Warn().Err(err).Str("hunter_id", snap.HunterID).
			Msg("internal 流量反查 hunter 失败，丢弃（hunter 已被清理 / 跨进程脏数据？）")
		return
	}

	reqH, _ := json.Marshal(snap.RequestHeaders)
	respH, _ := json.Marshal(snap.ResponseHeaders)
	flowID, err := t.flows.Append(ctx, flow.Flow{
		OwnerType:       run.OwnerType,
		OwnerID:         run.OwnerID,
		HunterID:        snap.HunterID,
		Source:          "internal",
		Identity:        snap.Identity,
		Tool:            snap.Tool,
		Host:            snap.Host,
		Path:            snap.Path,
		CreatedAt:       snap.Timestamp,
		Method:          snap.Method,
		URL:             fullURL(snap),
		RequestHeaders:  reqH,
		RequestBody:     snap.RequestBody,
		StatusCode:      snap.StatusCode,
		ResponseHeaders: respH,
		ResponseBody:    snap.ResponseBody,
		DurationMs:      int(snap.Duration.Milliseconds()),
	})
	if err != nil {
		t.logger.Warn().Err(err).Msg("internal flow.Append 失败")
		return
	}
	t.logger.Info().
		Str("owner_type", run.OwnerType).
		Str("owner_id", run.OwnerID).
		Str("hunter_id", snap.HunterID).
		Int64("flow_id", flowID).
		Str("method", snap.Method).Str("url", snap.URI).
		Msg("internal 流量已入字典（不触发 trafficAnalysis）")
}

func (t *Traffic) appendFlow(ctx context.Context, passSessID string, snap *proxy.TrafficSnapshot) (int64, error) {
	reqH, _ := json.Marshal(snap.RequestHeaders)
	respH, _ := json.Marshal(snap.ResponseHeaders)
	// passive 入口流量：owner_type 固定 passive_session，source 固定 external。
	// （active 容器内 browser-svc.py 抓的 internal 流量走 handleInternalSnap，反查 hunter→owner。）
	return t.flows.Append(ctx, flow.Flow{
		OwnerType:       "passive_session",
		OwnerID:         passSessID,
		Source:          "external",
		Host:            snap.Host,
		Path:            snap.Path,
		CreatedAt:       snap.Timestamp,
		Method:          snap.Method,
		URL:             fullURL(snap),
		RequestHeaders:  reqH,
		RequestBody:     snap.RequestBody,
		StatusCode:      snap.StatusCode,
		ResponseHeaders: respH,
		ResponseBody:    snap.ResponseBody,
		DurationMs:      int(snap.Duration.Milliseconds()),
	})
}

// ensureConversation 确保 passive 会话已绑对话流：已绑直接返回；未绑则建一条 conversation
// （title=host，scan_id 空——passive 不进 active_scan FK）并回填 passive_session.conversation_id。
// conversations nil / 建会话失败 → 返空串（降级：本次不绑，事件不落对话，但流量分析照常）。
func (t *Traffic) ensureConversation(ctx context.Context, sess *passivesession.Session) string {
	if sess.ConversationID != "" {
		return sess.ConversationID
	}
	if t.conversations == nil {
		return ""
	}
	conv, err := t.conversations.CreateConversation(ctx, sess.Host, "", "")
	if err != nil {
		t.logger.Warn().Err(err).Str("host", sess.Host).Msg("建 passive 会话对话流失败（降级：本次不绑对话）")
		return ""
	}
	if err := t.passive.SetConversationID(ctx, sess.ID, conv.ID); err != nil {
		t.logger.Warn().Err(err).Str("passive_session_id", sess.ID).Msg("回填 conversation_id 失败")
	}
	sess.ConversationID = conv.ID
	t.logger.Info().Str("passive_session_id", sess.ID).Str("conversation_id", conv.ID).
		Str("host", sess.Host).Msg("passive 会话已绑定对话流")
	return conv.ID
}

func (t *Traffic) enqueueMain(ctx context.Context, passSessID, convID string, flowID int64, snap *proxy.TrafficSnapshot) error {
	entrypoint, _ := json.Marshal(map[string]any{
		"flow_id": flowID,
		"host":    snap.Host,
		"method":  snap.Method,
		"url":     snap.URI,
	})
	payloadInput, _ := json.Marshal(map[string]any{
		"mode":       "passive",
		"entrypoint": json.RawMessage(entrypoint),
	})

	tid, err := t.tasks.Create(ctx, hunter.NewParams{
		OwnerType: owner.Passive,
		OwnerID:   passSessID,
		Role:      "traffic-analysis",
		Input:     payloadInput,
	})
	if err != nil {
		return fmt.Errorf("tasks.Create: %w", err)
	}

	if _, _, err := t.enq.Enqueue(ctx, worker.RoleHunter, worker.Payload{
		HunterID:       tid,
		OwnerType:      owner.Passive,
		OwnerID:        passSessID,
		ConversationID: convID, // 阶段2：passive 过程事件落进对话流 + 读对话历史（插话）
		Input:          payloadInput,
	}); err != nil {
		return fmt.Errorf("enq.Enqueue: %w", err)
	}
	return nil
}

func fullURL(s *proxy.TrafficSnapshot) string {
	host := s.HostPort
	if host == "" {
		host = s.Host
	}
	if s.Scheme != "" && host != "" {
		return s.Scheme + "://" + host + s.URI
	}
	if host != "" {
		return "//" + host + s.URI
	}
	return s.URI
}

// isNoGroupErr 检测 XREADGROUP 在 stream/consumer group 不存在时返回的错误。
// FLUSHDB / 容器重建后必然命中——上层据此触发自愈重建。
func isNoGroupErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "NOGROUP")
}

// recreateGroup 在 NOGROUP 自愈分支调用——MKSTREAM 让空 stream 一并创建；
// 起点 "0" 而非 "$"，确保自愈与 publisher XADD 之间存在 race 时不漏消息。
// BUSYGROUP 表示并发自愈竞争，已被另一进程恢复，忽略即可。
func (t *Traffic) recreateGroup(ctx context.Context) {
	err := t.rdb.XGroupCreateMkStream(ctx, t.stream, t.group, "0").Err()
	if err == nil {
		t.logger.Info().Str("stream", t.stream).Str("group", t.group).
			Msg("检测到 NOGROUP → 已自愈重建 consumer group")
		return
	}
	if strings.Contains(err.Error(), "BUSYGROUP") {
		return
	}
	t.logger.Warn().Err(err).Msg("自愈重建 consumer group 失败")
}
