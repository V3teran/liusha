// Package ingestor 是 Stream 流量摄入器，双来源分流：
//
//	proxy 责任链放行 → XADD → <stream_name>
//	ingestor.Traffic XREADGROUP → 按 source 分流：
//	  external（代理捕获的真实流量）→ 落 proxy_traffic（按 host）→ 喂 Redis 聚合窗口
//	                                → 攒批（20 条/10s）→ 建代理捕获 task → 领取流量（traffic_task 关联）→ enqueue
//	  internal（agent sandbox 自产）→ 反查 agent 得 task_id → 落 agent_traffic（不 enqueue，防自激震荡）
//
// 这里不做二次过滤：是否丢弃流量完全由 proxy 端 filter chain 决定。
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
	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/proxy"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/worker"
)

// ConversationCreator 建代理捕获 task 的会话流。聚合器建 task 后建一条 conversation，
// 代理捕获 agent 过程事件落进去，前端可打开实时观察 + 插话。nil 时跳过（单测按需部分装配；生产必注入）。
//
// AppendMessage 用于写会话首条「任务说明」消息（role=user）——代理捕获是流量驱动自动建 task、
// 没有用户输入，但有确定的任务目标（host + 认领的流量清单）；它作为首条右侧气泡，让前端会话
// 有「发起了什么」的锚点（对齐人工下发的首条用户消息）。
type ConversationCreator interface {
	CreateConversation(ctx context.Context, title, taskID string) (conversation.Conversation, error)
	AppendMessage(ctx context.Context, conversationID string, role conversation.Role, kind conversation.Kind, content string, metadata json.RawMessage) (conversation.Message, error)
}

// internalQueueSize 是进程内 internal 流量队列容量。
// sandbox 抓的流量经 SubmitInternal 入队，由 drain goroutine 调 handleInternalSnap 落库——
// 不再绕 redis（ingest_handler 与 ingestor 同进程，自环 broker 是冗余）。
// 有界 → 满时 SubmitInternal 返 false，handler 回 503 给沙箱明确背压信号。
const internalQueueSize = 1024

// PEL 回收参数：处理失败未 ACK 的消息滞留超过 reclaimMinIdle 后由 reclaimLoop
// XAUTOCLAIM 重投；单消息处理（毫秒级）远小于该阈值，不会误回收在途消息。
const (
	reclaimMinIdle  = 60 * time.Second
	reclaimInterval = 30 * time.Second
)

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

	assignments   *assignment.Store   // 聚合建代理捕获 assignment（一切 task 皆属某 assignment）
	tasks         *task.Store         // 代理捕获 task 建/查
	agg           *aggregator         // 按 host 攒批窗口（Redis）
	proxyStore    *traffic.ProxyStore // 代理捕获流量落库 + 领取
	agentStore    *traffic.AgentStore // agent 自产流量落库
	agentRuns     *agentrun.Store     // internal 流量反查 run→task_id
	conversations ConversationCreator // 建代理捕获 task 会话流（nil 跳过）
	enq           *worker.Client
	logger        zerolog.Logger
}

// Deps 注入。Stream 必填（来自 cfg.Proxy.StreamName）；Cfg 提供 group/consumer/batch/聚合参数。
type Deps struct {
	Redis         *redis.Client
	Cfg           config.IngestorConfig
	Stream        string
	Assignments   *assignment.Store
	Tasks         *task.Store
	ProxyStore    *traffic.ProxyStore
	AgentStore    *traffic.AgentStore
	AgentRuns     *agentrun.Store
	Conversations ConversationCreator
	Enqueuer      *worker.Client
	Logger        zerolog.Logger
}

