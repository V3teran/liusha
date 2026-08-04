// Package main 是 liusha api 进程入口：装配 config / pg / redis / stores / httpapi。
//
// 启动顺序：logger → config（含 ENV 覆盖）→ pg+redis → stores → http server → 监听 SIGINT/SIGTERM。
// 关闭顺序：收到信号后用 5s 超时 ctx 调 srv.Shutdown，再让 defer 关 pool/redis。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/attackgraph"
	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/chat"
	"github.com/V3teran/liusha/internal/config"
	cfghunter "github.com/V3teran/liusha/internal/config/hunter"
	cfgplaybook "github.com/V3teran/liusha/internal/config/playbook"
	cfgscenario "github.com/V3teran/liusha/internal/config/scenario"
	"github.com/V3teran/liusha/internal/config/seed"
	"github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/cronschedule"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/envx"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/hunterrun"
	"github.com/V3teran/liusha/internal/intent"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/qa"
	"github.com/V3teran/liusha/internal/scanstream"
	"github.com/V3teran/liusha/internal/sitemap"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/worker"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// auditLog 专记审计写入的 best-effort 失败——审计是安全/合规轨迹，
// 失败不阻塞业务但必须留可见日志（不能完全静默吞掉）。
var auditLog = logx.New("api.audit")

// chatLog 记纯聊天回答的 best-effort 失败——会话与用户消息已落库，回答缺失不阻断会话创建。
var chatLog = logx.New("api.chat")

