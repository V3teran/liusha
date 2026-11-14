package runner

import (
	"context"
	"encoding/json"
	"regexp"
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
	"github.com/V3teran/liusha/internal/tools/manifest"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/worker"
)

// Handler 持有所有跨任务共享依赖。
type Handler struct {
	AgentRuns  *agentrun.Store
	Tasks      *task.Store
	Findings   *finding.Store
	Corpus     *corpus.Store
	Embedder   corpus.Embedder // Jina embed（可 nil，降级纯 sparse）
	Reranker   corpus.Reranker
	ProxyStore *traffic.ProxyStore
	AgentStore *traffic.AgentStore
	Calls      *llminvocation.Store
	Settings   *settingstore.Store
	RunnerCfg  config.RunnerConfig
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

	// Distiller 是任务终态经验蒸馏（corpus source=agent 写入闭环）；
	// nil=关闭该能力（如 corpus 未装配）。
	Distiller *distill.Service
}

// hostRe 匹配 brief 里的 http(s) 目标（含端口）——任务装配层的入口记账：
// 抽出首个 host 作 target_host 派生列与凭据池键。认知机械不做文本抽取。
var hostRe = regexp.MustCompile(`https?://([^/\s]+)`)

// onboard 从 brief 抽取首个目标 host：回填 task.target_host 派生列，并返回 host key
// 供调用方下传（凭据池等按它分区）。根目标由认知循环创建（EnsureRootObjective）。
func (h *Handler) onboard(ctx context.Context, taskID, brief string, taskLogger zerolog.Logger) string {
	m := hostRe.FindStringSubmatch(brief)
	if m == nil {
		taskLogger.Info().Msg("简报里没有 http(s) 目标，凭据按任务分区")
		return taskID
	}
	host := m[1]
	if err := h.Tasks.SetTargetHost(ctx, taskID, host); err != nil {
		taskLogger.Warn().Err(err).Str("host", host).Msg("回填 task.target_host 失败（不阻塞扫描）")
	}
	return host
}

const terminalWriteTimeout = 10 * time.Second

func terminalCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), terminalWriteTimeout)
}

func (h *Handler) failTask(ctx context.Context, taskLogger zerolog.Logger, agentRunID, taskID, conversationID string, err error) error {
	writeCtx, cancel := terminalCtx(ctx)
	defer cancel()
	if setErr := h.AgentRuns.SetError(writeCtx, agentRunID, err.Error()); setErr != nil {
		taskLogger.Warn().Err(setErr).Str("agent_run_id", agentRunID).
			Msg("SetError 失败（task 留在 running，原始错误已透传给 caller）")
	}
	// task 终态：失败即 aborted（此前缺失——task 停留 active 直至 reaper 判死，
	// 期间前端显示「进行中」名不副实）。守卫幂等：已终态（如用户抢先停止）不覆盖。
	if taskID != "" {
		if abortErr := h.Tasks.Abort(writeCtx, taskID, "执行失败: "+err.Error()); abortErr != nil {
			taskLogger.Warn().Err(abortErr).Msg("task 失败终态迁移被拒（保持既有终态）")
		} else {
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
	// 出日志，task_id/assignment_id/conv 自动附加，调用点不再手写。assignmentID
	// 取自 task 行；加载失败降级为空串（下游字段缺省，不阻塞执行）。
	var assignmentID string
	tk, tErr := h.Tasks.GetByID(ctx, p.TaskID)
	if tErr == nil {
		assignmentID = tk.AssignmentID
	}
	taskLogger := h.Logger.With().
		Str("task_id", p.TaskID).
		Str("assignment_id", assignmentID).
		Str("conv", p.ConversationID).
		Logger()

	// task 终态/暂停守卫：排队期间被用户停止/判死/暂停的任务不得开跑——terminate
	// 只对运行中的循环生效，队列里的迟到领取靠此闸拦下；暂停中的任务开跑也没有
	// 意义（循环首个控制周期就会因暂停标记退出），同样拦下。run 直落 aborted，
	// 暂停的由继续操作重投。
	if tErr == nil && (tk.Status != task.StatusActive || tk.Paused) {
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

	var input struct {
		Brief string `json:"brief"`
	}
	if err := json.Unmarshal(p.Input, &input); err != nil {
		return h.failTask(ctx, taskLogger, p.AgentRunID, p.TaskID, p.ConversationID, err)
	}

	// 设置超时
	timeout := h.RunnerCfg.AgentRunTimeoutSeconds
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
	}

	// 四Agent认知架构
	return h.executeTask(ctx, p, input.Brief, taskLogger, assignmentID)
}
