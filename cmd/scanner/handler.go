package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"

	hunterbuilder "github.com/V3teran/liusha/internal/builder/hunter"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/einoagent"
	"github.com/V3teran/liusha/internal/einollm"
	"github.com/V3teran/liusha/internal/einotools"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/hunterrun"
	"github.com/V3teran/liusha/internal/lead"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/ratelimit"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/scanstream"
	"github.com/V3teran/liusha/internal/scenario"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/worker"
)

// handler 持有所有跨任务共享依赖。
type handler struct {
	hunters    *hunterrun.Store
	tasks      *task.Store
	findings   *finding.Store
	corpus     *corpus.Store            // 跨目标知识库（hybrid RAG）；search/write_corpus
	embedder   einotools.CorpusEmbedder // Jina embed（可 nil，降级纯 sparse）
	reranker   corpus.Reranker          // Jina rerank（可 nil，降级合并序兜底）
	leads      *lead.Store              // 情报黑板（§7）；每次写滚动刷新 target_host 的 TTL
	proxyFlows *traffic.ProxyStore
	agentFlows *traffic.AgentStore
	calls      *llminvocation.Store
	hostSem    *ratelimit.HostSemaphore // per-host 并发限速（§4.3）；仅对有 target_host 的 task 生效
	cfg        config.Config
	scannerCfg config.ScannerConfig
	launcher   sandbox.Launcher
	logger     zerolog.Logger

	// eino agent：passive + active 路径走 eino ChatModelAgent（唯一路径，react 退路已删）。
	//   - einoFactory：按 role 产独立 eino ChatModel
	//   - hunterDeps：prompt 拼装 + 工具装配的 store/loader 依赖
	einoFactory *einollm.Factory
	hunterDeps  hunterbuilder.Deps

	// roles 是 active deep 角色定义（hunters/active/*.md 加载），active 路径用 BuildDeepSwarm 装配
	// 主代理（orchestrator）+ 杀伤链子代理。
	roles []einoagent.RoleDef

	// passiveRole 是 passive 单 agent 角色（hunters/passive/traffic-analysis.md 加载）；
	// passive 路径用其 SystemPrompt + MaxIterations 跑 RunTrafficAnalysis。
	passiveRole einoagent.RoleDef

	// conversations + eventPublisher 是阶段B 过程事件管道：会话发起（Payload.ConversationID
	// 非空）时，agent 每次工具调用落 conversation message（PG）+ publish redis（实时推前端）。
	// 二者任一 nil 时不发事件（向后兼容纯后台扫描）。
	conversations  *conversation.Store
	eventPublisher *scanstream.Publisher

	// scenarioRoles 是场景 role（scenarios/*.md，阶段C）：active/passive handler 按 Payload.ScenarioID
	// 注入主代理人设。空/未匹配时不注入（退化为通用扫描）。
	scenarioRoles []scenario.Role
}

// terminalWriteTimeout 是终态写入（SetError/SetAborted）的独立超时上限。
const terminalWriteTimeout = 10 * time.Second

// terminalCtx 从入参 ctx 派生一个「不随其取消/超时失效」的写入 ctx（WithoutCancel 保留携带值用于日志关联）。
// 终态写入必须与请求生命周期解耦：当任务失败原因正是 ctx 超时/取消时，复用入参 ctx 会让 SetError/SetAborted
// 也立即失败，task 便永远悬挂 running（active_scan 已终态但 hunter 还 running 的不一致根因）。
func terminalCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), terminalWriteTimeout)
}

// failTask 把错误标记到 task 表。
func (h handler) failTask(ctx context.Context, hunterID string, err error) error {
	writeCtx, cancel := terminalCtx(ctx)
	defer cancel()
	if setErr := h.hunters.SetError(writeCtx, hunterID, err.Error()); setErr != nil {
		h.logger.Warn().Err(setErr).Str("hunter_id", hunterID).
			Msg("SetError 失败（task 留在 running，原始错误已透传给 caller）")
	}
	return err
}

// abortTask 把 task 推进到 aborted 终态（inspector 终止 / owner 中止 / ctx 取消）。
// 与 failTask 区别：aborted 是"主动收手"非错误，不应触发告警。
func (h handler) abortTask(ctx context.Context, hunterID, reason string) error {
	writeCtx, cancel := terminalCtx(ctx)
	defer cancel()
	if setErr := h.hunters.SetAborted(writeCtx, hunterID); setErr != nil {
		h.logger.Warn().Err(setErr).Str("hunter_id", hunterID).Str("reason", reason).
			Msg("SetAborted 失败（task 留在 running）")
	}
	h.logger.Info().Str("hunter_id", hunterID).Str("reason", reason).Msg("task aborted")
	return nil
}

// handle 是单个 hunter task 的处理入口。
//
// timeout 按 mode 分档：passive 用 AgentRunTimeoutSeconds（默认 1h），
// active 用 ActiveAgentRunTimeoutSeconds（默认 4h，对齐 sandbox max lifetime）。
func (h handler) handle(ctx context.Context, p worker.Payload) (retErr error) {
	taskStart := time.Now()
	h.logger.Info().
		Str("hunter_id", p.HunterID).
		Str("task_id", p.TaskID).
		Str("role", string(p.Role)).
		Msg("asynq task ▶ enter")
	defer func() {
		ev := h.logger.Info()
		if retErr != nil {
			ev = h.logger.Warn().Err(retErr)
		}
		ev.Str("hunter_id", p.HunterID).
			Str("task_id", p.TaskID).
			Dur("duration", time.Since(taskStart)).
			Msg("asynq task ◀ exit")
	}()

	// 入口检查：asynq 重试场景（PG status 已非 pending）→ SkipRetry。
	// 防 orchestrator被重试时新 Registry 空 → PreDoneCheck 永放行 → 旧 PG exploitation 僵尸 + 矛盾态。
	// GetByID 错误（PG 短时不可用等）不阻塞——让 SetRunning 走正常错误路径。
	if run, getErr := h.hunters.GetByID(ctx, p.HunterID); getErr == nil && run.Status != hunterrun.StatusPending {
		h.logger.Warn().
			Str("hunter_id", p.HunterID).
			Str("status", string(run.Status)).
			Msg("asynq task 已被处理过，跳过重试（防 PG 僵尸 + 矛盾态）")
		return asynq.SkipRetry
	}

	// per-host 并发限速（§4.3）：占一个 host 额度，超限则退避重试（此时 task 仍 pending，
	// 下次重试入口 SkipRetry 检查不误拦）。active orchestrator 的 target_host 空 → 放行
	// （真正打 host 的是它 spawn 的子任务）；passive task 恒有 host → 受限。
	// 限速是增强非硬门：信号量本身出错（Redis 抖动）则放行，不卡死扫描。
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

	if err := h.hunters.SetRunning(ctx, p.HunterID); err != nil {
		return err
	}

	var input struct {
		Mode       string          `json:"mode"`
		Entrypoint json.RawMessage `json:"entrypoint"`
	}
	if err := json.Unmarshal(p.Input, &input); err != nil {
		return h.failTask(ctx, p.HunterID, err)
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
		return h.handlePassiveEino(ctx, p, input.Entrypoint)
	case "active":
		return h.handleActiveEino(ctx, p, input.Entrypoint)
	default:
		err := fmt.Errorf("unknown mode: %s", input.Mode)
		return h.failTask(ctx, p.HunterID, err)
	}
}
