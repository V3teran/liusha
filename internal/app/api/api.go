// Package api 是 api 组件：HTTP API 与控制台前端、鉴权、LLM 接入首装引导、定时调度器。
//
// 启动顺序：pg + redis → stores → 首装引导（技能转换、LLM 种子、工具目录、初始管理员）→
// 调度器 → http server。ctx 取消后在 shutdown_timeout 内优雅关闭 http server。
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/attachment"
	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/auth"
	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/config/seed"
	"github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/convstream"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/cronschedule"
	"github.com/V3teran/liusha/internal/cryptx"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/dispatch"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	fwllm "github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/llmconfig"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/llmprobe"
	"github.com/V3teran/liusha/internal/llmstore"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/settingstore"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/tools/manifest"
	"github.com/V3teran/liusha/internal/toolstore"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/worker"
	"github.com/V3teran/liusha/internal/workspace"
	"github.com/V3teran/liusha/web"
)

// Run 运行 api 组件直到 ctx 取消。启动失败返回错误。
func Run(ctx context.Context, cfg config.AppConfig) error {
	logger := logx.New("api")

	pool, err := db.NewPgPool(ctx, cfg.Postgres, logger)
	if err != nil {
		return fmt.Errorf("连接 PostgreSQL: %w", err)
	}
	defer pool.Close()

	rdb, err := db.NewRedis(ctx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("连接 Redis: %w", err)
	}
	defer func() { _ = rdb.Close() }()

	// LLM provider API Key 加密密钥：缺失或长度不对直接拒启动——前端「LLM 配置」页写密钥、
	// LLM 调用读密钥都靠它，静默跳过会让密钥以明文落库或运行期解密报错。
	llmKeyCipher, err := cryptx.NewFromEnv("LIUSHA_LLM_KEY_SECRET")
	if err != nil {
		return fmt.Errorf("LIUSHA_LLM_KEY_SECRET 未配置或不合法（provider 密钥加密需要它）: %w", err)
	}

	credAPI := credential.NewRedis(rdb, cfg.Credential.RedisKeyPrefix)
	taskStore := task.NewStore(pool)
	assignmentStore := assignment.NewStore(pool)
	cronStore := cronschedule.NewStore(pool)
	findStore := finding.NewStore(pool)
	proxyTrafficStore := traffic.NewProxyStore(pool)
	invocationStore := llminvocation.NewStoreWithConfig(pool, cfg.LLM.Invocation)
	defer func() { _ = invocationStore.Close() }()
	controlPlaneStore := controlplane.NewStore(pool)
	agentRunStore := agentrun.NewStore(pool)
	auditStore := audit.NewStore(pool)
	conversationStore := conversation.NewStore(pool)
	toolStore := toolinvocation.NewStore(pool)
	explorationGraphStore := explorationgraph.NewStore(pool)
	authStore := auth.NewStore(pool)

	enq := worker.NewClient(asynq.RedisClientOpt{Addr: cfg.Redis.Addr, Password: cfg.Redis.Password})
	defer func() { _ = enq.Close() }()

	// 共享多级缓存内核（L1 内存 + L2 redis + 跨进程失效总线）：所有配置资源（agent 与技能、
	// LLM、系统设置）复用同一实例，一条 Subscribe 循环覆盖全部资源。
	cache := cachestore.New(rdb, 0)
	go func() {
		if err := cache.Subscribe(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("cachestore 失效订阅退出——配置跨进程失效不可用")
		}
	}()

	tools := manifest.Default()
	// agent 与技能配置：出厂值编译在程序里，用户层经三级缓存读写；写后广播失效，
	// runner 被动清缓存，下个任务即用新配置。
	cfgStore := configstore.New(pool, cache, configstore.ToolCatalogFrom(tools))
	llmStore := llmstore.New(pool, cache)
	// 系统设置：出厂默认 = 代码 + 部署配置，用户层经三级缓存读写；写后广播失效。
	settingStore := settingstore.New(pool, cache, settingstore.DefaultsFrom(cfg))
	toolCatalog := toolstore.New(pool)

	bootstrap(ctx, logger, cfg, pool, cfgStore, llmStore, toolCatalog, tools)
	if err := ensureInitialAdmin(ctx, authStore, logger); err != nil {
		return err
	}

	// 会话 / 问答 / 意图分类的 LLM 路由：与 runner 同一埋点口径——单步看门狗（现读热配置
	// runtime.step_llm_timeout_seconds）+ llm_invocation 审计。
	stepLLMTimeout := time.Duration(cfg.Runner.StepLLMTimeoutSeconds) * time.Second
	llmRouter := fwllm.NewRouterWithOpts(llmStore.AsRouterStore(llmKeyCipher), cfg.LLM.Retry.Options())
	llmRouter.SetProviderWrapper(func(p fwllm.Provider) fwllm.Provider {
		return fwllm.InstrumentProvider(fwllm.WithDynamicCallTimeout(p,
			stepLLMTimeout,
			func(ctx context.Context) time.Duration {
				rt, err := settingStore.Runtime(ctx)
				if err != nil {
					return 0
				}
				return time.Duration(rt.StepLLMTimeoutSeconds) * time.Second
			}), llminvocation.UsageSink{Store: invocationStore})
	})

	// 任务工作区（文件 CRUD 面）：与沙盒执行命令的工作区是同一目录。
	workspaceStore, err := workspace.NewStore(cfg.Sandbox.WorkspaceDir)
	if err != nil {
		return fmt.Errorf("打开任务工作区 %s: %w", cfg.Sandbox.WorkspaceDir, err)
	}
	defer func() { _ = workspaceStore.Close() }()
	logger.Info().Str("root", cfg.Sandbox.WorkspaceDir).Msg("任务工作区已就绪")
	// 附件复用工作区的路径钳制与落盘（暂存与落位同一棵树，下发搬家用 rename）。
	attachmentStore := attachment.NewStore(workspaceStore)

	publisher := convstream.NewPublisher(rdb)
	dispatchService := &dispatch.Service{
		LLM:           llmReadiness{llmRouter},
		Assignments:   assignmentStore,
		Tasks:         taskStore,
		AgentRuns:     agentRunStore,
		Enq:           enq,
		Audit:         auditStore,
		Conversations: conversationStore,
		Schedules:     cronStore,
		Router:        &dispatch.RoleRouter{Router: llmRouter},
		Findings:      findStore,
		Publisher:     publisher,
		Control:       controlPlaneStore,
		Attachments:   attachmentStore,
		MaxRunTimeout: time.Duration(cfg.Runner.AgentRunTimeoutSeconds) * time.Second,
	}

	// 定时调度器：轮询到点模板 → 克隆 assignment → 展开 task。随 ctx 取消立即退出，无在途状态要收尾。
	scheduler := &dispatch.Scheduler{
		Schedules:     cronStore,
		Assignments:   assignmentStore,
		Tasks:         taskStore,
		AgentRuns:     agentRunStore,
		Enq:           enq,
		Dispatcher:    dispatchService,
		Conversations: conversationStore,
		Logger:        logger,
	}
	go scheduler.Run(ctx)

	srv := &http.Server{
		Addr: cfg.API.ListenAddr,
		Handler: httpapi.NewServer(httpapi.Deps{
			Auth:               authStore,
			AuthAudit:          auditStore,
			CookieSecure:       cfg.API.CookieSecure,
			Workspaces:         workspaceStore,
			Credentials:        credAPI,
			CredentialAudit:    auditStore,
			TaskStatus:         dispatch.TaskStatusAdapter{Tasks: taskStore},
			Findings:           findStore,
			Invocations:        invocationStore,
			Chat:               dispatchService,
			FollowUp:           dispatchService,
			Deleter:            dispatchService,
			Attachments:        attachmentStore,
			AttachmentContext:  attachmentContext{conversationStore, taskStore},
			AttachmentMaxBytes: cfg.Conversation.AttachmentMaxBytes,
			Renamer:            conversationStore,
			ConfigStore:        cfgStore,
			SkillStore:         cfgStore,
			ToolCatalog:        toolCatalog,
			SandboxInventory:   sandbox.NewClient(cfg.Sandbox.URL, cfg.Sandbox.Token),
			Models:             llmStore,
			KeyEncrypter:       llmKeyCipher,
			ProviderDiagnoser:  llmprobe.New(llmStore, llmKeyCipher),
			Settings:           settingStore,
			Traffic:            proxyTrafficStore,
			TrafficConv:        conversationStore,
			Conversations:      conversationStore,
			EventStream:        dispatch.EventStreamAdapter{Rdb: rdb},
			UsageTasks:         conversationStore,
			UsageLLM:           invocationStore,
			UsageTools:         toolStore,
			ExplorationGraph:   explorationGraphStore,
			Tasks:              taskStore,
			TaskDispatch:       dispatchService,
			Schedules:          cronStore,
			TaskControl:        dispatchService,
			Logs:               logx.NewReader(logx.Dir()),
			WebUI:              web.Assets(),
		}),
		ReadTimeout:  time.Duration(cfg.API.ReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(cfg.API.WriteTimeoutSeconds) * time.Second,
	}

	if cfg.Sandbox.Token == "" {
		logger.Warn().Msg("sandbox.token 未配置（LIUSHA_SANDBOX_TOKEN）：工具页无法查询沙盒安装状态")
	}
	if !web.Embedded {
		logger.Warn().Msg("本程序未编入控制台前端（构建时未带 webdist 标签），访问首页只有占位说明；开发时前端走 vite dev server")
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info().Str("addr", srv.Addr).Msg("api listening")
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("api 监听 %s: %w", srv.Addr, err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(cfg.API.ShutdownTimeoutSeconds)*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("api shutdown")
	}
	logger.Info().Msg("api stopped")
	return nil
}

// bootstrap 执行启动期的幂等引导。各步失败都不阻止启动（下次启动重试，或可在前端补齐），只记日志。
func bootstrap(ctx context.Context, logger zerolog.Logger, cfg config.AppConfig, pool *pgxpool.Pool,
	cfgStore *configstore.Store, llmStore *llmstore.Store, toolCatalog *toolstore.Store, tools *manifest.ToolManifest,
) {
	// 已下线、但用户自定义过的内置技能转为同 code 的自建技能，用户的修改不随下线丢失。
	if adopted, err := cfgStore.AdoptRetiredSkills(ctx); err != nil {
		logger.Warn().Err(err).Msg("转换已下线内置技能失败（下次启动重试）")
	} else if len(adopted) > 0 {
		logger.Info().Strs("skills", adopted).Msg("已下线的内置技能已转为自建技能（保留用户自定义）")
	}

	// LLM 配置种子：llm_provider 空表时才导入配置里声明的 providers；非空库整段跳过
	// （数据库是运行期事实源，界面删除的 provider 不会被配置复活）。
	llmConfigStore := llmconfig.NewStore(pool)
	if err := seed.ImportLLM(ctx, cfg, llmConfigStore); err != nil {
		logger.Warn().Err(err).Msg("LLM 配置种子导入失败（跳过，可在「LLM 配置」页手动补齐）")
	}
	// 种子直连数据库写，绕过缓存层：导入后清空 LLM 配置读缓存，避免旧缓存盖过新配置。
	if err := llmStore.InvalidateAll(ctx); err != nil {
		logger.Warn().Err(err).Msg("LLM 配置缓存清空失败（运行期首读可能短暂滞后）")
	}
	// 零 provider 不拒启：允许「先部署、后在前端配置」，下发边界会拒绝并给出指引。
	if provs, err := llmConfigStore.ListProviders(ctx, false); err != nil {
		logger.Warn().Err(err).Msg("读取 LLM provider 失败（任务下发前请检查 LLM 配置）")
	} else if len(provs) == 0 {
		logger.Warn().Msg("LLM 未配置：尚无任何 provider——服务可启动，任务下发会被拒绝；请在「LLM 配置」页接入 provider")
	}

	// 工具目录同步：内置函数工具 + 工具清单幂等同步进 tool 表，供工具页与智能体配置页选用。
	if up, pr, err := toolstore.Reconcile(ctx, toolCatalog, tools); err != nil {
		logger.Warn().Err(err).Msg("工具目录同步失败（工具页可能展示陈旧目录）")
	} else {
		logger.Info().Int("upserted", up).Int("pruned", pr).Msg("工具目录已同步")
	}
}

// ensureInitialAdmin 在账号表为空时用 LIUSHA_ADMIN_INITIAL_PASSWORD 建 admin（仅首启生效，
// 之后永久跳过，改密走前端）。凭据随进程启动即存在——不存在「服务已起、账号未建」的
// 公网抢占窗口，api 没有任何未认证的建号端点。
func ensureInitialAdmin(ctx context.Context, accounts *auth.Store, logger zerolog.Logger) error {
	n, err := accounts.CountAccounts(ctx)
	if err != nil {
		return fmt.Errorf("读取账号数: %w", err)
	}
	if n > 0 {
		return nil
	}
	pw := os.Getenv("LIUSHA_ADMIN_INITIAL_PASSWORD")
	if len(pw) < 8 || len(pw) > 72 {
		return errors.New("首次启动需要 LIUSHA_ADMIN_INITIAL_PASSWORD（8-72 字节）初始化 admin 账号；仅首启生效，之后改密走前端")
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return fmt.Errorf("初始密码哈希: %w", err)
	}
	if _, err := accounts.CreateAccountIfAbsent(ctx, "admin", hash); err != nil {
		return fmt.Errorf("建立初始管理员: %w", err)
	}
	logger.Info().Msg("初始管理员已建立（admin），建议登录后立即修改密码")
	return nil
}

// attachmentContext 组合 conversation / task 两个 store 满足 httpapi.AttachmentContext
// （会话 → 任务 → 批次解析链，供附件落位定 inbox 目录）。
type attachmentContext struct {
	Conversations *conversation.Store
	Tasks         *task.Store
}

func (a attachmentContext) ResolveTaskID(ctx context.Context, conversationID string) (string, error) {
	return a.Conversations.ResolveTaskID(ctx, conversationID)
}

func (a attachmentContext) GetSummary(ctx context.Context, id string) (task.Summary, error) {
	return a.Tasks.GetSummary(ctx, id)
}

// llmReadiness 把 LLM Router 适配为下发边界的就绪校验：simple 是全部档位的降级兜底——
// simple 可解析 ⟺ 任意档位可解析，一次调用即完备校验（含密钥来源存在性）。
type llmReadiness struct{ r *fwllm.Router }

func (a llmReadiness) Ready(ctx context.Context) error {
	_, err := a.r.For(ctx, fwllm.ComplexitySimple)
	return err
}