// NewTraffic 构造并 ensure consumer group 存在。
func NewTraffic(ctx context.Context, deps Deps) (*Traffic, error) {
	if deps.Redis == nil || deps.Assignments == nil || deps.Tasks == nil || deps.ProxyStore == nil ||
		deps.AgentStore == nil || deps.AgentRuns == nil || deps.Enqueuer == nil {
		return nil, errors.New("ingestor.NewTraffic: redis/assignments/tasks/proxyStore/agentStore/agentRuns/enqueuer 必填")
	}
	if strings.TrimSpace(deps.Stream) == "" {
		return nil, errors.New("ingestor.NewTraffic: stream 必填（应来自 cfg.Proxy.StreamName）")
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
		agg:           newAggregator(deps.Redis, deps.Cfg.AggregateBatchSize, window, deps.Logger),
		proxyStore:    deps.ProxyStore,
		agentStore:    deps.AgentStore,
		agentRuns:     deps.AgentRuns,
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
// 返回 false 表示队列已满——调用方（runner ingest_handler）据此回 503，给沙箱背压信号。
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
// 对每个走 claimAndReset（跨实例幂等抢锁）+ spawnProxyTask，兜住静默 host 的尾批。
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
		t.logger.Info().Str("host", host).Msg("窗口超时，超时补偿触发建代理捕获 task")
		t.spawnProxyTask(ctx, host)
	}
}

// Run 阻塞读 stream → 处理每条 snap → 成功才 ack；ctx 取消即返回。
// 同时启动 drainInternal goroutine 消费进程内 internal 队列（external 流量走 redis stream，sandbox 自产的 internal 流量走进程内 channel），
// 以及 reclaimLoop 回收 PEL 滞留消息（处理失败未 ACK / 崩溃残留）。
func (t *Traffic) Run(ctx context.Context) error {
	t.logger.Info().Str("stream", t.stream).Str("group", t.group).Msg("ingestor.traffic 已启动")
	go t.drainInternal(ctx)
	go t.sweepLoop(ctx)   //nolint:gosec // 长生命周期 ctx（随 Run 取消），非 request-scoped
	go t.reclaimLoop(ctx) //nolint:gosec // 同上
	// 超时补偿：定时扫活跃 host 窗口，兜住静默 host 的尾批
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
				t.process(ctx, msg)
			}
		}
	}
}

// process 处理并 ACK 单条消息：处理成功或永久失败（毒消息，重投无意义）才 ACK；
// 瞬时故障（落库失败等）不 ACK，消息留在 PEL 由 reclaimLoop 重投——at-least-once，
// 重投可能产生重复 proxy_traffic 行，换取瞬时故障下流量不丢失。
func (t *Traffic) process(ctx context.Context, msg redis.XMessage) {
	if err := t.handleMessage(ctx, msg); err != nil {
		t.logger.Warn().Err(err).Str("msg_id", msg.ID).Msg("处理失败，留在 PEL 等待重投")
		return
	}
	if err := t.rdb.XAck(ctx, t.stream, t.group, msg.ID).Err(); err != nil {
		t.logger.Warn().Err(err).Str("msg_id", msg.ID).Msg("XACK 失败（消息留在 PEL，等待回收重投）")
	}
}

// reclaimLoop 周期 XAUTOCLAIM 回收 PEL 中滞留超过 reclaimMinIdle 的消息
// （处理失败未 ACK / 消费者崩溃残留），重新走 process。单消息处理远快于
// reclaimMinIdle，不会误回收在途消息。
func (t *Traffic) reclaimLoop(ctx context.Context) {
	ticker := time.NewTicker(reclaimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		start := "0-0"
		for {
			msgs, next, err := t.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
				Stream:   t.stream,
				Group:    t.group,
				Consumer: t.name,
				MinIdle:  reclaimMinIdle,
				Start:    start,
				Count:    t.readBatch,
			}).Result()
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					t.logger.Warn().Err(err).Msg("XAUTOCLAIM 失败（下轮重试）")
				}
				break
			}
			for _, msg := range msgs {
				t.process(ctx, msg)
			}
			if next == "0-0" || len(msgs) == 0 {
				break
			}
			start = next
		}
	}
}

