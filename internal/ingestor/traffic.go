// Package ingestor 是 Stream 流量摄入器，双来源分流（见 spec §5.4）：
//
//	proxy 责任链放行 → XADD → <stream_name>
//	ingestor.Traffic XREADGROUP → 按 source 分流：
//	  external（代理捕获真实流量）→ 落 proxy_traffic（按 host）→ 喂 Redis 聚合窗口
//	                                → 攒批（20 条/10s）→ 建 passive task → 回填 consumed_by_task_id → enqueue
//	  internal（agent sandbox 自产）→ 反查 hunter 得 task_id → 落 agent_traffic（不 enqueue，防自激震荡）
//
// 这里不再做二次过滤：是否丢弃流量完全由 proxy 端 filter chain 决定。
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

	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/hunter"
	"github.com/V3teran/liusha/internal/proxy"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"
)

// ConversationCreator 建 passive task 的对话流。聚合器建 task 后建一条 conversation，
// passive agent 过程事件落进去，前端可打开实时观察 + 插话。nil 时跳过（向后兼容）。
type ConversationCreator interface {
	CreateConversation(ctx context.Context, title, taskID, roleID string) (conversation.Conversation, error)
}

// internalQueueSize 是进程内 internal 流量队列容量。
// active 沙箱抓的流量经 SubmitInternal 入队，由 drain goroutine 调 handleInternalSnap 落库——
// 不再绕 redis（ingest_handler 与 ingestor 同进程，自环 broker 是冗余）。
// 有界 → 满时 SubmitInternal 返 false，handler 回 503 给沙箱明确背压信号。
const internalQueueSize = 1024

// Traffic 是流量入口的 Stream 消费者。
type Traffic struct {
	rdb           *redis.Client
	stream        string
	group         string
	name          string
	internalCh    chan *proxy.TrafficSnapshot
	readBatch     int64
	readBlock     time.Duration
	retryDelay    time.Duration
	recreateDelay time.Duration

	assignments   *assignment.Store    // 聚合建 passive assignment（一切 task 皆属某 assignment）
	tasks         *task.Store          // passive task 建/查
	agg           *aggregator          // 按 host 攒批窗口（Redis）
	proxyFlows    *flow.ProxyStore     // 代理捕获流量落库 + 领取
	agentFlows    *flow.AgentStore     // agent 自产流量落库
	hunters       *hunter.Store        // internal 流量反查 hunter→task_id
	conversations ConversationCreator  // 建 passive task 对话流（nil 跳过）
	enq           *worker.Client
	logger        zerolog.Logger
}

// Deps 注入。Stream 必填（来自 cfg.Proxy.StreamName）；Cfg 提供 group/consumer/batch/聚合参数。
type Deps struct {
	Redis         *redis.Client
	Cfg           config.IngestorConfig
	Stream        string
	Tenant        string // Redis key 前缀（聚合窗口 + 锁）
	Assignments   *assignment.Store
	Tasks         *task.Store
	ProxyFlows    *flow.ProxyStore
	AgentFlows    *flow.AgentStore
	Hunters       *hunter.Store
	Conversations ConversationCreator
	Enqueuer      *worker.Client
	Logger        zerolog.Logger
}

