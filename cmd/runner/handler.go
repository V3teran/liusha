package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"

	cfgscenario "github.com/V3teran/liusha/internal/config/scenario"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/config/settingstore"
	"github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/lead"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/agentrun"
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
	"github.com/V3teran/liusha/internal/worldmodel"
	"github.com/V3teran/liusha/internal/actor"
	"github.com/V3teran/liusha/internal/ledger"
)

// handler 持有所有跨任务共享依赖。
type handler struct {
	operators  *agentrun.Store
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
	sandboxMgr *sandbox.Manager
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

	profiles *executor.Registry
	world    *worldmodel.Store

	// Actor 基础设施
	ledger     *ledger.Ledger
	checkpoint actor.CheckpointStore
}

// onboard 用域注册表解析 brief 目标，并完成三件 best-effort 副作用：
//  1. 落 L3 世界模型 KindTarget 节点（幂等 upsert）；
//  2. 回填 task.target_host 派生列；
//  3. 返回 host key 供调用方下传。
func (h handler) onboard(ctx context.Context, assignmentID, taskID, brief string) string {
	refs, ok := h.profiles.Onboard(ctx, executor.BriefInput{Brief: brief})
	if !ok || len(refs) == 0 || refs[0].Locator == "" {
		return taskID
	}

	if h.world != nil && assignmentID != "" {
		for _, ref := range refs {
			if _, err := h.world.UpsertNode(ctx, worldmodel.Node{
				TaskID: assignmentID,
				Kind:   worldmodel.KindTarget,
				Ref:    ref,
			}); err != nil {
				h.logger.Warn().Err(err).Str("assignment_id", assignmentID).
					Str("locator", ref.Locator).Msg("落 KindTarget 世界模型节点失败（不阻塞扫描）")
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

func (h handler) failTask(ctx context.Context, operatorID string, err error) error {
	writeCtx, cancel := terminalCtx(ctx)
	defer cancel()
	if setErr := h.operators.SetError(writeCtx, operatorID, err.Error()); setErr != nil {
		h.logger.Warn().Err(setErr).Str("operator_id", operatorID).
			Msg("SetError 失败（task 留在 running，原始错误已透传给 caller）")
	}
	return err
}

func (h handler) abortTask(ctx context.Context, operatorID, reason string) error {
	writeCtx, cancel := terminalCtx(ctx)
	defer cancel()
	if setErr := h.operators.SetAborted(writeCtx, operatorID); setErr != nil {
		h.logger.Warn().Err(setErr).Str("operator_id", operatorID).Str("reason", reason).
			Msg("SetAborted 失败（task 留在 running）")
	}
	h.logger.Info().Str("operator_id", operatorID).Str("reason", reason).Msg("task aborted")
	return nil
}

// handle 是单个 hunter task 的处理入口。
func (h handler) handle(ctx context.Context, p worker.Payload) (retErr error) {
	taskStart := time.Now()
	h.logger.Info().
		Str("operator_id", p.OperatorID).
		Str("task_id", p.TaskID).
		Str("role", string(p.Role)).
		Msg("asynq task ▶ enter")
	defer func() {
		ev := h.logger.Info()
		if retErr != nil {
			ev = h.logger.Warn().Err(retErr)
		}
		ev.Str("operator_id", p.OperatorID).
			Str("task_id", p.TaskID).
			Dur("duration", time.Since(taskStart)).
			Msg("asynq task ◀ exit")
	}()

	if run, getErr := h.operators.GetByID(ctx, p.OperatorID); getErr == nil && run.Status != agentrun.StatusPending {
		h.logger.Warn().
			Str("operator_id", p.OperatorID).
			Str("status", string(run.Status)).
			Msg("asynq task 已被处理过，跳过重试（防 PG 僵尸 + 矛盾态）")
		return asynq.SkipRetry
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

	if err := h.operators.SetRunning(ctx, p.OperatorID); err != nil {
		return err
	}

	var input struct {
		Brief string `json:"brief"`
	}
	if err := json.Unmarshal(p.Input, &input); err != nil {
		return h.failTask(ctx, p.OperatorID, err)
	}

	scen, err := h.cfgStore.ScenarioByCode(ctx, p.ScenarioID)
	if err != nil {
		return h.failTask(ctx, p.OperatorID, fmt.Errorf("加载 scenario %s 失败: %w", p.ScenarioID, err))
	}

	timeout := h.runnerCfg.SoloAgentRunTimeoutSeconds
	if scen.Engine == cfgscenario.EngineSwarm {
		timeout = h.runnerCfg.SwarmAgentRunTimeoutSeconds
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
	}

	switch scen.Engine {
	case cfgscenario.EngineSolo:
		if scen.SoloOperatorID == nil || *scen.SoloOperatorID == "" {
			return h.failTask(ctx, p.OperatorID, fmt.Errorf("solo scenario %s 未指定 solo_operator_id", scen.Code))
		}
		op, err := h.cfgStore.OperatorByID(ctx, *scen.SoloOperatorID)
		if err != nil {
			return h.failTask(ctx, p.OperatorID, fmt.Errorf("scenario %s 引用的 operator %s 加载失败: %w", scen.Code, *scen.SoloOperatorID, err))
		}
		return h.handleSolo(ctx, p, scen, op, input.Brief)
	case cfgscenario.EngineSwarm:
		operators, err := h.cfgStore.EnabledDomainOperators(ctx)
		if err != nil {
			return h.failTask(ctx, p.OperatorID, fmt.Errorf("加载 enabled 领域操作员失败: %w", err))
		}
		return h.handleSwarm(ctx, p, scen, operators, input.Brief)
	default:
		return h.failTask(ctx, p.OperatorID, fmt.Errorf("unknown engine: %s", scen.Engine))
	}
}
