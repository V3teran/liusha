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
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/hunter"
	"github.com/V3teran/liusha/internal/owner"
	"github.com/V3teran/liusha/internal/passivesession"
	"github.com/V3teran/liusha/internal/proxy"
	"github.com/V3teran/liusha/internal/worker"
)

// Traffic 是流量入口的 Stream 消费者。
type Traffic struct {
	rdb           *redis.Client
	stream        string
	group         string
	name          string
	readBatch     int64
	readBlock     time.Duration
	retryDelay    time.Duration
	recreateDelay time.Duration
	passive       *passivesession.Store // 流量入口 LookupOrCreate by host
	passiveTTL    time.Duration
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
	Redis      *redis.Client
	Cfg        config.IngestorConfig
	Stream     string
	Tenant     string
	Passive    *passivesession.Store // 流量入口 LookupOrCreate by host
	PassiveTTL time.Duration         // passive session 过期窗口
	Flows      *flow.Store
	Tasks      *hunter.Store
	Enqueuer   *worker.Client
	Logger     zerolog.Logger
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
		flows:         deps.Flows,
		tasks:         deps.Tasks,
		enq:           deps.Enqueuer,
		logger:        deps.Logger,
	}
	if err := t.rdb.XGroupCreateMkStream(ctx, t.stream, t.group, "$").Err(); err != nil {
		if !strings.Contains(err.Error(), "BUSYGROUP") {
			return nil, fmt.Errorf("create consumer group: %w", err)
		}
	}
	return t, nil
}

// Run 阻塞读 stream → 处理每条 snap → ack；ctx 取消即返回。
func (t *Traffic) Run(ctx context.Context) error {
	t.logger.Info().Str("stream", t.stream).Str("group", t.group).Msg("ingestor.traffic 已启动")
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
//   - source=external（passive 入口）: LookupOrCreate passive_session + flow.Append + enqueue tracker
//   - source=internal（active 容器内 browser-svc.py CDP capture）: hunter.GetByID → owner +
//     flow.Append（不 enqueue，防自激震荡——active LLM 自己挖的流量不该回头再触发 tracker）
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

// handleExternalSnap 处理 passive 入口流量：LookupOrCreate passive_session + 入 tracker 队列。
func (t *Traffic) handleExternalSnap(ctx context.Context, snap *proxy.TrafficSnapshot) {
	sess, lkErr := t.passive.LookupOrCreate(ctx, snap.Host, t.passiveTTL)
	if lkErr != nil {
		t.logger.Warn().Err(lkErr).Str("host", snap.Host).Msg("passive_session LookupOrCreate 失败，跳过本流量")
		return
	}
	passSessID := sess.ID
	flowID, err := t.appendFlow(ctx, passSessID, snap)
	if err != nil {
		t.logger.Warn().Err(err).Msg("flow.Append 失败")
		return
	}

	if err := t.enqueueMain(ctx, passSessID, flowID, snap); err != nil {
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
// 反查 hunter→owner 后落双字段 http_flow，但不 enqueue tracker。
//
// source=internal 唯一来源（B1）：browser-svc.py 持单一 CDP 连接，内建 Network observer 把
// chromium 的 Document/XHR/Fetch（含真实认证凭证位置）→ POST /internal/v1/flows/ingest →
// cmd/proxy/ingest_handler 构造 snap.Source="internal" + snap.HunterID（payload body 内自带，
// browser-svc.py 按 session→tab→hunter 逐请求归属）。
//
// 这里反查 hunter 表得 owner_type/owner_id 写双字段（hunter_id 细粒度可追溯 + owner_id 顶层归档）。
// 不 enqueue：active LLM 自己用浏览器挖的流量回头再触发 tracker 会自激震荡。
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
		Msg("internal 流量已入字典（不触发 tracker）")
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

func (t *Traffic) enqueueMain(ctx context.Context, passSessID string, flowID int64, snap *proxy.TrafficSnapshot) error {
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
		Role:      "tracker",
		Input:     payloadInput,
	})
	if err != nil {
		return fmt.Errorf("tasks.Create: %w", err)
	}

	if _, _, err := t.enq.Enqueue(ctx, worker.RoleHunter, worker.Payload{
		HunterID:  tid,
		OwnerType: owner.Passive,
		OwnerID:   passSessID,
		Input:     payloadInput,
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
