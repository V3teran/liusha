// Package ingestor 是 Stream 流量摄入器：
//
//	proxy XADD → liusha:flow_events
//	ingestor.Traffic XREADGROUP → 落库 http_flow → 启发式打分 →
//	    score >= threshold → tasks.Create(role=main) →
//	    Asynq react queue → scanner 主 ReAct
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

	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/heuristic"
	"github.com/V3teran/liusha/internal/proxy"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"
)

const (
	defaultGroup        = "liusha-ingestor"
	defaultConsumerName = "ingestor-1"
	readBatch           = 16
	readBlockTimeout    = 1 * time.Second
	defaultTenant       = "default"
)

// Traffic 是流量入口的 Stream 消费者。
type Traffic struct {
	rdb    *redis.Client
	stream string
	group  string
	name   string
	engs   *engagement.Store
	flows  *flow.Store
	tasks  *task.Store
	enq    *worker.Client
	logger zerolog.Logger
}

// Deps 注入。
type Deps struct {
	Redis    *redis.Client
	Stream   string
	Group    string
	Consumer string
	Engs     *engagement.Store
	Flows    *flow.Store
	Tasks    *task.Store
	Enqueuer *worker.Client
	Logger   zerolog.Logger
}

// NewTraffic 构造并 ensure consumer group 存在。
func NewTraffic(ctx context.Context, deps Deps) (*Traffic, error) {
	if deps.Redis == nil || deps.Engs == nil || deps.Flows == nil ||
		deps.Tasks == nil || deps.Enqueuer == nil {
		return nil, errors.New("ingestor.NewTraffic: redis/engs/flows/tasks/enqueuer 必填")
	}
	t := &Traffic{
		rdb:    deps.Redis,
		stream: pickNonEmpty(deps.Stream, proxy.FlowStream),
		group:  pickNonEmpty(deps.Group, defaultGroup),
		name:   pickNonEmpty(deps.Consumer, defaultConsumerName),
		engs:   deps.Engs,
		flows:  deps.Flows,
		tasks:  deps.Tasks,
		enq:    deps.Enqueuer,
		logger: deps.Logger,
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
			Count:    readBatch,
			Block:    readBlockTimeout,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) || errors.Is(err, context.Canceled) {
				continue
			}
			t.logger.Warn().Err(err).Msg("XREADGROUP 失败")
			time.Sleep(500 * time.Millisecond)
			continue
		}
		for _, s := range streams {
			for _, msg := range s.Messages {
				t.handleMessage(ctx, msg)
			}
		}
	}
}

// handleMessage 处理单条 stream entry：落 http_flow + 启发式打分 + 入主任务。
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
	eng, err := t.engs.LookupOrCreate(ctx, defaultTenant, snap.Host, engagement.ModeProxy)
	if err != nil {
		t.logger.Warn().Err(err).Str("host", snap.Host).Msg("engagement.LookupOrCreate 失败")
		return
	}
	flowID, err := t.appendFlow(ctx, eng.ID, &snap)
	if err != nil {
		t.logger.Warn().Err(err).Msg("flow.Append 失败")
		return
	}

	// 2) 启发式打分；< threshold → 不入队
	score := heuristic.Score(snapshotFlow{snap: &snap})
	if score < heuristic.Threshold() {
		t.logger.Debug().
			Int("score", score).
			Str("url", snap.URI).
			Msg("启发式过滤：分数不足，不入主队列")
		return
	}

	// 3) 创建主 react task + 入 Asynq
	if err := t.enqueueMain(ctx, eng.ID, flowID, &snap); err != nil {
		t.logger.Warn().Err(err).Str("eid", eng.ID).Int64("flow_id", flowID).Msg("主任务入队失败")
		return
	}
	t.logger.Info().
		Str("eid", eng.ID).
		Int64("flow_id", flowID).
		Int("score", score).
		Str("method", snap.Method).Str("url", snap.URI).
		Msg("流量已入主 ReAct 队列")
}

// snapshotFlow 实现 heuristic.Flow（包级 helper，不导出）。
type snapshotFlow struct{ snap *proxy.TrafficSnapshot }

func (s snapshotFlow) GetMethod() string         { return s.snap.Method }
func (s snapshotFlow) GetURL() string            { return s.snap.URI }
func (s snapshotFlow) GetHeader(k string) string { return s.snap.RequestHeaders[strings.ToLower(k)] }

func (t *Traffic) appendFlow(ctx context.Context, eid string, snap *proxy.TrafficSnapshot) (int64, error) {
	reqH, _ := json.Marshal(snap.RequestHeaders)
	respH, _ := json.Marshal(snap.ResponseHeaders)
	return t.flows.Append(ctx, flow.Flow{
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

	tid, err := t.tasks.Create(ctx, task.NewParams{
		EngagementID: eid,
		Role:         string(worker.RoleMain),
		Skill:        "",
		Input:        payloadInput,
	})
	if err != nil {
		return fmt.Errorf("tasks.Create: %w", err)
	}

	if _, _, err := t.enq.Enqueue(ctx, worker.RoleMain, worker.Payload{
		TaskID:       tid,
		EngagementID: eid,
		Input:        payloadInput,
	}); err != nil {
		return fmt.Errorf("enq.Enqueue: %w", err)
	}
	return nil
}

func fullURL(s *proxy.TrafficSnapshot) string {
	if s.Scheme != "" && s.Host != "" {
		return s.Scheme + "://" + s.Host + s.URI
	}
	if s.Host != "" {
		return "//" + s.Host + s.URI
	}
	return s.URI
}

func pickNonEmpty(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
