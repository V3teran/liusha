// Package api 是 api 组件：HTTP API 与控制台前端、鉴权、定时调度器。
//
// 启动顺序：pg + redis → stores → 启动引导（技能转换、工具目录、初始管理员）→
// 调度器 → http server。ctx 取消后在 shutdown_timeout 内优雅关闭 http server。
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/attachment"
	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/auth"
	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/convstream"
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/cronschedule"
	"github.com/V3teran/liusha/internal/cryptx"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/dispatch"
	"github.com/V3teran/liusha/internal/embedding"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	fwllm "github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/llmconfig"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/llmprobe"
	"github.com/V3teran/liusha/internal/llmstore"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/overview"
	"github.com/V3teran/liusha/internal/proxystatus"
	"github.com/V3teran/liusha/internal/retrievalstatus"
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

	redisOpts, err := db.RedisOptions(cfg.Redis)
	if err != nil {
		return err
	}
	rdb, err := db.NewRedis(ctx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("连接 Redis: %w", err)
	}
	defer func() { _ = rdb.Close() }()

	// provider 密钥的加密密钥：缺失或长度不对直接拒启动——前端「模型配置」页写密钥、
	// LLM 调用读密钥都靠它，静默跳过会让密钥以明文落库或运行期解密报错。
	llmKeyCipher, err := cryptx.NewFromHex(cfg.LLM.EncryptionKey)
	if err != nil {
		return fmt.Errorf("llm.encryption_key（LIUSHA_LLM_ENCRYPTION_KEY）未配置或不合法: %w", err)
	}

	credAPI := credential.NewRedis(rdb)
	taskStore := task.NewStore(pool)
	assignmentStore := assignment.NewStore(pool)
	cronStore := cronschedule.NewStore(pool)
	findStore := finding.NewStore(pool)
	proxyTrafficStore := traffic.NewProxyStore(pool)
	invocationStore := llminvocation.NewStore(pool)
	defer func() { _ = invocationStore.Close() }()
	agentRunStore := agentrun.NewStore(pool)
	auditStore := audit.NewStore(pool)
	conversationStore := conversation.NewStore(pool)
	toolStore := toolinvocation.NewStore(pool)
	explorationGraphStore := explorationgraph.NewStore(pool)
	authStore := auth.NewStore(pool)

	enq := worker.NewClient(worker.RedisOpt(redisOpts))
	defer func() { _ = enq.Close() }()

	// 共享多级缓存内核（L1 内存 + L2 redis + 跨进程失效总线）：所有配置资源（agent 与技能、
	// LLM、系统设置）复用同一实例，一条 Subscribe 循环覆盖全部资源。
	schemaVersion, err := db.SchemaVersion(ctx, pool)
	if err != nil {
		return err
	}
	cache := cachestore.New(rdb, schemaVersion)
	go func() {
		if err := cache.Subscribe(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("cachestore 失效订阅退出——配置跨进程失效不可用")
		}
	}()

	tools := manifest.Default()
	// agent、技能与场景配置经三级缓存读写（agent 为出厂值叠加用户层，技能与场景为整表）；
	// 写后广播失效，runner 被动清缓存，下个任务即用新配置。
	cfgStore := configstore.New(pool, cache, configstore.ToolCatalogFrom(tools))
	llmStore := llmstore.New(pool, cache)
	// 系统设置：出厂默认 = 代码 + 部署配置，用户层经三级缓存读写；写后广播失效。
	settingStore := settingstore.New(pool, cache, settingstore.DefaultsFrom(cfg))
	toolCatalog := toolstore.New(pool)

	bootstrap(ctx, logger, pool, cfgStore, toolCatalog, tools)
	if err := ensureInitialAdmin(ctx, authStore, cfg.Admin.InitialPassword, logger); err != nil {
		return err
	}

	// 会话 / 问答 / 意图分类的 LLM 路由：与 runner 同一埋点口径——单步看门狗（现读热配置
	// runtime.step_llm_timeout_seconds）+ llm_invocation 审计。这些调用没有外层重认领循环，
	// 额度窗口内、名额排满时都不在调用内等待：立即交还，用户当场看到原因，而不是等到连接超时。
	stepLLMTimeout := time.Duration(cfg.Runner.StepLLMTimeoutSeconds) * time.Second
	llmRouter := fwllm.NewRouterWithOpts(llmStore.AsRouterStore(llmKeyCipher), cfg.LLM.Retry.Options(), cfg.LLM.AccountConcurrency)
	llmRouter.FailFast()
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
	workspaceStore, err := workspace.NewStore(cfg.WorkspaceDir())
	if err != nil {
		return fmt.Errorf("打开任务工作区 %s: %w", cfg.WorkspaceDir(), err)
	}
	defer func() { _ = workspaceStore.Close() }()
	logger.Info().Str("root", cfg.WorkspaceDir()).Msg("任务工作区已就绪")
	// 附件复用工作区的路径钳制与落盘（暂存与落位同一棵树，下发搬家用 rename）。
	attachmentStore := attachment.NewStore(workspaceStore)

	publisher := convstream.NewPublisher(rdb)
	dispatchService := &dispatch.Service{
		DB:            pool,
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
		Attachments:   attachmentStore,
		Scenarios:     cfgStore,
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

	kb := newCorpus(pool, llmStore, llmKeyCipher, rdb, logger)

	srv := &http.Server{
		Addr: cfg.API.ListenAddr,
		Handler: httpapi.NewServer(httpapi.Deps{
			Auth:                authStore,
			AuthAudit:           auditStore,
			CookieSecure:        cfg.API.CookieSecure,
			Workspaces:          workspaceStore,
			WorkspaceLabels:     taskStore,
			Credentials:         credAPI,
			CredentialAudit:     auditStore,
			TaskStatus:          dispatch.TaskStatusAdapter{Tasks: taskStore},
			Findings:            findStore,
			Invocations:         invocationStore,
			AuditReader:         auditStore,
			Chat:                dispatchService,
			FollowUp:            dispatchService,
			Deleter:             dispatchService,
			Attachments:         attachmentStore,
			AttachmentContext:   attachmentContext{conversationStore, taskStore},
			AttachmentMaxBytes:  cfg.Conversation.AttachmentMaxBytes,
			Renamer:             conversationStore,
			ConfigStore:         cfgStore,
			SkillStore:          cfgStore,
			ScenarioStore:       cfgStore,
			Corpus:              kb,
			ToolCatalog:         toolCatalog,
			SandboxInventory:    sandbox.NewClient(cfg.Sandbox.URL, cfg.Sandbox.Token),
			Models:              llmStore,
			KeyEncrypter:        llmKeyCipher,
			ProviderDiagnoser:   llmprobe.New(llmStore, llmKeyCipher),
			RetrievalDiagnoser:  retrievalDiagnoser{models: llmStore, cipher: llmKeyCipher},
			RetrievalModelIndex: retrievalIndex{kb: kb, logger: logger},
			Settings:            settingStore,
			Traffic:             proxyTrafficStore,
			Overview:            overview.NewStore(pool),
			ProxyTraffic:        proxyTrafficStore,
			Runners:             enq,
			DataStores:          db.DataStores{Pool: pool, Redis: rdb},
			Conversations:       conversationStore,
			EventStream:         dispatch.EventStreamAdapter{Rdb: rdb},
			UsageLLM:            invocationStore,
			UsageTools:          toolStore,
			ExplorationGraph:    explorationGraphStore,
			TaskReader:          taskStore,
			Tasks:               taskStore,
			TaskDispatch:        dispatchService,
			Schedules:           cronStore,
			TaskControl:         dispatchService,
			TaskSteering:        dispatchService,
			LiveRuns:            agentRunStore,
			Logs:                logx.NewReader(cfg.LogDir()),
			Proxy:               proxystatus.New(rdb),
			WebUI:               web.Assets(),
		}),
		ReadTimeout:       time.Duration(cfg.API.ReadTimeoutSeconds) * time.Second,
		WriteTimeout:      time.Duration(cfg.API.WriteTimeoutSeconds) * time.Second,
		ReadHeaderTimeout: time.Duration(cfg.API.ReadHeaderTimeoutSeconds) * time.Second,
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
func bootstrap(ctx context.Context, logger zerolog.Logger, pool *pgxpool.Pool,
	cfgStore *configstore.Store, toolCatalog *toolstore.Store, tools *manifest.ToolManifest,
) {
	// 零 provider 不拒启：LLM 在前端「模型配置」页接入，未接入时下发边界会拒绝并给出指引。
	if provs, err := llmconfig.NewStore(pool).ListProviders(ctx, false); err != nil {
		logger.Warn().Err(err).Msg("读取 LLM provider 失败（任务下发前请检查模型配置）")
	} else if len(provs) == 0 {
		logger.Warn().Msg("LLM 未配置：尚无任何 provider——服务可启动，任务下发会被拒绝；请在「模型配置」页接入 provider")
	}

	// 工具目录同步：内置函数工具 + 工具清单幂等同步进 tool 表，供工具页与智能体配置页选用。
	if up, pr, err := toolstore.Reconcile(ctx, toolCatalog, tools); err != nil {
		logger.Warn().Err(err).Msg("工具目录同步失败（工具页可能展示陈旧目录）")
	} else {
		logger.Info().Int("upserted", up).Int("pruned", pr).Msg("工具目录已同步")
	}
}

// ensureInitialAdmin 在账号表为空时用 admin.initial_password 建 admin（仅首启生效，
// 之后永久跳过，改密走前端）。凭据随进程启动即存在——不存在「服务已起、账号未建」的
// 公网抢占窗口，api 没有任何未认证的建号端点。
func ensureInitialAdmin(ctx context.Context, accounts *auth.Store, pw string, logger zerolog.Logger) error {
	n, err := accounts.CountAccounts(ctx)
	if err != nil {
		return fmt.Errorf("读取账号数: %w", err)
	}
	if n > 0 {
		return nil
	}
	if len(pw) < 8 || len(pw) > 72 {
		return errors.New("首次启动需要 admin.initial_password（LIUSHA_ADMIN_INITIAL_PASSWORD，8-72 字节）初始化 admin 账号；仅首启生效，之后改密走前端")
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

func (a attachmentContext) GetConversation(ctx context.Context, id string) (conversation.Conversation, error) {
	return a.Conversations.GetConversation(ctx, id)
}

func (a attachmentContext) GetByID(ctx context.Context, id string) (task.Task, error) {
	return a.Tasks.GetByID(ctx, id)
}

// llmReadiness 把 LLM Router 适配为下发边界的就绪校验：simple 是全部档位的降级兜底——
// simple 可解析 ⟺ 任意档位可解析，一次调用即完备校验（含密钥来源存在性）。
type llmReadiness struct{ r *fwllm.Router }

func (a llmReadiness) Ready(ctx context.Context) error {
	_, err := a.r.For(ctx, fwllm.ComplexitySimple)
	return err
}

// newCorpus 装配知识库服务（页面的录入、修改要生成向量，检索测试要用重排）。
// 检索模型密钥是运行期可变的（模型配置页热改、跨进程失效），来源每次操作现取：
// 未配置退字面检索、不阻塞；配置后下一次操作即语义检索。
func newCorpus(pool *pgxpool.Pool, models *llmstore.Store, cipher *cryptx.Cipher, rdb *redis.Client, logger zerolog.Logger) *corpus.Service {
	return corpus.NewService(corpus.NewStore(pool), models.RetrievalModels(cipher),
		retrievalstatus.NewSink(rdb, logger), logger)
}

// retrievalDiagnoser 实现 httpapi.RetrievalDiagnoser：一次向量 + 一次重排的实测。
// 规格的空字段按已存值或出厂默认解析（与运行期同一套解析）。上游任何失败都进 Steps
// （不返回 error）——向量一步含维度校验，换模型的人靠它确认兼容；只有密钥拿不到
// （未配置 / 解不开）无法发起才返回 error。
type retrievalDiagnoser struct {
	models *llmstore.Store
	cipher *cryptx.Cipher
}

func (d retrievalDiagnoser) DiagnoseRetrieval(ctx context.Context, spec httpapi.RetrievalProbeSpec) (httpapi.RetrievalDiagnosis, error) {
	apiKey := spec.APIKey
	if apiKey == "" {
		row, err := d.models.RetrievalModel(ctx)
		if err != nil {
			return httpapi.RetrievalDiagnosis{}, err
		}
		if !row.HasKey() {
			return httpapi.RetrievalDiagnosis{}, errors.New("检索模型密钥未配置，请先填写或保存")
		}
		if apiKey, err = d.cipher.Decrypt(row.EncryptedAPIKey); err != nil {
			return httpapi.RetrievalDiagnosis{}, errors.New("检索模型密钥无法解密，请重新填写")
		}
	}
	client, err := embedding.NewClient(embedding.Config{
		APIKey: apiKey, BaseURL: spec.BaseURL,
		EmbeddingModel: spec.EmbeddingModel, RerankModel: spec.RerankModel,
	})
	if err != nil {
		return httpapi.RetrievalDiagnosis{}, err
	}

	var diag httpapi.RetrievalDiagnosis
	step := func(key, request string, fn func(ctx context.Context) error) {
		start := time.Now()
		err := fn(ctx)
		st := httpapi.StepOK
		detail := "成功"
		if err != nil {
			st, detail = httpapi.StepFail, err.Error()
		}
		diag.Steps = append(diag.Steps, httpapi.DiagnosisStep{
			Key: key, Status: st, Detail: detail, Request: request,
			LatencyMS: time.Since(start).Milliseconds(),
		})
	}
	step(httpapi.StepEmbedding, "POST "+client.EmbedURL(), func(ctx context.Context) error {
		_, err := client.EmbedQuery(ctx, "连接诊断")
		return err
	})
	step(httpapi.StepRerank, "POST "+client.RerankURL(), func(ctx context.Context) error {
		_, err := client.Rerank(ctx, "连接诊断", []string{"连接诊断文档"}, 1)
		return err
	})
	diag.OK = diag.Steps[0].Status == httpapi.StepOK && diag.Steps[1].Status == httpapi.StepOK
	return diag, nil
}

// retrievalIndex 实现 httpapi.RetrievalModelIndex：保存检索模型后的向量收口。
// ResetVectors 在向量模型更换时作废全部旧向量（不同模型的向量不可混在一个空间）；
// KickVectorBackfill 随即异步补齐（即时生效，不必等 runner 的周期巡检）。补齐与 runner
// 共用同一份 IndexPending，知识库写锁保证两个进程不重复 embed 同一批条目。补齐失败不
// 重试——runner 周期巡检兜底（≤10 分钟）。
type retrievalIndex struct {
	kb     *corpus.Service
	logger zerolog.Logger
}

func (i retrievalIndex) ResetVectors(ctx context.Context) (int64, error) {
	return i.kb.ResetVectors(ctx)
}

func (i retrievalIndex) KickVectorBackfill() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		err := i.kb.Exclusive(ctx, func(ctx context.Context) error {
			_, err := i.kb.IndexPending(ctx)
			return err
		})
		if err != nil && ctx.Err() == nil {
			i.logger.Warn().Err(err).Msg("检索模型已保存，向量补齐未完成（runner 周期巡检将兜底）")
			return
		}
		i.logger.Info().Msg("检索模型已保存，无向量条目已补齐")
	}()
}