func main() {
	logger := logx.New("api")
	ctx := context.Background()

	cfg, err := config.Load(envx.OrDefault("LIUSHA_CONFIG", "./config/config.yaml"))
	if err != nil {
		logger.Fatal().Err(err).Msg("load config")
	}

	pool, err := db.NewPgPool(ctx, os.Getenv("LIUSHA_POSTGRES_DSN"),
		cfg.Postgres.MaxConns, cfg.Postgres.MinConns,
		cfg.Postgres.ConnectTimeoutSeconds, cfg.Postgres.MaxConnLifetimeSeconds)
	if err != nil {
		logger.Fatal().Err(err).Msg("pg")
	}
	defer pool.Close()

	rdb, err := db.NewRedis(ctx, os.Getenv("LIUSHA_REDIS_ADDR"), cfg.Redis)
	if err != nil {
		logger.Fatal().Err(err).Msg("redis")
	}
	defer func() { _ = rdb.Close() }()

	credAPI := credential.NewRedis(rdb, cfg.Credential.RedisKeyPrefix)
	taskStore := task.NewStore(pool)
	assignmentStore := assignment.NewStore(pool)
	cronStore := cronschedule.NewStore(pool) // 定时模板（§3.3/§4.2），Scheduler goroutine 轮询
	findStore := finding.NewStore(pool)
	agentFlowStore := traffic.NewAgentStore(pool) // sitemap 攻击面从 agent_traffic 派生
	proxyFlowStore := traffic.NewProxyStore(pool) // cron 定时触发 passive 展开时领取该 host 未消费流量
	projector := &sitemap.Projector{
		Findings: findStore,
		Flows:    agentFlowStore, // 攻击面从 agent_traffic 派生（按 task）
	}
	invocationStore := llminvocation.NewStoreWithConfig(pool, cfg.LLM.Invocation)
	defer func() { _ = invocationStore.Close() }()

	// hunter run store + asynq 入队器。
	hunterStore := hunterrun.NewStore(pool)
	enq := worker.NewClient(asynq.RedisClientOpt{Addr: os.Getenv("LIUSHA_REDIS_ADDR")})
	defer enq.Close()

	// 配置三级缓存 Store（scenario/playbook/hunter CRUD 后端）。写路径经 redis 总线广播失效，
	// runner 进程被动失效其 L1。Subscribe 阻塞运行（内部 for-select 直到 ctx 取消），必须后台起——
	// 同步调用会把 main goroutine 卡死在订阅循环。
	cfgStore := configstore.New(pool, rdb)
	go func() {
		if err := cfgStore.Subscribe(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("configstore 失效订阅退出——配置跨进程失效不可用")
		}
	}()

	// 种子首填（insert-only）：空库时从磁盘 scenarios/playbooks/hunters 导入默认配置，
	// 已存在的行按 code 整行跳过（DB 是事实源，不覆盖运维/前端改动）。
	// 目录缺失时静默跳过（walkFiles 容忍不存在），非致命——失败仅告警不 fail-fast，
	// 让 api 仍能起（配置可事后经 CRUD 补齐）。
	seedDir := envx.OrDefault("LIUSHA_SEED_DIR", ".")
	if err := seed.Import(ctx, seedDir,
		cfghunter.NewStore(pool), cfgplaybook.NewStore(pool), cfgscenario.NewStore(pool)); err != nil {
		logger.Warn().Err(err).Str("dir", seedDir).Msg("配置种子导入失败（跳过，可经 CRUD 手动补齐）")
	}
	auditStore := audit.NewStore(pool)         // 0047：task abort / create 审计
	convStore := conversation.NewStore(pool)   // 阶段B：会话/消息
	toolStore := toolinvocation.NewStore(pool) // 会话用量合计：工具耗时来源
	// 执行图（思维链+成果链）read-model 投影：复用 conv/finding store，不落表（docs/attack-graph-design.md）。
	attackGraphProjector := &attackgraph.Projector{Messages: convStore, Findings: findStore, Conv: convStore}
	// 多轮问答/意图分类依赖：light provider 路由 + 问答读 finding + SSE publish。
	router := llm.NewRouterWithOptions(llm.NewFactory(cfg), llm.RetryOptionsFromConfig(cfg.LLM.Retry))
	// 执行图里程碑摘要：用 light LLM 把子代理推理总结成一句（派生层，按需调用）。
	attackGraphProjector.Summary = llmSummarizer{router: router}
	publisher := scanstream.NewPublisher(rdb)
	adapter := &scanAdapter{assignments: assignmentStore, tasks: taskStore, hunters: hunterStore, enq: enq, audit: auditStore, conversations: convStore, router: router, findings: findStore, publisher: publisher, maxRunTimeout: time.Duration(cfg.Runner.SwarmAgentRunTimeoutSeconds) * time.Second}

	// cron Scheduler（§4.2/§10 P4）：轮询 cron_schedule 到点模板 → 克隆 assignment → 展开 task。
	// 单副本够用；ctx 随进程关停取消（无需独立 shutdown 时限——轮询循环立即退出，无 in-flight 状态要收尾）。
	cronCtx, cronCancel := context.WithCancel(context.Background())
	defer cronCancel()
	runner := &cronRunner{
		schedules:   cronStore,
		assignments: assignmentStore,
		tasks:       taskStore,
		proxyFlows:  proxyFlowStore,
		hunters:     hunterStore,
		enq:         enq,
		scan:        adapter,
		logger:      logger,
	}
	go runner.run(cronCtx)

	// SSE stream cookie 密钥：会话功能开启时必填（EventSource 鉴权用），缺失 fail-fast。
	streamSecret := []byte(os.Getenv("LIUSHA_STREAM_COOKIE_SECRET"))
	if len(streamSecret) == 0 {
		logger.Fatal().Msg("LIUSHA_STREAM_COOKIE_SECRET 未配置——SSE stream cookie 鉴权需要它（fail-fast）")
	}

	// 监听地址：优先 ENV（运维临时切换）→ yaml。
	listenAddr := envx.OrDefault("LIUSHA_API_ADDR", cfg.API.ListenAddr)
	srv := &http.Server{
		Addr: listenAddr,
		Handler: httpapi.NewServer(httpapi.Deps{
			APIKey:             os.Getenv("LIUSHA_API_KEY"),
			StreamCookieSecret: streamSecret,
			CookieSecure:       os.Getenv("LIUSHA_COOKIE_SECURE") == "true",
			Credentials:        credAPI,
			Tasks: taskAPIAdapter{
				tasks: taskStore,
				audit: auditStore,
			},
			Sitemap:           projector,
			Findings:          findStore,            // 全局漏洞台账（active+passive 全量 + triage 处置）
			AttackGraph:       attackGraphProjector, // 执行图（思维链+成果链）投影
			Invocations:       invocationStore,
			Scan:              adapter,
			Chat:              adapter,                      // 阶段B：POST /chat 会话发起扫描
			FollowUp:          adapter,                      // 多轮：POST /conversations/:id/messages 动作续接
			Abort:             adapter,                      // 多轮：POST /conversations/:id/abort 停止会话关联扫描
			Deleter:           adapter,                      // DELETE /conversations/:id 删会话+消息；关联扫描进行中拒删（409，先停后删）
			Renamer:           convStore,                    // PATCH /conversations/:id 重命名标题（convStore.SetTitle 直接满足）
			ConfigStore:       cfgStore,                     // scenario/playbook/hunter 配置 CRUD（配置管理页 + 对话 ScenarioPicker）
			Conversations:     convStore,                    // 阶段B：会话列表 / 消息回看
			EventStream:       eventStreamAdapter{rdb: rdb}, // 阶段B：SSE 订阅 redis 事件
			UsageTasks:        convStore,                    // 会话用量：会话→task 解析
			UsageLLM:          invocationStore,              // 会话用量：LLM token/耗时合计
			UsageTools:        toolStore,                    // 会话用量：工具耗时合计
			EnableDevAutofill: envx.OrDefault("LIUSHA_DEV_AUTOFILL", "") != "",
		}),
		ReadTimeout:  time.Duration(cfg.API.ReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(cfg.API.WriteTimeoutSeconds) * time.Second,
	}

	go func() {
		logger.Info().Str("addr", srv.Addr).Msg("api listening")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("api serve")
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	sig := <-stop
	logger.Info().Str("signal", sig.String()).Msg("api shutting down")

	shutdownTimeout := time.Duration(cfg.API.ShutdownTimeoutSeconds) * time.Second
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("api shutdown")
	}
	logger.Info().Msg("api stopped")
}

// taskAPIAdapter 把 task store 适配到 httpapi.TaskAPI 窄接口。
//
// Abort/List 直接走 task store，无双表试探。
// HTTP API 不暴露 errMsg：用户主动取消 task 即视为正常结束，abort 恒传 ""。
type taskAPIAdapter struct {
	tasks *task.Store
	audit *audit.Store // 0047：abort 写审计事件；nil 时跳过
}

// Abort 把 task 置为 aborted。errMsg 恒空（用户主动 abort 视为正常结束）。
// 0047：成功 abort 后写 audit_log（actor=api_user，target_kind=task）。
func (a taskAPIAdapter) Abort(ctx context.Context, id string) error {
	if _, err := a.tasks.GetByID(ctx, id); err != nil {
		return fmt.Errorf("task %s not found: %w", id, err)
	}
	if err := a.tasks.Abort(ctx, id, ""); err != nil {
		return err
	}
	a.writeAudit(ctx, audit.ActionTaskAbort, "task", id)
	return nil
}

// writeAudit best-effort 写审计事件；失败不阻塞业务，但记 Warn 留可见痕迹。
func (a taskAPIAdapter) writeAudit(ctx context.Context, action, kind, id string) {
	if a.audit == nil {
		return
	}
	if _, err := a.audit.Append(ctx, audit.Event{
		Actor:      audit.ActorAPIUser,
		Action:     action,
		TargetKind: kind,
		TargetID:   id,
	}); err != nil {
		auditLog.Warn().Err(err).Str("action", action).Str("target", id).Msg("审计事件写入失败（不阻塞业务）")
	}
}

// List 列出最近的 task → httpapi.TaskSummary（前端下拉/列表）。各场景混列，按 created_at desc。
func (a taskAPIAdapter) List(ctx context.Context, limit int) ([]httpapi.TaskSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	tasks, err := a.tasks.List(ctx, "", limit)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	out := make([]httpapi.TaskSummary, 0, len(tasks))
	for _, t := range tasks {
		// 输入已统一（brief 主 + target_host 派生，见 D5）：始终输出两字段，不按场景分形状。
		scopeJSON, _ := json.Marshal(map[string]string{"brief": t.Brief, "target_host": t.TargetHost})
		s := httpapi.TaskSummary{
			ID:           t.ID,
			Scope:        string(scopeJSON),
			Status:       string(t.Status),
			ScenarioID:   t.ScenarioID,
			CreatedAt:    t.CreatedAt.Format(time.RFC3339),
			ErrorMessage: t.ErrorMessage,
		}
		if t.EndedAt != nil {
			s.EndedAt = t.EndedAt.Format(time.RFC3339)
		}
		out = append(out, s)
	}
	return out, nil
}

// scanAdapter 把 task store + hunterrun.Store + worker.Client 组合成
// httpapi.ScanAPI 一站式入口：建 scan → 建 hunter agent_run → 入 asynq 队列。
//
// 任一步失败都不留中间状态（前面失败直接返错；task 已建但 enqueue 失败会留
// scan，由用户手动 abort 或后续 sweeper——保持简单不上事务，与
// ingestor.enqueueMain 一致语义）。
// 合表后：task.Store 管扫描生命周期，hunterrun.Store 建 run。
type scanAdapter struct {
	assignments   *assignment.Store
	tasks         *task.Store
	hunters       *hunterrun.Store
	enq           *worker.Client
	audit         *audit.Store        // 0047：create 写审计事件；nil 跳过
	conversations *conversation.Store // 阶段B：StartChatScan 建会话；nil 时仅 CreateScan 可用

	router    *llm.Router           // 多轮：意图分类 + 问答（light provider）
	findings  *finding.Store        // 问答读 task 黑板 finding
	publisher *scanstream.Publisher // 问答回答 publish SSE

	// run 整体超时上限（取最长引擎 = runner.SwarmAgentRunTimeoutSeconds）。入队时设为 asynq.Timeout，
	// 否则 asynq 默认 30min 任务 deadline 会架空 runner handler 里 4h 的 WithTimeout——
	// run 跑到 30min 就被 ctx cancel（实测 swarm 扫描 30min 整 abort、orchestrator 没机会收尾）。
	maxRunTimeout time.Duration
}

// eventStreamAdapter 把 scanstream 订阅适配成 httpapi.EventStream（SSE handler 用）。
// scanstream.Subscription 自带 Events()/Close()，满足 httpapi.EventSubscription。
type eventStreamAdapter struct{ rdb *redis.Client }

func (e eventStreamAdapter) Subscribe(ctx context.Context, conversationID string) httpapi.EventSubscription {
	return scanstream.Subscribe(ctx, e.rdb, conversationID)
}

// createScan 是建 scan 的核心：建 assignment + task + hunter run + 入 asynq 队列（带
// conversationID）。CreateScan（无会话纯后台）与 StartChatScan（会话发起）共用。
// scenarioID 必填——标识场景 code，runner 据此解析引擎与 playbook（数据驱动派发）。
func (a *scanAdapter) createScan(ctx context.Context, brief, conversationID, scenarioID string) (string, string, error) {
	// 一切下发皆走 assignment（§3.1）：单发 = 单元素 assignment(manual) → 1 task。
	asg, err := a.assignments.Create(ctx, assignment.NewParams{
		ScenarioID: scenarioID,
		Source:     assignment.SourceManual,
		Items:      []assignment.Item{{Brief: brief}},
		Title:      briefTitle(brief),
	})
	if err != nil {
		return "", "", fmt.Errorf("create assignment: %w", err)
	}
	return a.expandItem(ctx, asg.ID, brief, conversationID, scenarioID)
}

// expandItem 把 assignment 下的一个 item（brief）展开成 task + hunter run + enqueue。
// 单发（createScan，建单元素 assignment 后展开 1 条）与批量/定时（cron Scheduler，建多元素
// assignment 后逐条展开）复用同一份展开逻辑，只是 assignment 的建法不同（§3.1 单发 vs 批量/cron）。
//
// target_host 留空——不在 API 层 parse brief，runner 入口从 brief 抽取后回填（派生列，见 D5）。
// 引擎（solo/swarm）与 playbook 由 runner 按 scenarioID 解析，API 不关心（职责下沉，数据驱动）。
func (a *scanAdapter) expandItem(ctx context.Context, assignmentID, brief, conversationID, scenarioID string) (string, string, error) {
	// payload 只装 brief 原文——目标 URL / host 由 runner 从 brief 自识别回填。
	payloadInput, err := json.Marshal(map[string]string{"brief": brief})
	if err != nil {
		return "", "", fmt.Errorf("marshal payload: %w", err)
	}
	tk, err := a.tasks.Create(ctx, task.NewParams{ScenarioID: scenarioID, AssignmentID: assignmentID, Brief: brief})
	if err != nil {
		return "", "", fmt.Errorf("create task: %w", err)
	}

	tid, err := a.hunters.Create(ctx, hunterrun.NewParams{
		TaskID: tk.ID,
		Role:   "orchestrator",
		Input:  payloadInput,
	})
	if err != nil {
		return "", "", fmt.Errorf("create hunter run: %w", err)
	}

	// orchestrator 跑 ~4h，asynq 默认 retry 25 次 → 4 天死循环；且 retry 接管时新 runner 进程
	// parentRegistries 是空的，PreDoneCheck 永放行，旧 PG exploitation 留 status=running 僵尸态。
	// MaxRetry(0)：跑挂就跑挂，让用户手动 abort + 重新触发，不重试。
	if _, _, err := a.enq.Enqueue(ctx, worker.RoleHunter, worker.Payload{
		HunterID:       tid,
		TaskID:         tk.ID,
		ConversationID: conversationID, // 阶段B：会话发起时非空 → runner 发过程事件
		ScenarioID:     scenarioID,     // 场景 code：runner 据此数据驱动派发引擎/playbook
		Input:          payloadInput,
		Role:           worker.RoleHunter,
	}, asynq.MaxRetry(0), asynq.Timeout(a.maxRunTimeout)); err != nil {
		return "", "", fmt.Errorf("enqueue: %w", err)
	}

	// 0047：task 创建成功 → 审计事件。metadata 记 brief 前 200 字便于事后查（完整 brief 在 task.brief 列）。
	if a.audit != nil {
		briefPreview := brief
		if len(briefPreview) > 200 {
			briefPreview = briefPreview[:200]
		}
		meta, _ := json.Marshal(map[string]string{"brief_preview": briefPreview, "hunter_id": tid})
		if _, err := a.audit.Append(ctx, audit.Event{
			Actor:      audit.ActorAPIUser,
			Action:     audit.ActionTaskCreate,
			TargetKind: "task",
			TargetID:   tk.ID,
			Metadata:   meta,
		}); err != nil {
			// best-effort：审计失败不阻塞业务返回，但记 Warn 留可见痕迹
			auditLog.Warn().Err(err).Str("action", string(audit.ActionTaskCreate)).Str("target", tk.ID).Msg("task 创建审计写入失败（不阻塞业务）")
		}
	}

	return tk.ID, tid, nil
}

// FollowUp 在已有 task 上发起一次续接 run（多轮动作）：重开 task + 建 hunter run + 入队
// （brief=追加消息）。复用 task 作用域黑板——新 run 经 BuildUserPrompt 看到先前 finding。
// 入队 Payload 与 createScan 同构，仅 TaskID 复用传入 taskID、不新建 task。
//
// 引擎由 runner 按 task 的 scenarioID 解析（数据驱动派发）——续接不区分 solo/swarm，
// 统一走 brief 追加，runner 侧按场景装配对应引擎。scenarioID 从原 task 读取，保证与首轮一致。
func (a *scanAdapter) FollowUp(ctx context.Context, taskID, conversationID, scenarioID, brief string) (string, error) {
	if err := a.tasks.Reopen(ctx, taskID); err != nil {
		return "", fmt.Errorf("reopen task: %w", err)
	}
	payloadInput, err := json.Marshal(map[string]string{"brief": brief})
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	tid, err := a.hunters.Create(ctx, hunterrun.NewParams{
		TaskID: taskID,
		Role:   "orchestrator",
		Input:  payloadInput,
	})
	if err != nil {
		return "", fmt.Errorf("create hunter run: %w", err)
	}
	if _, _, err := a.enq.Enqueue(ctx, worker.RoleHunter, worker.Payload{
		HunterID:       tid,
		TaskID:         taskID,
		ConversationID: conversationID,
		ScenarioID:     scenarioID,
		Input:          payloadInput,
		Role:           worker.RoleHunter,
	}, asynq.MaxRetry(0), asynq.Timeout(a.maxRunTimeout)); err != nil {
		return "", fmt.Errorf("enqueue followup: %w", err)
	}
	return tid, nil
}

// AbortConversationScan 满足 httpapi.AbortAPI：abort 会话关联的 task。
func (a *scanAdapter) AbortConversationScan(ctx context.Context, convID string) error {
	conv, err := a.conversations.GetConversation(ctx, convID)
	if err != nil {
		return err
	}
	if conv.TaskID == "" {
		return fmt.Errorf("conversation 无关联 task")
	}
	return a.tasks.Abort(ctx, conv.TaskID, "用户停止")
}

// DeleteConversation 满足 httpapi.ConversationDeleter：删会话+消息，但关联 task 仍在跑时拒删。
// 「先停后删」的服务端把关——返回 httpapi.ErrConversationScanActive → handler 映射 409。
// 防「删了会话、扫描脱缰后台跑、UI 再停不掉、还在烧 token」的孤儿（见 reference_deep_subagent_context 同源思路：
// 不变量在服务端守，不靠前端）。仅在确证 active 时拦截；无 task / 终态 / task 读不到则照常删。
func (a *scanAdapter) DeleteConversation(ctx context.Context, convID string) error {
	conv, err := a.conversations.GetConversation(ctx, convID)
	if err != nil {
		return err
	}
	if conv.TaskID != "" {
		if tk, gerr := a.tasks.GetByID(ctx, conv.TaskID); gerr == nil && tk.Status == task.StatusActive {
			return httpapi.ErrConversationScanActive
		}
	}
	return a.conversations.DeleteConversation(ctx, convID)
}

// HandleMessage 满足 httpapi.FollowUpAPI：落 user 消息 → 意图闸 → 分流。
//
// 两类会话共用一道意图闸（light LLM 判 action/qa）：
//   - 已绑 task 的会话：action → Reopen 同一 task 续接（沿用原场景，finding 累积）；
//     qa → qa.Answer 就已挖 finding 提问。
//   - 纯聊天会话（无 task）：action → 用当前 scenarioID 建 task 并关联（首次升级为扫描）；
//     qa/闲聊 → chat.Answer 通用助手回答（不读 finding、不下发 task）。
//
// scenarioID 由前端 ScenarioPicker 随 followup 带上（Composer 始终带场景选择），仅纯聊天会话
// 升级为 action 时用于建 task；已绑 task 的会话续接沿用原 task 场景，忽略本参数。
func (a *scanAdapter) HandleMessage(ctx context.Context, convID, scenarioID, content string) (string, bool, error) {
	conv, err := a.conversations.GetConversation(ctx, convID)
	if err != nil {
		return "", false, err
	}
	if _, err := a.conversations.AppendMessage(ctx, convID, conversation.RoleUser, conversation.KindMessage, content, nil); err != nil {
		return "", false, err
	}

	// 意图分流（action/qa）对纯聊天、active、passive 通用：判定走 light LLM。
	g, err := a.router.For(ctx, "inspector") // light provider
	if err != nil {
		return "", false, err
	}
	isAction := intent.Classify(ctx, g, content) == intent.IntentAction

	// 纯聊天会话（无 task）：升级为 action 时用当前场景建 task；否则通用助手回答。
	if conv.TaskID == "" {
		if !isAction {
			if err := chat.New(a).Answer(ctx, convID, content); err != nil {
				return "", false, err
			}
			return "qa", false, nil
		}
		if scenarioID == "" {
			return "", false, fmt.Errorf("升级为扫描需指定 scenario_id")
		}
		taskID, _, err := a.createScan(ctx, content, convID, scenarioID)
		if err != nil {
			return "", false, err
		}
		if err := a.conversations.LinkTask(ctx, convID, taskID); err != nil {
			return "", false, fmt.Errorf("link task: %w", err)
		}
		go a.genTitle(convID, content)
		return "action", false, nil
	}

	tk, err := a.tasks.GetByID(ctx, conv.TaskID)
	if err != nil {
		return "", false, err
	}
	if isAction {
		if tk.Status == task.StatusActive {
			return "action", true, nil // 忙：agent 在跑，本轮指导经 conversationContext 下次读到
		}
		// 续接沿用原 task 的场景（引擎由 runner 按 scenarioID 解析）：Reopen 同一 task，
		// finding 累积在这次分析会话里（不新建 task）。追加消息作为新一轮 brief 下发。
		if _, err := a.FollowUp(ctx, conv.TaskID, convID, tk.ScenarioID, content); err != nil {
			return "", false, err
		}
		return "action", false, nil
	}
	// qa：就已有 finding/流量提问，各场景同一套问答
	if err := qa.New(a).Answer(ctx, convID, conv.TaskID, content); err != nil {
		return "", false, err
	}
	return "qa", false, nil
}

// ---- qa.Deps 实现 ----

// FindingsSummary 满足 qa.Deps：把 task 黑板 finding 渲染成文本摘要。
func (a *scanAdapter) FindingsSummary(ctx context.Context, _, taskID string) (string, error) {
	fs, err := a.findings.ListByTask(ctx, taskID)
	if err != nil {
		return "", err
	}
	if len(fs) == 0 {
		return "（暂无 finding）", nil
	}
	var b strings.Builder
	for i, f := range fs {
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, f.Severity, f.Summary)
	}
	return b.String(), nil
}

