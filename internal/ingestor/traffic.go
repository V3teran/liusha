// Package ingestor 是 Stream 流量消费者：
//
// 业界最佳实践（Stream-based）：proxy 进程无状态把过滤后的 snapshot XADD 到 Redis Stream；
// ingestor 作为消费者把每条 snap 落库（engagement/http_flow/traffic_window）+ 切窗 +
// 投递 Asynq sniffer 任务。
//
// 在 scanner 进程内以 goroutine 运行：
//
//	consumer.Run(ctx)  // 阻塞 XREADGROUP，处理 snap → ack
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
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/proxy"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/window"
	"github.com/V3teran/liusha/internal/worker"
)

// 默认 group / consumer 名 + 读取参数。
const (
	defaultGroup        = "liusha-flowconsumer"
	defaultConsumerName = "flowconsumer-1"
	readBatch           = 16
	readBlockTimeout    = 1 * time.Second
)

// 单租户：与 cmd/proxy/proxy_flush 的旧约定一致，所有 engagement 都挂在 default 租户下。
const defaultTenant = "default"

// Consumer 跑 XREADGROUP 循环。
type Consumer struct {
	rdb     *redis.Client
	stream  string
	group   string
	name    string
	engs    *engagement.Store
	flows   *flow.Store
	windows *window.Store
	tasks   *task.Store
	enq     *worker.Client
	cfg     config.ProxyConfig
	logger  zerolog.Logger
}

// Deps 注入依赖；rdb / engs / flows / windows / tasks / enq 必填。
type Deps struct {
	Redis    *redis.Client
	Stream   string // 默认 proxy.FlowStream
	Group    string // 默认 defaultGroup
	Consumer string // 默认 defaultConsumerName
	Engs     *engagement.Store
	Flows    *flow.Store
	Windows  *window.Store
	Tasks    *task.Store
	Enqueuer *worker.Client
	Cfg      config.ProxyConfig
	Logger   zerolog.Logger
}

// NewConsumer 构造 Consumer 并 ensure consumer group 存在（idempotent）。
func NewConsumer(ctx context.Context, deps Deps) (*Consumer, error) {
	if deps.Redis == nil || deps.Engs == nil || deps.Flows == nil || deps.Windows == nil || deps.Tasks == nil || deps.Enqueuer == nil {
		return nil, errors.New("ingestor.NewConsumer: redis/engs/flows/windows/tasks/enqueuer 必填")
	}
	c := &Consumer{
		rdb:     deps.Redis,
		stream:  pickNonEmpty(deps.Stream, proxy.FlowStream),
		group:   pickNonEmpty(deps.Group, defaultGroup),
		name:    pickNonEmpty(deps.Consumer, defaultConsumerName),
		engs:    deps.Engs,
		flows:   deps.Flows,
		windows: deps.Windows,
		tasks:   deps.Tasks,
		enq:     deps.Enqueuer,
		cfg:     deps.Cfg,
		logger:  deps.Logger,
	}
	// XGroupCreateMkStream 幂等：BUSYGROUP 表示已存在，直接吞掉。
	if err := c.rdb.XGroupCreateMkStream(ctx, c.stream, c.group, "$").Err(); err != nil {
		if !strings.Contains(err.Error(), "BUSYGROUP") {
			return nil, fmt.Errorf("create consumer group %s/%s: %w", c.stream, c.group, err)
		}
	}
	return c, nil
}

// Run 阻塞读 stream → 处理每条 snap → ack；ctx 取消即返回。
//
// 单消费者足够 v1 用量；redis BLOCK 1s 让 ctx 取消有响应。
func (c *Consumer) Run(ctx context.Context) error {
	c.logger.Info().Str("stream", c.stream).Str("group", c.group).Msg("flowconsumer 已启动")
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		streams, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    c.group,
			Consumer: c.name,
			Streams:  []string{c.stream, ">"},
			Count:    readBatch,
			Block:    readBlockTimeout,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) || errors.Is(err, context.Canceled) {
				continue
			}
			c.logger.Warn().Err(err).Msg("XREADGROUP 失败")
			time.Sleep(500 * time.Millisecond) // 防止暴风式重试
			continue
		}

		for _, s := range streams {
			for _, msg := range s.Messages {
				c.handleMessage(ctx, msg)
			}
		}
	}
}

// handleMessage 处理单条 stream entry，成功 / 解析失败都 ACK（避免毒消息卡死流）。
func (c *Consumer) handleMessage(ctx context.Context, msg redis.XMessage) {
	defer func() {
		if err := c.rdb.XAck(ctx, c.stream, c.group, msg.ID).Err(); err != nil {
			c.logger.Warn().Err(err).Str("msg_id", msg.ID).Msg("XACK 失败")
		}
	}()

	raw, ok := msg.Values["snap"]
	if !ok {
		c.logger.Warn().Str("msg_id", msg.ID).Msg("stream entry 缺 snap 字段")
		return
	}
	rawStr, ok := raw.(string)
	if !ok {
		c.logger.Warn().Str("msg_id", msg.ID).Msg("snap 字段类型异常")
		return
	}

	var snap proxy.TrafficSnapshot
	if err := json.Unmarshal([]byte(rawStr), &snap); err != nil {
		c.logger.Warn().Err(err).Str("msg_id", msg.ID).Msg("snap json 解析失败")
		return
	}
	c.applySnapshot(ctx, &snap)
}

