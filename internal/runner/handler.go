package runner

import (
	"context"
	"time"

	"github.com/V3teran/liusha/internal/cognition"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/config"
	configstore "github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/convstream"
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/distill"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/settingstore"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/tools"
	"github.com/V3teran/liusha/internal/tools/manifest"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/worker"
)

// Handler 持有所有跨任务共享依赖。
type Handler struct {
	AgentRuns *agentrun.Store
	Tasks     *task.Store
	Findings  *finding.Store
	Corpus    *corpus.Service
	// WebSearch 是公开网页的检索后端（部署方接入了搜索接口才有）；nil 时 web_search 不注册。
	WebSearch  tools.WebSearcher
	ProxyStore *traffic.ProxyStore
	AgentStore *traffic.AgentStore
	Calls      *llminvocation.Store
	Settings   *settingstore.Store
	RunnerCfg  config.RunnerConfig
	// Heartbeats 是各 run 的存活心跳：run 执行期间续约，恢复监控据此判定它是否还在被推进。
	Heartbeats *Heartbeats
	// Sandbox 是沙盒服务客户端：任务开始时查询安装状态，工具经它执行命令，run 结束时释放任务资源。
	Sandbox sandbox.Client
	// SandboxListener 是沙盒对外可达性契约（监听段 + 回连地址），注入认知循环的工具契约。
	SandboxListener cognition.SandboxListener
	Logger          zerolog.Logger

	// LLM 路由（tier → provider）
	Router *llm.Router

	// 工具装配依赖（与 prompt 拼装共用）
	Creds         credential.Provider
	Audit         *audit.Store // 凭证库等敏感操作的 runner 侧审计（会话身份回收）
	ToolCalls     *toolinvocation.Store
	Skills        *skill.StoreReader // skill 渐进式加载后端（DB 事实源 + cfgstore 三级缓存）；cognition 按 agent.skills 声明建白名单视图
	ToolsManifest *manifest.ToolManifest

	CfgStore *configstore.Store

	Conversations  *conversation.Store
	EventPublisher *convstream.Publisher

	Graph    *explorationgraph.Store
	EventBus bus.EventBus // 统一事件总线

	// Distiller 是任务自然收敛后的经验沉淀；nil = 不沉淀。
	Distiller *distill.Service
	// DistillQueue 投递经验沉淀（持久化队列任务，由 HandleDistill 消费）；与 Distiller 同时装配。
	DistillQueue DistillQueue
}

// DistillQueue 是投递经验沉淀的窄接口（*worker.Client 满足）。
type DistillQueue interface {
	EnqueueDistill(ctx context.Context, taskID string, timeout time.Duration) error
}

// HandleDistill 消费一个任务的经验沉淀。返回错误即由队列重试（停机中断同样如此）：整合对同一
// 任务幂等，重试不会写出重复知识。
func (h *Handler) HandleDistill(ctx context.Context, taskID string) error {
	out, err := h.Distiller.DistillTask(ctx, taskID)
	if err != nil {
		h.Logger.Warn().Err(err).Str("task_id", taskID).Msg("任务经验沉淀失败（队列稍后重试）")
		return err
	}
	if out.Total() > 0 {
		h.Logger.Info().Str("task_id", taskID).Int("added", out.Added).Int("confirmed", out.Confirmed).
			Int("revised", out.Revised).Msg("任务经验已沉淀到知识库")
	}
	return nil
}

// credentialHost 是本 run 的凭据分区键：任务的目标主机（目标的派生列，见 task.Task），
// 目标里没有 http(s) 目标时按任务分区。开跑时取定——运行中调整目标，下一个 run 起才按新主机。
func credentialHost(tk task.Task) string {
	if tk.TargetHost != "" {
		return tk.TargetHost
	}
	return tk.ID
}

const terminalWriteTimeout = 10 * time.Second

func terminalCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), terminalWriteTimeout)
}

func (h *Handler) failTask(ctx context.Context, taskLogger zerolog.Logger, agentRunID, taskID, conversationID string, err error) error {
	writeCtx, cancel := terminalCtx(ctx)
	defer cancel()
	if setErr := h.AgentRuns.SetError(writeCtx, agentRunID); setErr != nil {
		taskLogger.Warn().Err(setErr).Str("agent_run_id", agentRunID).
			Msg("SetError 失败（task 留在 running，原始错误已透传给 caller）")
	}
	// task 终态：失败即 aborted。守卫幂等：已终态（如用户抢先停止）不覆盖，也不重复落节点。
	if taskID != "" {
		if applied, abortErr := h.Tasks.Abort(writeCtx, taskID, "执行失败: "+err.Error()); abortErr != nil {
			taskLogger.Warn().Err(abortErr).Msg("task 失败终态迁移被拒（保持既有终态）")
		} else if applied {
			h.appendTaskStatus(writeCtx, taskLogger, conversationID,
				conversation.TaskStatusFailed, "任务执行失败",
				map[string]any{"reason": err.Error()})
		}
	}
	return err
}