// Generate 满足 qa.Deps：调 light provider。
func (a *scanAdapter) Generate(ctx context.Context, msgs []llm.Message, tools []llm.ToolSchema) (llm.Result, error) {
	g, err := a.router.For(ctx, "inspector")
	if err != nil {
		return llm.Result{}, err
	}
	return g.Generate(ctx, msgs, tools)
}

// AppendAssistant 满足 qa.Deps：落 assistant 消息，返回 SSE payload。
func (a *scanAdapter) AppendAssistant(ctx context.Context, convID, content string) ([]byte, error) {
	msg, err := a.conversations.AppendMessage(ctx, convID, conversation.RoleAssistant, conversation.KindMessage, content, nil)
	if err != nil {
		return nil, err
	}
	return json.Marshal(msg)
}

// Publish 满足 qa.Deps：推 SSE。
func (a *scanAdapter) Publish(ctx context.Context, convID string, payload []byte) error {
	return a.publisher.Publish(ctx, convID, payload)
}

// CreateScan 满足 httpapi.ScanAPI（无会话的纯后台扫描入口）。scenarioID 必填。
func (a *scanAdapter) CreateScan(ctx context.Context, brief, scenarioID string) (string, string, error) {
	return a.createScan(ctx, brief, "", scenarioID)
}

