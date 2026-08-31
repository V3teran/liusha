package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/domain"
	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/orchestrator"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/config/settingstore"
	"github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/eventbus"
	"github.com/V3teran/liusha/internal/worldmodel"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/lead"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/ratelimit"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/scanstream"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/tools/manifest"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/worker"
)

// handler 持有所有跨任务共享依赖。
type handler struct {
	executors  *agentrun.Store
	tasks      *task.Store
	findings   *finding.Store
	corpus     *corpus.Store
	embedder   corpus.Embedder      // Jina embed（可 nil，降级纯 sparse）
	reranker   corpus.Reranker
	leads      *lead.Store
	proxyStore *traffic.ProxyStore
	agentStore *traffic.AgentStore
	calls      *llminvocation.Store
	hostSem    *ratelimit.HostSemaphore
	settings   *settingstore.Store
	runnerCfg  config.RunnerConfig
	sandboxMgr *sandbox.PooledManager
	logger     zerolog.Logger

	// LLM 路由（tier → provider）
	router *provider.Router

	// 工具装配依赖（与 prompt 拼装共用）
	creds         credential.Provider
	toolCalls     *toolinvocation.Store
	toolingLoader *skill.Loader
	vulnLoader    *skill.Loader
	toolsManifest *manifest.Manifest

	cfgStore *configstore.Store

	conversations  *conversation.Store
	eventPublisher *scanstream.Publisher

	profiles     *domain.Registry
	world        *worldmodel.Store
	checkpoint   executor.CheckpointStore
	eventBus     *orchestrator.EventBus     // Task 级别事件总线（Planner 用）
	actionBus    *eventbus.Bus           // Action 级别事件总线（Executor 用）
	plannerMgr   *plannerAgentManager
	controlPlane *controlplane.Store
}

// onboard 用域注册表解析 brief 目标，并完成三件 best-effort 副作用：
//  1. 落 L3 世界模型 KindObjective 节点（目标）；
//  2. 回填 task.target_host 派生列；
//  3. 返回 host key 供调用方下传。
func (h handler) onboard(ctx context.Context, assignmentID, taskID, brief string) string {
	refs, ok := h.profiles.Onboard(ctx, domain.BriefInput{Brief: brief})
	if !ok || len(refs) == 0 || refs[0].Locator == "" {
		return taskID
	}

	if h.world != nil && taskID != "" {
		for _, ref := range refs {
			content, _ := json.Marshal(map[string]interface{}{
				"target_ref": ref,
			})
			node := worldmodel.Node{
				ID:         uuid.New().String(),
				TaskID:     taskID,
				Kind:       worldmodel.KindObjective,
				Content:    content,
				Priority:   5,
				SourceType: "user",
				SourceID:   "task_init",
				CreatedAt:  time.Now(),
				UpdatedAt:  time.Now(),
			}
			if _, err := h.world.CreateNode(ctx, node); err != nil {
				h.logger.Warn().Err(err).Str("task_id", taskID).
					Str("locator", ref.Locator).Msg("创建 objective 节点失败（不阻塞扫描）")
			}
		}
	}

	host := refs[0].Locator
	if host != taskID {
		if err := h.tasks.SetTargetHost(ctx, taskID, host); err != nil {
			h.logger.Warn().Err(err).Str("task_id", taskID).Str("host", host).
				Msg("回填 task.target_host 失败（不阻塞扫描）")
		}
	}
	return host
}

const terminalWriteTimeout = 10 * time.Second

func terminalCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), terminalWriteTimeout)
}

func (h handler) failTask(ctx context.Context, executorID string, err error) error {
	writeCtx, cancel := terminalCtx(ctx)
	defer cancel()
	if setErr := h.executors.SetError(writeCtx, executorID, err.Error()); setErr != nil {
		h.logger.Warn().Err(setErr).Str("executor_id", executorID).
			Msg("SetError 失败（task 留在 running，原始错误已透传给 caller）")
	}
	return err
}

func (h handler) abortTask(ctx context.Context, executorID, reason string) error {
	writeCtx, cancel := terminalCtx(ctx)
	defer cancel()
	if setErr := h.executors.SetAborted(writeCtx, executorID); setErr != nil {
		h.logger.Warn().Err(setErr).Str("executor_id", executorID).Str("reason", reason).
			Msg("SetAborted 失败（task 留在 running）")
	}
	h.logger.Info().Str("executor_id", executorID).Str("reason", reason).Msg("task aborted")
	return nil
}

// handle 是单个 agent task 的处理入口。
func (h handler) handle(ctx context.Context, p worker.Payload) (retErr error) {
	taskStart := time.Now()
	h.logger.Info().
		Str("executor_id", p.ExecutorID).
		Str("task_id", p.TaskID).
		Str("role", string(p.Role)).
		Msg("asynq task ▶ enter")
	defer func() {
		ev := h.logger.Info()
		if retErr != nil {
			ev = h.logger.Warn().Err(retErr)
		}
		ev.Str("executor_id", p.ExecutorID).
			Str("task_id", p.TaskID).
			Dur("duration", time.Since(taskStart)).
			Msg("asynq task ◀ exit")

		// Task 结束时停止 Planner Agent
		h.plannerMgr.Stop(p.TaskID)
	}()

	if run, getErr := h.executors.GetByID(ctx, p.ExecutorID); getErr == nil && run.Status != agentrun.StatusPending {
		h.logger.Warn().
			Str("executor_id", p.ExecutorID).
			Str("status", string(run.Status)).
			Msg("asynq task 已被处理过，跳过重试（防 PG 僵尸 + 矛盾态）")
		return asynq.SkipRetry
	}

	// 启动 Planner Agent（异步，事件驱动）
	if err := h.plannerMgr.Start(ctx, h, p.TaskID); err != nil {
		h.logger.Error().Err(err).Str("task_id", p.TaskID).Msg("启动 Planner Agent 失败（不阻塞任务）")
	}

	if h.hostSem != nil {
		if tk, tErr := h.tasks.GetByID(ctx, p.TaskID); tErr == nil && tk.TargetHost != "" {
			rel, ok, semErr := h.hostSem.Acquire(ctx, tk.TargetHost)
			switch {
			case semErr != nil:
				h.logger.Warn().Err(semErr).Str("host", tk.TargetHost).Msg("per-host 信号量 acquire 失败，放行不阻塞")
			case !ok:
				h.logger.Info().Str("host", tk.TargetHost).Str("task_id", p.TaskID).
					Msg("per-host 并发已达上限，退避重试")
				return fmt.Errorf("per-host 并发上限（host=%s），退避重试", tk.TargetHost)
			default:
				defer rel()
			}
		}
	}

	if err := h.executors.SetRunning(ctx, p.ExecutorID); err != nil {
		return err
	}

	var input struct {
		Brief string `json:"brief"`
	}
	if err := json.Unmarshal(p.Input, &input); err != nil {
		return h.failTask(ctx, p.ExecutorID, err)
	}

	// 设置超时
	timeout := h.runnerCfg.SwarmAgentRunTimeoutSeconds
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
	}

	// 新架构：统一走Planner + Executor
	return h.handleCognition(ctx, p, input.Brief)
}