// NewTraffic 构造并 ensure consumer group 存在。
func NewTraffic(ctx context.Context, deps Deps) (*Traffic, error) {
	if deps.Redis == nil || deps.Assignments == nil || deps.Tasks == nil || deps.ProxyFlows == nil ||
		deps.AgentFlows == nil || deps.Hunters == nil || deps.Enqueuer == nil {
		return nil, errors.New("ingestor.NewTraffic: redis/assignments/tasks/proxyFlows/agentFlows/hunters/enqueuer 必填")
	}
	if strings.TrimSpace(deps.Stream) == "" {
		return nil, errors.New("ingestor.NewTraffic: stream 必填（应来自 cfg.Proxy.StreamName）")
	}
	prefix := deps.Tenant
	if prefix == "" {
		prefix = "liusha"
	}
	window := time.Duration(deps.Cfg.AggregateWindowSeconds) * time.Second
	t := &Traffic{
		rdb:           deps.Redis,
		stream:        deps.Stream,
		group:         deps.Cfg.ConsumerGroup,
		name:          deps.Cfg.ConsumerName,
		readBatch:     int64(deps.Cfg.ReadBatch),
		readBlock:     time.Duration(deps.Cfg.ReadBlockTimeoutMs) * time.Millisecond,
		retryDelay:    time.Duration(deps.Cfg.RetryDelayMs) * time.Millisecond,
		recreateDelay: time.Duration(deps.Cfg.RecreateGroupDelayMs) * time.Millisecond,
		assignments:   deps.Assignments,
		tasks:         deps.Tasks,
		agg:           newAggregator(deps.Redis, prefix, deps.Cfg.AggregateBatchSize, window),
		proxyFlows:    deps.ProxyFlows,
		agentFlows:    deps.AgentFlows,
		hunters:       deps.Hunters,
		conversations: deps.Conversations,
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
func (t *Traffic) SubmitInternal(snap *proxy.TrafficSnapshot) bool {
	select {
	case t.internalCh <- snap:
		return true
	default:
		return false
	}
}

// drainInternal 消费进程内 internal 队列 → handleInternalSnap 落库。ctx 取消即返回。
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

// sweepLoop 是超时补偿的时钟源：定时调 agg.sweepExpired 找出窗口已超时的 host，
// 对每个走 claimAndReset（跨实例幂等抢锁）+ spawnPassiveTask，兜住静默 host 的尾批。
//
// 间隔取 window 的 1/3（至少 1s）——保证超时后最多 window/3 延迟即触发，又不空转太频。
// ctx 取消时退出前再 sweep 一轮，不丢关停瞬间的尾批（对齐单实例 stop-flush 语义）。
func (t *Traffic) sweepLoop(ctx context.Context) {
	interval := t.agg.window / 3
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.sweepOnce(context.Background()) // 关停前兜最后一轮尾批
			return
		case <-ticker.C:
			t.sweepOnce(ctx)
		}
	}
}

// sweepOnce 执行一轮超时扫描：对每个超时 host 抢锁建 task（抢不到=别的实例正在处理，跳过）。
func (t *Traffic) sweepOnce(ctx context.Context) {
	hosts, err := t.agg.sweepExpired(ctx)
	if err != nil {
		t.logger.Warn().Err(err).Msg("聚合超时扫描失败（下轮重试）")
		return
	}
	for _, host := range hosts {
		won, err := t.agg.claimAndReset(ctx, host)
		if err != nil {
			t.logger.Warn().Err(err).Str("host", host).Msg("超时补偿抢锁失败")
			continue
		}
		if !won {
			continue // 别的实例正在建 task
		}
		t.logger.Info().Str("host", host).Msg("窗口超时，超时补偿触发建 passive task")
		t.spawnPassiveTask(ctx, host)
	}
}

// Run 阻塞读 stream → 处理每条 snap → ack；ctx 取消即返回。
// 同时启动 drainInternal goroutine 消费进程内 internal 队列（passive 走 redis，active 走 channel）。
func (t *Traffic) Run(ctx context.Context) error {
	t.logger.Info().Str("stream", t.stream).Str("group", t.group).Msg("ingestor.traffic 已启动")
	go t.drainInternal(ctx)
	go t.sweepLoop(ctx) // 超时补偿：定时扫活跃 host 窗口，兜住静默 host 的尾批
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

// handleMessage 处理单条 stream entry：按 source 分流。
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

	source := snap.Source
	if source == "" {
		source = "external" // 兼容老 snap 无 source 字段
	}
	if source == "internal" {
		t.handleInternalSnap(ctx, &snap)
		return
	}
	t.handleExternalSnap(ctx, &snap)
}