// StartChatScan 满足 httpapi.ChatAPI：建会话（记 scenario_id）+ 落用户首条消息，然后过意图闸
// （light LLM 判 action/qa）——action 才发起扫描（入队带 conversationID + scenarioID）并关联
// 会话与 scan；qa/闲聊则只作纯聊天回答，不下发 task（scanID 返回空）。返回 conversationID 供前端
// 订阅 SSE。scenarioID 必填——前端 ScenarioPicker 选定（handler 已校验非空），供 action 时建 task。
//
// 首次对话与追加消息（HandleMessage）走同一道意图闸：避免把闲聊/答疑误判成动作而白烧一次扫描。
func (a *scanAdapter) StartChatScan(ctx context.Context, brief, scenarioID string) (string, string, error) {
	conv, err := a.conversations.CreateConversation(ctx, briefTitle(brief), "")
	if err != nil {
		return "", "", fmt.Errorf("create conversation: %w", err)
	}
	if _, err := a.conversations.AppendMessage(ctx, conv.ID, conversation.RoleUser, conversation.KindMessage, brief, nil); err != nil {
		return "", "", fmt.Errorf("append user message: %w", err)
	}

	// 意图闸：light LLM 判 action/qa（解析失败默认 qa，见 intent.Classify）。
	g, err := a.router.For(ctx, "inspector") // light provider
	if err != nil {
		return "", "", fmt.Errorf("intent provider: %w", err)
	}
	if intent.Classify(ctx, g, brief) != intent.IntentAction {
		// 纯聊天：不下发 task，用通用助手回答（落 assistant 消息 + SSE，前端补历史即见）。
		// 失败不阻断会话创建——会话与用户消息已落库，回答缺失可由用户再发一句触发。
		if err := chat.New(a).Answer(ctx, conv.ID, brief); err != nil {
			chatLog.Warn().Err(err).Str("conv", conv.ID).Msg("纯聊天回答失败")
		}
		return conv.ID, "", nil
	}

	taskID, _, err := a.createScan(ctx, brief, conv.ID, scenarioID)
	if err != nil {
		return "", "", err
	}
	if err := a.conversations.LinkTask(ctx, conv.ID, taskID); err != nil {
		return "", "", fmt.Errorf("link task: %w", err)
	}
	// 异步生成智能标题（light LLM 把 brief 总结成短标题）——不阻塞会话创建响应；
	// 失败则保留 briefTitle 截断兜底。前端下次 refresh 列表即见新标题。
	go a.genTitle(conv.ID, brief)
	return conv.ID, taskID, nil
}

