package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/activescan"
	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/lesson"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/notes"
	"github.com/V3teran/liusha/internal/passivesession"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/worker"
)

// handler 持有所有跨任务共享依赖。
type handler struct {
	tasks           *agentrun.Store
	passiveSessions *passivesession.Store
	activeScans     *activescan.Store
	notes           *notes.RedisStore
	findings        *finding.Store
	lessons         *lesson.Store
	flows           *flow.Store
	calls           *llminvocation.Store
	cfg             config.Config
	scannerCfg      config.ScannerConfig
	pricing         llm.PricingProvider
	router          *llm.Router
	hunterBuilder   skill.Builder
	launcher        sandbox.Launcher
	logger          zerolog.Logger
	// parentRegistries 索引父 taskID → 子任务 Registry（subtask swarm）。
	// spawnerFactory 闭包 Store；handleActive 在 react.Run 返回后 LoadAndDelete
	// + cancel 父 ctx + WaitAll，确保子 goroutine 全退再 Destroy sandbox，防孤儿。
	parentRegistries *sync.Map
}

// failTask 把错误标记到 task 表。
func (h handler) failTask(ctx context.Context, taskID string, err error) error {
	if setErr := h.tasks.SetError(ctx, taskID, err.Error()); setErr != nil {
		h.logger.Warn().Err(setErr).Str("agent_run_id", taskID).
			Msg("SetError 失败（task 留在 running，原始错误已透传给 caller）")
	}
	return err
}

// abortTask 把 task 推进到 aborted 终态（reviewer 终止 / engagement 中止 / ctx 取消）。
// 与 failTask 区别：aborted 是"主动收手"非错误，不应触发告警。
func (h handler) abortTask(ctx context.Context, taskID, reason string) error {
	if setErr := h.tasks.SetAborted(ctx, taskID); setErr != nil {
		h.logger.Warn().Err(setErr).Str("agent_run_id", taskID).Str("reason", reason).
			Msg("SetAborted 失败（task 留在 running）")
	}
	h.logger.Info().Str("agent_run_id", taskID).Str("reason", reason).Msg("task aborted")
	return nil
}

// handle 是单个 hunter task 的处理入口。
//
// timeout 按 mode 分档：passive 用 AgentRunTimeoutSeconds（默认 1h），
// active 用 ActiveAgentRunTimeoutSeconds（默认 4h，对齐 sandbox max lifetime）。
func (h handler) handle(ctx context.Context, p worker.Payload) (retErr error) {
	taskStart := time.Now()
	h.logger.Info().
		Str("agent_run_id", p.TaskID).
		Str("engagement_id", p.EngagementID).
		Str("role", string(p.Role)).
		Msg("asynq task ▶ enter")
	defer func() {
		ev := h.logger.Info()
		if retErr != nil {
			ev = h.logger.Warn().Err(retErr)
		}
		ev.Str("agent_run_id", p.TaskID).
			Str("engagement_id", p.EngagementID).
			Dur("duration", time.Since(taskStart)).
			Msg("asynq task ◀ exit")
	}()

	// 入口检查：asynq 重试场景（PG status 已非 pending）→ SkipRetry。
	// 防 active 父被重试时新 Registry 空 → PreDoneCheck 永放行 → 旧 PG 子僵尸 + 矛盾态。
	// GetByID 错误（PG 短时不可用等）不阻塞——让 SetRunning 走正常错误路径。
	if run, getErr := h.tasks.GetByID(ctx, p.TaskID); getErr == nil && run.Status != agentrun.StatusPending {
		h.logger.Warn().
			Str("agent_run_id", p.TaskID).
			Str("status", string(run.Status)).
			Msg("asynq task 已被处理过，跳过重试（防 PG 僵尸 + 矛盾态）")
		return asynq.SkipRetry
	}

	if err := h.tasks.SetRunning(ctx, p.TaskID); err != nil {
		return err
	}

	var input struct {
		Mode       string          `json:"mode"`
		Entrypoint json.RawMessage `json:"entrypoint"`
	}
	if err := json.Unmarshal(p.Input, &input); err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}

	// 按 mode 选 timeout（解析 input 后才知道 mode；未知 mode 用 passive 兜底，
	// switch default 会立即报错，无超时浪费）。
	timeout := h.scannerCfg.AgentRunTimeoutSeconds
	if input.Mode == "active" {
		timeout = h.scannerCfg.ActiveAgentRunTimeoutSeconds
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
	}

	switch input.Mode {
	case "passive":
		return h.handlePassive(ctx, p, input.Entrypoint)
	case "active":
		return h.handleActive(ctx, p, input.Entrypoint)
	default:
		err := fmt.Errorf("unknown mode: %s", input.Mode)
		return h.failTask(ctx, p.TaskID, err)
	}
}
