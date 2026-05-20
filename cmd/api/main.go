// Package main 是 liusha api 进程入口：装配 config / pg / redis / stores / httpapi。
//
// 启动顺序：logger → config（含 ENV 覆盖）→ pg+redis → stores → http server → 监听 SIGINT/SIGTERM。
// 关闭顺序：收到信号后用 5s 超时 ctx 调 srv.Shutdown，再让 defer 关 pool/redis。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/V3teran/liusha/internal/activescan"
	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/envx"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/graphview"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/passivesession"
	"github.com/V3teran/liusha/internal/worker"
	"github.com/V3teran/liusha/web"

	"github.com/hibiken/asynq"
)

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
	passiveSessionStore := passivesession.NewStore(pool)
	activeScanStore := activescan.NewStore(pool)
	findStore := finding.NewStore(pool)
	projector := &graphview.Projector{Findings: findStore, Passive: passiveSessionStore, Active: activeScanStore}
	invocationStore := llminvocation.NewStoreWithConfig(pool, cfg.LLM.Invocation)
	defer func() { _ = invocationStore.Close() }()

	// Active 模式装配：agentrun store + asynq 入队器。
	// 计数 best-effort 维护到 active_scan.agent_run_count（cmd/api 只创建 active scans）。
	taskStore := agentrun.NewStore(pool).WithCounter(activeScanStore)
	enq := worker.NewClient(asynq.RedisClientOpt{Addr: os.Getenv("LIUSHA_REDIS_ADDR")})
	defer enq.Close()
	activeAdapter := &activeScanAdapter{activeScans: activeScanStore, tasks: taskStore, enq: enq}

	// 监听地址：优先 ENV（运维临时切换）→ yaml。
	listenAddr := envx.OrDefault("LIUSHA_API_ADDR", cfg.API.ListenAddr)
	srv := &http.Server{
		Addr: listenAddr,
		Handler: httpapi.NewServer(httpapi.Deps{
			APIKey:            os.Getenv("LIUSHA_API_KEY"),
			Credentials:       credAPI,
			Owners: ownerAPIAdapter{
				passive: passiveSessionStore,
				active:  activeScanStore,
			},
			Graph:             projector,
			Invocations:       invocationStore,
			AgentRuns:         taskStore, // viewer 拼父子树用（按 parent_id）
			ActiveScan:        activeAdapter,
			StaticFS:          web.ViewerFS(),
			EnableDevAutofill: envx.OrDefault("LIUSHA_VIEWER_DEV_KEY", "") != "",
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

// ownerAPIAdapter 把 owner store (passive_session/active_scan) 适配到 httpapi.OwnersAPI 窄接口。
//
// HTTP API 不暴露 errMsg：用户主动取消  owner 即视为正常结束，
// abort 调用恒传 ""；store 层完整签名（含 errMsg）保留给 scanner 内部用。
//
// 已剥离 owner store——passive_session 现在 per-host 由 ingestor 流量入口
// LookupOrCreate；EnsurePassiveSession 仅作"代理准备就绪"信号 stub。
type ownerAPIAdapter struct {
	passive *passivesession.Store // List 合并新表，Abort 试两表
	active  *activescan.Store
}

// Abort 双试：先 passive 表，否则 active 表；都没命中则报错。
// HTTP API 不区分 mode，用户只给 ID 不给 type，故试两表是必要的。
// errMsg 恒空（用户主动 abort 视为正常结束；store 完整签名留给 scanner 内部用）。
func (a ownerAPIAdapter) Abort(ctx context.Context, id string) error {
	if _, err := a.passive.GetByID(ctx, id); err == nil {
		return a.passive.Abort(ctx, id, "")
	}
	if _, err := a.active.GetByID(ctx, id); err == nil {
		return a.active.Abort(ctx, id, "")
	}
	return fmt.Errorf("session %s not found in passive_session or active_scan", id)
}

// EnsurePassiveSession 历史路径：mitmproxy 启动期预热 passive session 拿 owner_id。
// 新模型下 passive_session 按 host 由 ingestor 流量入口自创建——本 API 仅作"代理就绪"
// 信号返回空 ID（客户端可忽略此值）。保留 endpoint 兼容旧 client 不报错。
func (a ownerAPIAdapter) EnsurePassiveSession(ctx context.Context) (string, error) {
	return "", nil
}

// List 合并 passive_session + active_scan 两新表 → httpapi.OwnerSummary。
//
// 双轨期：旧 owner store 不再读，由新表数据直接返回。每个 sub-list 各取 limit 条，
// 合并后按 CreatedAt desc 排，最终截到 limit。Mode 字段标记来源表（"passive"/"active"），
// 前端 viewer 用 Mode 区分展示。
func (a ownerAPIAdapter) List(ctx context.Context, limit int) ([]httpapi.OwnerSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	// 各取 limit 条；后面合并截断保证 desc 排序正确（边界 case：单表都新过另一表的最旧 N）。
	passives, err := a.passive.List(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list passive sessions: %w", err)
	}
	actives, err := a.active.List(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list active scans: %w", err)
	}

	out := make([]httpapi.OwnerSummary, 0, len(passives)+len(actives))
	for _, p := range passives {
		scopeJSON, _ := json.Marshal(map[string]string{"host": p.Host})
		s := httpapi.OwnerSummary{
			ID:            p.ID,
			Scope:         string(scopeJSON),
			Status:        string(p.Status),
			Mode:          "passive",
			FlowCount:     p.FlowCount,
			FindingCount:  p.FindingCount,
			AgentRunCount: p.AgentRunCount,
			CreatedAt:     p.CreatedAt.Format(time.RFC3339),
			ExpiresAt:     p.ExpiresAt.Format(time.RFC3339),
			ErrorMessage:  p.ErrorMessage,
		}
		if p.EndedAt != nil {
			s.EndedAt = p.EndedAt.Format(time.RFC3339)
		}
		out = append(out, s)
	}
	for _, sc := range actives {
		scopeJSON, _ := json.Marshal(map[string]string{"brief": sc.Brief})
		s := httpapi.OwnerSummary{
			ID:            sc.ID,
			Scope:         string(scopeJSON),
			Status:        string(sc.Status),
			Mode:          "active",
			FindingCount:  sc.FindingCount,
			AgentRunCount: sc.AgentRunCount,
			CreatedAt:     sc.CreatedAt.Format(time.RFC3339),
			ErrorMessage:  sc.ErrorMessage,
		}
		if sc.EndedAt != nil {
			s.EndedAt = sc.EndedAt.Format(time.RFC3339)
		}
		out = append(out, s)
	}
	// 按 CreatedAt desc 排——比较字符串即可（RFC3339 lexicographic order = chronological order）。
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// activeScanAdapter 把 owner store + agentrun.Store + worker.Client 组合成
// httpapi.ActiveScanAPI 一站式入口：建 active scan → 建 hunter agent_run → 入 asynq 队列。
//
// 任一步失败都不留中间状态（前面失败直接返错；owner 已建但 enqueue 失败会留
// active scan，由用户手动 abort 或后续 sweeper——保持简单不上事务，与
// passive 模式 ingestor.enqueueMain 一致语义）。
// 单源：仅 active_scan 表（0040 DROP FK 后旧 owner 路径正式弃用）。
type activeScanAdapter struct {
	activeScans *activescan.Store
	tasks       *agentrun.Store
	enq         *worker.Client
}

func (a *activeScanAdapter) CreateActiveScan(ctx context.Context, brief string) (string, string, error) {
	// scope 与 entrypoint 都只装 brief 原文——目标 URL / host 由 hunter LLM
	// 从 brief 自然语言里自行识别（不在 API 层做 NL parser）。
	body, err := json.Marshal(map[string]string{"brief": brief})
	if err != nil {
		return "", "", fmt.Errorf("marshal brief: %w", err)
	}
	// 单源：仅写 active_scan 表（旧 owner 路径在 0040 FK DROP 后正式弃用）。
	// target_host 留空——暂不在 API 层 parse brief，hunter LLM 从 brief 自识别。
	sc, err := a.activeScans.Create(ctx, brief, "")
	if err != nil {
		return "", "", fmt.Errorf("create active_scan: %w", err)
	}
	payloadInput, err := json.Marshal(map[string]any{
		"mode":       "active",
		"entrypoint": json.RawMessage(body),
	})
	if err != nil {
		return "", "", fmt.Errorf("marshal payload: %w", err)
	}

	tid, err := a.tasks.Create(ctx, agentrun.NewParams{
		OwnerType: "active_scan",
		OwnerID:   sc.ID,
		Role:      string(worker.RoleHunter),
		Input:     payloadInput,
	})
	if err != nil {
		return "", "", fmt.Errorf("create agent_run: %w", err)
	}

	// active 父任务跑 ~4h，asynq 默认 retry 25 次 → 4 天死循环；且 retry 接管时
	// 新 scanner 进程 parentRegistries 是空的，PreDoneCheck 永放行，旧 PG 子留
	// status=running 僵尸态 + viewer 看到"父 done + 子 running"矛盾。
	// MaxRetry(0)：active 父跑挂就跑挂，让用户手动 abort + 重新触发，不重试。
	if _, _, err := a.enq.Enqueue(ctx, worker.RoleHunter, worker.Payload{
		TaskID:    tid,
		OwnerType: "active_scan",
		OwnerID:   sc.ID,
		Input:     payloadInput,
	}, asynq.MaxRetry(0)); err != nil {
		return "", "", fmt.Errorf("enqueue: %w", err)
	}
	return sc.ID, tid, nil
}