// genTitle 用 light LLM 把 brief 总结成 ≤16 字的简短标题，回填 conversation.title。
// 异步调用（独立 context，不随请求结束被 cancel）；失败静默（保留 briefTitle 兜底）。
func (a *scanAdapter) genTitle(convID, brief string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	g, err := a.router.For(ctx, "inspector") // light provider，便宜
	if err != nil {
		return
	}
	prompt := "把下面的渗透测试任务描述总结成一个不超过 16 字的简短中文标题，" +
		"突出目标和测试类型（如「DVWA SQL注入渗透」）。只输出标题本身，不要引号、不要解释。\n\n任务：" + brief
	res, err := g.Generate(ctx, []llm.Message{{Role: llm.RoleUser, Content: prompt}}, nil)
	if err != nil {
		return
	}
	title := strings.TrimSpace(strings.Trim(strings.TrimSpace(res.Content), `"'「」`))
	if r := []rune(title); len(r) > 24 { // 防 LLM 超长，硬截兜底
		title = string(r[:24])
	}
	if title == "" {
		return
	}
	_ = a.conversations.SetTitle(ctx, convID, title)
}

// briefTitle 取 brief 前 40 字（rune 安全，不截半个中文）作会话标题。
func briefTitle(brief string) string {
	const maxRunes = 40
	r := []rune(brief)
	if len(r) > maxRunes {
		return string(r[:maxRunes])
	}
	return brief
}