// handleMessage 处理单条 stream entry：按 source 分流。
// 返回 nil = 可安全 ACK（成功，或永久失败）；返回 error = 瞬时故障，不应 ACK。
func (t *Traffic) handleMessage(ctx context.Context, msg redis.XMessage) error {
	raw, ok := msg.Values["snap"].(string)
	if !ok {
		t.logger.Warn().Str("msg_id", msg.ID).Msg("snap 字段缺失，丢弃消息")
		return nil
	}
	var snap proxy.TrafficSnapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.logger.Warn().Err(err).Str("msg_id", msg.ID).Msg("snap 解析失败，丢弃消息")
		return nil
	}

	if snap.Source == "internal" {
		// internal 流量是 best-effort 副产物：缺 AgentRunID/反查失败直接丢弃（见 handleInternalSnap）
		t.handleInternalSnap(ctx, &snap)
		return nil
	}
	return t.handleExternalSnap(ctx, &snap)
}

// handleExternalSnap 处理代理捕获流量：落 proxy_traffic（按 host）+ 喂聚合窗口，达阈值则建代理捕获 task。
// 仅落库失败返回 error（瞬时，留 PEL 重投）；落库之后的下游失败只 warn——流量已安全在库，
// 未消费部分由后续 task 的 ClaimUnconsumedByHost 或超时 sweep 兜底。
func (t *Traffic) handleExternalSnap(ctx context.Context, snap *proxy.TrafficSnapshot) error {
	if _, err := t.proxyStore.Append(ctx, traffic.ProxyTraffic{
		Host:        snap.Host,
		Method:      snap.Method,
		Scheme:      snap.Scheme,
		URL:         fullURL(snap),
		Path:        snap.Path,
		StatusCode:  snap.StatusCode,
		RequestRaw:  snap.RequestRaw(),
		ResponseRaw: snap.ResponseRaw(),
		ContentType: snap.ContentType(),
		HTTPVersion: snap.HTTPVersion,
		RespLen:     int64(len(snap.ResponseBody)),
	}); err != nil {
		return fmt.Errorf("proxy_traffic 落库失败（host=%s）: %w", snap.Host, err)
	}

	// 喂聚合窗口：达 batch 阈值或超窗口时长 → 抢锁建 task。
	w, err := t.agg.observe(ctx, snap.Host)
	if err != nil {
		t.logger.Warn().Err(err).Str("host", snap.Host).Msg("聚合窗口 observe 失败（流量已落库，等下条重试触发）")
		return nil
	}
	if !w.Triggered {
		return nil
	}
	won, err := t.agg.claimAndReset(ctx, snap.Host)
	if err != nil {
		t.logger.Warn().Err(err).Str("host", snap.Host).Msg("聚合抢锁失败")
		return nil
	}
	if !won {
		return nil // 别的实例正在建 task
	}
	t.spawnProxyTask(ctx, snap.Host)
	return nil
}