// handleExternalSnap 处理代理捕获流量：落 proxy_traffic（按 host）+ 喂聚合窗口，达阈值则建 passive task。
func (t *Traffic) handleExternalSnap(ctx context.Context, snap *proxy.TrafficSnapshot) {
	reqH, _ := json.Marshal(snap.RequestHeaders)
	respH, _ := json.Marshal(snap.ResponseHeaders)
	if _, err := t.proxyFlows.Append(ctx, flow.ProxyTraffic{
		Host:            snap.Host,
		Method:          snap.Method,
		Scheme:          snap.Scheme,
		URL:             fullURL(snap),
		Path:            snap.Path,
		StatusCode:      snap.StatusCode,
		RequestHeaders:  reqH,
		RequestBody:     snap.RequestBody,
		ResponseHeaders: respH,
		ResponseBody:    snap.ResponseBody,
		DurationMs:      int(snap.Duration.Milliseconds()),
	}); err != nil {
		t.logger.Warn().Err(err).Str("host", snap.Host).Msg("proxy_traffic 落库失败，跳过本流量")
		return
	}

	// 喂聚合窗口：达 batch 阈值或超窗口时长 → 抢锁建 task。
	w, err := t.agg.observe(ctx, snap.Host)
	if err != nil {
		t.logger.Warn().Err(err).Str("host", snap.Host).Msg("聚合窗口 observe 失败（流量已落库，等下条重试触发）")
		return
	}
	if !w.Triggered {
		return
	}
	won, err := t.agg.claimAndReset(ctx, snap.Host)
	if err != nil {
		t.logger.Warn().Err(err).Str("host", snap.Host).Msg("聚合抢锁失败")
		return
	}
	if !won {
		return // 别的实例正在建 task
	}
	t.spawnPassiveTask(ctx, snap.Host)
}

// spawnPassiveTask 为某 host 攒够的一批流量建 passive task：
// 建 task → 领取该 host 未消费流量回填 consumed_by_task_id → 绑对话 → enqueue traffic-analysis。
//
// 领取用条件更新（consumed_by_task_id IS NULL），跨实例幂等（§13.2）。领到 0 条说明流量已被
// 别的 task 消费或全部滞留锁定，回滚建的 task（abort）避免空任务。
func (t *Traffic) spawnPassiveTask(ctx context.Context, host string) {
	// 一切下发皆走 assignment（§3.1）：聚合器建 assignment(passive, auto, [host]) → 1 task（fan-in）。
	asg, err := t.assignments.Create(ctx, assignment.NewParams{
		Mode:   assignment.ModePassive,
		Source: assignment.SourceAuto,
		Items:  []assignment.Item{{Host: host}},
		Title:  host,
	})
	if err != nil {
		t.logger.Warn().Err(err).Str("host", host).Msg("建 passive assignment 失败")
		return
	}
	tk, err := t.tasks.Create(ctx, task.NewParams{Mode: task.ModePassive, AssignmentID: asg.ID, TargetHost: host})
	if err != nil {
		t.logger.Warn().Err(err).Str("host", host).Msg("建 passive task 失败")
		return
	}
	claimed, err := t.proxyFlows.ClaimUnconsumedByHost(ctx, tk.ID, host, t.agg.batchN())
	if err != nil {
		t.logger.Warn().Err(err).Str("task_id", tk.ID).Msg("领取 proxy_traffic 失败，abort 本 task")
		_ = t.tasks.Abort(ctx, tk.ID, "领取流量失败")
		return
	}
	if claimed == 0 {
		_ = t.tasks.Abort(ctx, tk.ID, "无未消费流量可分析")
		return
	}

	convID := t.ensureConversation(ctx, tk.ID, host)
	if err := t.enqueuePassive(ctx, tk.ID, convID, host); err != nil {
		t.logger.Warn().Err(err).Str("task_id", tk.ID).Msg("passive task 入队失败")
		return
	}
	t.logger.Info().
		Str("task_id", tk.ID).Str("host", host).Int64("flows", claimed).
		Msg("passive task 已建并入 traffic-analysis 队列")
}