// Handle 是单个 agent 运行的处理入口。
func (h *Handler) Handle(ctx context.Context, p worker.Payload) (retErr error) {
	taskStart := time.Now()
	h.Logger.Info().
		Str("agent_run_id", p.AgentRunID).
		Str("task_id", p.TaskID).
		Str("role", string(p.TaskRole)).
		Msg("asynq task ▶ enter")
	defer func() {
		ev := h.Logger.Info()
		if retErr != nil {
			ev = h.Logger.Warn().Err(retErr)
		}
		ev.Str("agent_run_id", p.AgentRunID).
			Str("task_id", p.TaskID).
			Dur("duration", time.Since(taskStart)).
			Msg("asynq task ◀ exit")
	}()

	run, getErr := h.AgentRuns.GetByID(ctx, p.AgentRunID)
	switch {
	case getErr == nil && run.Status != agentrun.StatusPending:
		h.Logger.Warn().
			Str("agent_run_id", p.AgentRunID).
			Str("status", string(run.Status)).
			Msg("asynq task 已被处理过，跳过重试（防 PG 僵尸 + 矛盾态）")
		return asynq.SkipRetry
	case getErr != nil:
		// 查询失败不阻断执行（幂等检查 best-effort），但必须可见——否则重复消费只能靠下游发现。
		h.Logger.Warn().Err(getErr).Str("agent_run_id", p.AgentRunID).
			Msg("agent_run 查询失败，幂等防重检查被跳过")
	}

	// task 作用域派生：此后整条执行链（含 cognition 内部全部组件）经 taskLogger
	// 出日志，task_id/assignment_id/conv 自动附加，调用点不再手写。
	tk, tErr := h.Tasks.GetByID(ctx, p.TaskID)
	taskLogger := h.Logger.With().
		Str("task_id", p.TaskID).
		Str("assignment_id", tk.AssignmentID).
		Str("conv", p.ConversationID).
		Logger()

	// 任务行是 run 的输入（目标主机、所属批次）：读不到就无从开跑。run 落 error、任务保持
	// active，由恢复监控按出错续跑（计入重投上限）——与开跑时沙盒不可达同一口径。
	if tErr != nil {
		writeCtx, cancel := terminalCtx(ctx)
		defer cancel()
		if err := h.AgentRuns.SetError(writeCtx, p.AgentRunID); err != nil {
			taskLogger.Warn().Err(err).Msg("run 出错终态回写失败（由僵尸 run 回收兜底）")
		}
		taskLogger.Warn().Err(tErr).Msg("读取任务失败：本次 run 出错，任务保持进行中，由恢复监控重试")
		return nil
	}

	// task 终态/暂停守卫：排队期间被用户停止/判死/暂停的任务不得开跑——terminate
	// 只对运行中的循环生效，队列里的迟到领取靠此闸拦下；暂停中的任务开跑也没有
	// 意义（循环首个控制周期就会因暂停标记退出），同样拦下。run 直落 aborted，
	// 暂停的由继续操作重投。
	if tk.Status != task.StatusActive || tk.Paused {
		taskLogger.Info().
			Str("status", string(tk.Status)).
			Bool("paused", tk.Paused).
			Msg("task 已终态或暂停中（排队期间被停止/判死/暂停），跳过执行")
		writeCtx, cancel := terminalCtx(ctx)
		defer cancel()
		if err := h.AgentRuns.SetAborted(writeCtx, p.AgentRunID); err != nil {
			taskLogger.Warn().Err(err).Str("agent_run_id", p.AgentRunID).Msg("agent_run 置 aborted 失败")
		}
		return asynq.SkipRetry
	}

	if err := h.AgentRuns.SetRunning(ctx, p.AgentRunID); err != nil {
		return err
	}

	// 存活心跳与这次执行同生命周期：执行结束（无论何种收尾）即停止续约。
	defer h.Heartbeats.Attach(ctx, h.AgentRuns, p.AgentRunID, taskLogger)()

	// 设置超时
	timeout := h.RunnerCfg.AgentRunTimeoutSeconds
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
	}

	// 四Agent认知架构
	return h.executeTask(ctx, p, tk, taskLogger)
}