// spawnProxyTask 为某 host 攒够的一批流量建代理捕获 task：
// 建 task → 领取该 host 未消费流量（traffic_task 关联）→ 按领到的流量定稿任务目标 → 绑会话 → enqueue。
//
// 领取跨实例幂等（见 traffic.ProxyStore.ClaimUnconsumedByHost）。领到 0 条说明流量已被
// 别的 task 消费或全部滞留锁定，回滚建的 task（abort）避免空任务。
func (t *Traffic) spawnProxyTask(ctx context.Context, host string) {
	// 一切下发皆走 assignment：聚合器建 assignment(source=proxy, [host]) → 1 task（fan-in）。
	asg, err := t.assignments.Create(ctx, assignment.NewParams{
		Source: assignment.SourceProxy,
		Items:  []assignment.Item{{Host: host}},
		Title:  host,
	})
	if err != nil {
		t.logger.Warn().Err(err).Str("host", host).Msg("建代理捕获 assignment 失败")
		return
	}
	// 目标要列出领到的流量，领取又以任务为归属：先以概括目标建任务，领取后定稿。
	tk, err := t.tasks.Create(ctx, task.NewParams{
		AssignmentID: asg.ID,
		Goal:         fmt.Sprintf("分析 %s 的代理流量", host),
	})
	if err != nil {
		t.logger.Warn().Err(err).Str("host", host).Msg("建代理捕获 task 失败")
		return
	}
	// 任务作用域派生：本任务后续日志自动携带 task_id/assignment_id，调用点不再手写
	taskLogger := t.logger.With().Str("task_id", tk.ID).Str("assignment_id", asg.ID).Logger()
	claimed, err := t.proxyStore.ClaimUnconsumedByHost(ctx, tk.ID, host, t.agg.batchN())
	if err != nil {
		taskLogger.Warn().Err(err).Msg("领取 proxy_traffic 失败，abort 本 task")
		_ = t.tasks.Abort(ctx, tk.ID, "领取流量失败")
		return
	}
	if claimed == 0 {
		_ = t.tasks.Abort(ctx, tk.ID, "无未消费流量可分析")
		return
	}

	// 取本批已认领流量的清单（供首条任务说明列出 method/path/status）；失败则降级为空清单
	// （首条说明仍显示条数，只是列不出明细），不阻塞分析。
	claimedTraffic, err := t.proxyStore.ListByTask(ctx, tk.ID)
	if err != nil {
		taskLogger.Warn().Err(err).Msg("取已认领流量清单失败（任务目标降级为无明细）")
	}
	goal := ProxyTaskGoal(host, claimedTraffic)
	if err := t.tasks.SetGoal(ctx, tk.ID, goal); err != nil {
		taskLogger.Warn().Err(err).Msg("定稿任务目标失败，abort 本 task")
		_ = t.tasks.Abort(ctx, tk.ID, "定稿任务目标失败")
		return
	}
	conversationID := t.ensureConversation(ctx, taskLogger, tk.ID, host, goal)
	conversationLogger := taskLogger.With().Str("conversation_id", conversationID).Logger()
	if err := t.enqueueProxyTask(ctx, tk.ID, conversationID); err != nil {
		conversationLogger.Warn().Err(err).Msg("代理捕获 task 入队失败")
		return
	}
	conversationLogger.Info().
		Str("host", host).Int64("traffic", claimed).
		Msg("代理捕获 task 已建并入 executor 队列")
}