// handleInternalSnap 处理 agent sandbox 自产流量：反查 hunter 得 task_id，落 agent_traffic，不 enqueue。
//
// source=internal 唯一来源：browser-svc.py 持 CDP 连接，把 chromium 的 Document/XHR/Fetch（含真实
// 认证凭证位置）→ POST /internal/v1/flows/ingest → snap.Source="internal" + snap.HunterID。
// 反查 hunter 表得 task_id 写 agent_traffic。不 enqueue：agent 自己挖的流量回头触发分析会自激震荡。
func (t *Traffic) handleInternalSnap(ctx context.Context, snap *proxy.TrafficSnapshot) {
	if snap.HunterID == "" {
		t.logger.Warn().Str("host", snap.Host).Str("uri", snap.URI).
			Msg("internal 流量缺 hunter_id，丢弃（browser-svc.py session→hunter 归属异常？）")
		return
	}
	run, err := t.hunters.GetByID(ctx, snap.HunterID)
	if err != nil {
		t.logger.Warn().Err(err).Str("hunter_id", snap.HunterID).
			Msg("internal 流量反查 hunter 失败，丢弃（hunter 已被清理 / 跨进程脏数据？）")
		return
	}

	reqH, _ := json.Marshal(snap.RequestHeaders)
	respH, _ := json.Marshal(snap.ResponseHeaders)
	flowID, err := t.agentFlows.Append(ctx, flow.AgentTraffic{
		TaskID:          run.TaskID,
		HunterID:        snap.HunterID,
		Identity:        snap.Identity,
		Tool:            snap.Tool,
		Host:            snap.Host,
		Path:            snap.Path,
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
		t.logger.Warn().Err(err).Msg("agent_traffic 落库失败")
		return
	}
	t.logger.Info().
		Str("task_id", run.TaskID).Str("hunter_id", snap.HunterID).Int64("flow_id", flowID).
		Str("method", snap.Method).Str("url", snap.URI).
		Msg("internal 流量已入 agent_traffic（不触发 trafficAnalysis）")
}

// ensureConversation 为 passive task 建对话流（title=host）并回填 conversation.task_id。
// conversations nil / 建会话失败 → 返空串（降级：本次不绑，事件不落对话，但流量分析照常）。
func (t *Traffic) ensureConversation(ctx context.Context, taskID, host string) string {
	if t.conversations == nil {
		return ""
	}
	conv, err := t.conversations.CreateConversation(ctx, host, taskID, "")
	if err != nil {
		t.logger.Warn().Err(err).Str("host", host).Msg("建 passive task 对话流失败（降级：本次不绑对话）")
		return ""
	}
	return conv.ID
}

func (t *Traffic) enqueuePassive(ctx context.Context, taskID, convID, host string) error {
	// entrypoint 嵌套与 active 结构对齐（handler 统一抽 input.Entrypoint 传给 mode handler）：
	// active entrypoint={brief}，passive entrypoint={host}。
	entrypoint, _ := json.Marshal(map[string]string{"host": host})
	payloadInput, _ := json.Marshal(map[string]any{
		"mode":       "passive",
		"entrypoint": json.RawMessage(entrypoint),
	})
	hid, err := t.hunters.Create(ctx, hunter.NewParams{
		TaskID: taskID,
		Role:   "traffic-analysis",
		Input:  payloadInput,
	})
	if err != nil {
		return fmt.Errorf("hunters.Create: %w", err)
	}
	if _, _, err := t.enq.Enqueue(ctx, worker.RoleHunter, worker.Payload{
		HunterID:       hid,
		TaskID:         taskID,
		ConversationID: convID,
		Input:          payloadInput,
		Role:           worker.RoleHunter,
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
func isNoGroupErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "NOGROUP")
}

// recreateGroup 在 NOGROUP 自愈分支调用——MKSTREAM 让空 stream 一并创建；起点 "0" 防漏消息。
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