// applySnapshot 把 snapshot 落库 + 切窗 + 必要时 enqueue sniffer。
//
//	1. engagement.LookupOrCreate（按 host 懒建）
//	2. flow.Append（写 http_flow，store 内部按上限截断）
//	3. window.OpenOrAppend（切 traffic_window；满 batchLimit 即 closed）
//	4. justClosed → enqueue sniffer
//
// 任意单步失败仅 warn，不抛错（XACK 会照常发，避免毒消息卡死流）。
func (c *Consumer) applySnapshot(ctx context.Context, snap *proxy.TrafficSnapshot) {
	if snap == nil || snap.Host == "" {
		c.logger.Warn().Msg("snapshot host 为空，跳过")
		return
	}

	eng, err := c.engs.LookupOrCreate(ctx, defaultTenant, snap.Host, engagement.ModeProxy)
	if err != nil {
		c.logger.Warn().Err(err).Str("host", snap.Host).Msg("engagement LookupOrCreate 失败")
		return
	}

	flowID, err := c.appendFlow(ctx, eng.ID, snap)
	if err != nil {
		c.logger.Warn().Err(err).Str("eid", eng.ID).Msg("flow 入库失败")
		return
	}

	wid, justClosed, err := c.windows.OpenOrAppend(ctx, eng.ID, window.FlowRef{
		ID:     flowID,
		Method: snap.Method,
		URL:    fullURL(snap),
	}, c.cfg.WindowBatch)
	if err != nil {
		c.logger.Warn().Err(err).Str("eid", eng.ID).Msg("window OpenOrAppend 失败")
		return
	}
	if justClosed {
		c.enqueueSniffer(ctx, eng.ID, wid)
	}
}

func (c *Consumer) appendFlow(ctx context.Context, eid string, snap *proxy.TrafficSnapshot) (int64, error) {
	reqH, err := json.Marshal(snap.RequestHeaders)
	if err != nil {
		return 0, fmt.Errorf("marshal request headers: %w", err)
	}
	respH, err := json.Marshal(snap.ResponseHeaders)
	if err != nil {
		return 0, fmt.Errorf("marshal response headers: %w", err)
	}
	return c.flows.Append(ctx, flow.Flow{
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

func (c *Consumer) enqueueSniffer(ctx context.Context, eid, windowID string) {
	taskID, input, err := EnqueueSnifferTask(ctx, c.tasks, c.enq, eid, windowID)
	if err != nil {
		c.logger.Warn().Err(err).Str("eid", eid).Str("window_id", windowID).Msg("sniffer 入队失败")
		return
	}
	c.logger.Info().Str("eid", eid).Str("window_id", windowID).Str("task_id", taskID).
		Int("input_bytes", len(input)).Msg("窗口关闭，已投递 sniffer 任务")
}

// EnqueueSnifferTask 是统一的"投递 sniffer 任务"入口（consumer + ager 共用）：
//
//	1. tasks.Create 在 PG 插一行 pending 的 agent_task（role=sniffer，skill=空），返回新 uuid
//	2. worker.Client.Enqueue 把同一 uuid 作为 asynq TaskID 投到 sniffer 队列
//
// 这样消费 worker 进入 handler 时 SetRunning 能找到 pending 行。
//
// 返回 (taskID, inputJSON, err)。
func EnqueueSnifferTask(ctx context.Context, tasks *task.Store, enq *worker.Client, eid, windowID string) (string, []byte, error) {
	input, err := json.Marshal(map[string]string{"window_id": windowID})
	if err != nil {
		return "", nil, fmt.Errorf("marshal sniffer input: %w", err)
	}
	tid, err := tasks.Create(ctx, task.NewParams{
		EngagementID: eid,
		Role:         string(worker.RoleSniffer),
		Skill:        "",
		Input:        input,
	})
	if err != nil {
		return "", nil, fmt.Errorf("create agent_task: %w", err)
	}
	if _, _, err := enq.Enqueue(ctx, worker.RoleSniffer, worker.Payload{
		TaskID:       tid,
		EngagementID: eid,
		Input:        input,
	}); err != nil {
		return tid, input, fmt.Errorf("enqueue asynq: %w", err)
	}
	return tid, input, nil
}

func fullURL(snap *proxy.TrafficSnapshot) string {
	if snap.Scheme != "" && snap.Host != "" {
		return snap.Scheme + "://" + snap.Host + snap.URI
	}
	if snap.Host != "" {
		return "//" + snap.Host + snap.URI
	}
	return snap.URI
}

func pickNonEmpty(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