// handleInternalSnap 处理沙盒自产流量：按 run 反查 task_id，落 agent_traffic，不 enqueue。
//
// source=internal 唯一来源：沙盒浏览器服务（internal/sandbox/browser/service.py）持 CDP 连接，把 chromium 的 Document/XHR/Fetch（含真实
// 认证凭证位置）→ POST /internal/v1/traffic/ingest → snap.Source="internal" + snap.AgentRunID。
// 反查 agent_run 表得 task_id 写 agent_traffic。不 enqueue：agent 自己挖的流量回头触发分析会自激震荡。
func (t *Traffic) handleInternalSnap(ctx context.Context, snap *proxy.TrafficSnapshot) {
	if snap.AgentRunID == "" {
		t.logger.Warn().Str("host", snap.Host).Str("path", proxy.LogPath(snap.URI)).
			Msg("internal 流量缺 agent_run_id，丢弃")
		return
	}
	run, err := t.agentRuns.GetByID(ctx, snap.AgentRunID)
	if err != nil {
		t.logger.Warn().Err(err).Str("agent_run_id", snap.AgentRunID).
			Msg("internal 流量反查 run 失败，丢弃")
		return
	}

	reqH, _ := json.Marshal(snap.RequestHeaders)
	respH, _ := json.Marshal(snap.ResponseHeaders)
	trafficID, err := t.agentStore.Append(ctx, traffic.AgentTraffic{
		TaskID:          run.TaskID,
		AgentRunID:      snap.AgentRunID,
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
		Str("task_id", run.TaskID).Str("agent_run_id", snap.AgentRunID).Int64("traffic_id", trafficID).
		Str("method", snap.Method).Str("path", proxy.LogPath(snap.URI)).
		Msg("internal 流量已入 agent_traffic（不触发代理流量分析）")
}

// ensureConversation 为代理捕获 task 建会话流（title=host）并回填 conversation.task_id，
// 再把任务目标写成会话首条消息（role=user）——用户一眼看清在分析哪些流量。
// conversations nil / 建会话失败 → 返空串（降级：本次不绑，事件不落会话，但流量分析照常）。
func (t *Traffic) ensureConversation(ctx context.Context, taskLogger zerolog.Logger, taskID, host, goal string) string {
	if t.conversations == nil {
		return ""
	}
	created, err := t.conversations.CreateConversation(ctx, host, taskID)
	if err != nil {
		taskLogger.Warn().Err(err).Str("host", host).Msg("建代理捕获 task 会话流失败（降级：本次不绑会话）")
		return ""
	}
	// best-effort——写失败仅缺首条气泡，不影响分析与事件落会话。
	if _, err := t.conversations.AppendMessage(ctx, created.ID, conversation.RoleUser, conversation.KindMessage, goal, nil); err != nil {
		taskLogger.Warn().Err(err).Str("conversation_id", created.ID).Msg("写代理捕获任务说明首条消息失败（降级：会话缺首条气泡）")
	}
	return created.ID
}

// proxyTaskGoalMaxList 是目标里最多逐条列出的请求数；超出只显示"等 N 条"，避免超长批刷屏。
const proxyTaskGoalMaxList = 20

// ProxyTaskGoal 合成代理捕获任务的目标（一份文本两用：智能体的任务目标 + 会话首条气泡）。
// 设计要点：
//   - 首行必须含 scheme://host 形式的目标 URL——目标主机（task.target_host）由它派生；
//     scheme 取领到的第一条流量，没有则按 http；
//   - 每条流量带 view_traffic 可直接寻址的自描述 id（proxy:<id>）——agent 无需先
//     list_traffic 就能按 id 拉取任意一条的完整报文；
//   - 同 host 同 path 的重复请求在目录里无法互辨，id 是唯一可靠的对应键。
func ProxyTaskGoal(host string, trafficList []traffic.ProxyTraffic) string {
	scheme := "http"
	if len(trafficList) > 0 && trafficList[0].Scheme != "" {
		scheme = trafficList[0].Scheme
	}
	var b strings.Builder
	fmt.Fprintf(&b, "代理流量分析：代理已捕获 %s://%s 的 %d 条 HTTP 流量并已认领至本任务，发起分析——从请求/响应线索反推可控点，追查漏洞。\n\n每条流量按「id method path → status」列出，用 view_traffic（id=…）可拉取完整请求与响应原文，也可用 list_traffic（source=proxy）过滤浏览。\n\n捕获请求：", scheme, host, len(trafficList))
	for i, f := range trafficList {
		if i >= proxyTaskGoalMaxList {
			fmt.Fprintf(&b, "\n… 等共 %d 条", len(trafficList))
			break
		}
		fmt.Fprintf(&b, "\nproxy:%d %s %s → %d", f.ID, f.Method, f.Path, f.StatusCode)
	}
	return b.String()
}

func (t *Traffic) enqueueProxyTask(ctx context.Context, taskID, conversationID string) error {
	hid, err := t.agentRuns.Create(ctx, agentrun.NewParams{
		TaskID: taskID,
		Role:   constants.AgentCodePlanner,
	})
	if err != nil {
		return fmt.Errorf("agentRuns.Create: %w", err)
	}
	if _, _, err := t.enq.Enqueue(ctx, worker.RoleAgent, worker.Payload{
		AgentRunID:     hid,
		TaskID:         taskID,
		ConversationID: conversationID,
		TaskRole:       worker.RoleAgent,
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
