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
// 通过 config.IngestorConfig + Deps.Tenant 注入；零值由 caller 走 ApplyDefaults 兜底。
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

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/proxy"
	"github.com/V3teran/liusha/internal/worker"
)

// Traffic 是流量入口的 Stream 消费者。
type Traffic struct {
	rdb           *redis.Client
	stream        string
	group         string
	name          string
	tenant        string
	readBatch     int64
	readBlock     time.Duration
	retryDelay    time.Duration
	recreateDelay time.Duration
	engs          *engagement.Store
	rotator       *engagement.Rotator
	flows         *flow.Store
	tasks         *agentrun.Store
	enq           *worker.Client
	logger        zerolog.Logger
}

// Deps 注入。
//
// Stream 必填（来自 cfg.Proxy.StreamName，proxy/ingestor 之间约定）；
// Cfg 提供 group/consumer/batch/block/retry 等运行参数（缺省值已由 ApplyDefaults 兜底）；
// Tenant 控制 engagement 多租户隔离，空时回退 "default"。
//
// Rotator 可空：空时走 engs.LookupOrCreate（旧行为）；
// 非空时走 rotator.EnsureActive，proxy 模式按阈值滚动 engagement。
type Deps struct {
	Redis    *redis.Client
	Cfg      config.IngestorConfig
	Stream   string
	Tenant   string
	Engs     *engagement.Store
	Rotator  *engagement.Rotator
	Flows    *flow.Store
	Tasks    *agentrun.Store
	Enqueuer *worker.Client
	Logger   zerolog.Logger
}

// NewTraffic 构造并 ensure consumer group 存在。
func NewTraffic(ctx context.Context, deps Deps) (*Traffic, error) {
	if deps.Redis == nil || deps.Engs == nil || deps.Flows == nil ||
		deps.Tasks == nil || deps.Enqueuer == nil {
		return nil, errors.New("ingestor.NewTraffic: redis/engs/flows/tasks/enqueuer 必填")
	}
	if strings.TrimSpace(deps.Stream) == "" {
		return nil, errors.New("ingestor.NewTraffic: stream 必填（应来自 cfg.Proxy.StreamName）")
	}
	tenant := strings.TrimSpace(deps.Tenant)
	if tenant == "" {
		tenant = "default"
	}
	t := &Traffic{
		rdb:           deps.Redis,
		stream:        deps.Stream,
		group:         deps.Cfg.ConsumerGroup,
		name:          deps.Cfg.ConsumerName,
		tenant:        tenant,
		readBatch:     int64(deps.Cfg.ReadBatch),
		readBlock:     time.Duration(deps.Cfg.ReadBlockTimeoutMs) * time.Millisecond,
		retryDelay:    time.Duration(deps.Cfg.RetryDelayMs) * time.Millisecond,
		recreateDelay: time.Duration(deps.Cfg.RecreateGroupDelayMs) * time.Millisecond,
		engs:          deps.Engs,
		rotator:       deps.Rotator,
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

// handleMessage 处理单条 stream entry：落 http_flow + 入主任务。
//
// 不做二次过滤：proxy 端 filter chain 已经把无关流量（静态资源、心跳、
// websocket、超大 body 等）拦在外面，能进 stream 的都直接入主 ReAct 队列。
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

	// 1) 落 engagement + http_flow
	// rotator 非 nil 时由它决定是否轮转（proxy 模式按阈值），否则退化到 engs.LookupOrCreate。
	var (
		eid    string
		ensErr error
	)
	if t.rotator != nil {
		eid, ensErr = t.rotator.EnsureActive(ctx, t.tenant, snap.Host, engagement.ModeProxy)
	} else {
		var eng engagement.Engagement
		eng, ensErr = t.engs.LookupOrCreate(ctx, t.tenant, snap.Host, engagement.ModeProxy)
		if ensErr == nil {
			eid = eng.ID
		}
	}
	if err := ensErr; err != nil {
		t.logger.Warn().Err(err).Str("host", snap.Host).Msg("engagement EnsureActive/LookupOrCreate 失败")
		return
	}
	flowID, err := t.appendFlow(ctx, eid, &snap)
	if err != nil {
		t.logger.Warn().Err(err).Msg("flow.Append 失败")
		return
	}

	// 2) 创建主 react task + 入 Asynq
	if err := t.enqueueMain(ctx, eid, flowID, &snap); err != nil {
		t.logger.Warn().Err(err).Str("eid", eid).Int64("flow_id", flowID).Msg("主任务入队失败")
		return
	}
	t.logger.Info().
		Str("eid", eid).
		Int64("flow_id", flowID).
		Str("method", snap.Method).Str("url", snap.URI).
		Msg("流量已入主 ReAct 队列")
}

func (t *Traffic) appendFlow(ctx context.Context, eid string, snap *proxy.TrafficSnapshot) (int64, error) {
	reqH, _ := json.Marshal(snap.RequestHeaders)
	respH, _ := json.Marshal(snap.ResponseHeaders)
	return t.flows.Append(ctx, flow.Flow{
		EngagementID:    eid,
		CreatedAt:       snap.Timestamp,
		Method:          snap.Method,
		URL:             fullURL(snap),
		RequestHeaders:  reqH,
		RequestBody:     snap.RequestBody,
		StatusCode:      snap.StatusCode,
		ResponseHeaders: respH,
		ResponseBody:    snap.ResponseBody,
	})
}

func (t *Traffic) enqueueMain(ctx context.Context, eid string, flowID int64, snap *proxy.TrafficSnapshot) error {
	entrypoint, _ := json.Marshal(map[string]any{
		"flow_id": flowID,
		"host":    snap.Host,
		"method":  snap.Method,
		"url":     snap.URI,
	})
	payloadInput, _ := json.Marshal(map[string]any{
		"mode":       "traffic",
		"entrypoint": json.RawMessage(entrypoint),
	})

	tid, err := t.tasks.Create(ctx, agentrun.NewParams{
		EngagementID: eid,
		Role:         string(worker.RoleHunter),
		Skill:        "hunter",
		Input:        payloadInput,
	})
	if err != nil {
		return fmt.Errorf("tasks.Create: %w", err)
	}

	if _, _, err := t.enq.Enqueue(ctx, worker.RoleHunter, worker.Payload{
		TaskID:       tid,
		EngagementID: eid,
		Input:        payloadInput,
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
