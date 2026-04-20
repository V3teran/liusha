// agent-worker 启动 Asynq Server，消费 agent:run 任务，执行 ReAct 循环。
// Plan 5 骨架：Sink=LoggingSink（不写 graph_node），仅暴露 /healthz 活性探针。
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog/log"

	"github.com/V3teran/liusha/internal/agent/llm"
	"github.com/V3teran/liusha/internal/agent/runtime"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/worker"
)

func main() {
	cfgPath := os.Getenv("LIUSHA_CONFIG")
	if cfgPath == "" {
		cfgPath = "config/config.yaml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		panic("加载配置失败: " + err.Error())
	}
	logx.Init(cfg.LogLevel, cfg.Env == "development")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	gen, err := llm.NewDeepSeek(cfg.LLM.APIKey, cfg.LLM.BaseURL, cfg.LLM.DefaultModel, cfg.LLM.MaxTokens)
	if err != nil {
		log.Fatal().Err(err).Msg("初始化 Eino DeepSeek 失败")
	}

	sink := runtime.NewLoggingSink(log.Logger)
	reactRunner := &reactRunnerImpl{cfg: cfg, gen: gen, sink: sink}

	handler := worker.NewHandler(reactRunner)
	mux := asynq.NewServeMux()
	mux.HandleFunc(worker.TypeAgentRun, handler.HandleAgentRun)

	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: cfg.RedisAddr},
		asynq.Config{
			Queues:      map[string]int{"critical": 6, "default": 3},
			Concurrency: 5,
		},
	)

	httpSrv := startHealthzServer(":9090")
	defer func() {
		shutdownCtx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	log.Info().Str("redis", cfg.RedisAddr).Msg("agent-worker 启动")

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(mux) }()

	select {
	case <-ctx.Done():
		srv.Shutdown()
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Msg("Asynq server 异常退出")
		}
	}
}

type reactRunnerImpl struct {
	cfg  config.Config
	gen  llm.Generator
	sink runtime.Sink
}

func (r *reactRunnerImpl) Run(ctx context.Context, p worker.AgentRunPayload) error {
	return runtime.Run(ctx, runtime.RunParams{
		Payload:      p,
		SystemPrompt: fmt.Sprintf("你是 %s，专注于渗透测试中的漏洞检测与验证。当前为骨架阶段，工具尚未接入。", p.Role),
		MaxSteps:     r.cfg.LLM.MaxSteps,
		Gen:          r.gen,
		Sink:         r.sink,
	})
}

// startHealthzServer 起一个最小 HTTP server，暴露 /healthz 活性探针。
func startHealthzServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("healthz server 异常")
		}
	}()
	return srv
}
