package main

import (
	"context"
	"encoding/json"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/config/settingstore"
	configstore "github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/insight"
	"github.com/V3teran/liusha/internal/llminvocation"
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
	agentRuns  *agentrun.Store
	tasks      *task.Store
	findings   *finding.Store
	corpus     *corpus.Store
	embedder   corpus.Embedder // Jina embed（可 nil，降级纯 sparse）
	reranker   corpus.Reranker
	insights   *insight.Store
	proxyStore *traffic.ProxyStore
	agentStore *traffic.AgentStore
	calls      *llminvocation.Store
	settings   *settingstore.Store
	runnerCfg  config.RunnerConfig
	sandboxMgr sandbox.Manager // 改为接口类型（支持 SingletonManager 和未来的其他实现）
	logger     zerolog.Logger

	// LLM 路由（tier → provider）
	router *llm.Router

	// 工具装配依赖（与 prompt 拼装共用）
	creds         credential.Provider
	audit         *audit.Store // 凭证库等敏感操作的 runner 侧审计（会话身份回收）
	toolCalls     *toolinvocation.Store
	skills        *skill.StoreReader // skill 渐进式加载后端（DB 事实源 + cfgstore 三级缓存）；cognition 按 agent.skills 声明建白名单视图
	toolsManifest *manifest.Manifest

	cfgStore *configstore.Store

	conversations  *conversation.Store
	eventPublisher *scanstream.Publisher

	graph        *explorationgraph.Store
	checkpointer core.Checkpointer
	eventBus     bus.Bus // 统一事件总线
	controlPlane *controlplane.Store
}

// hostRe 匹配 brief 里的 http(s) 目标（含端口）——任务装配层的入口记账：
// 抽出首个 host 作 target_host 派生列与凭据池键。认知机械不做文本抽取。
var hostRe = regexp.MustCompile(`https?://([^/\s]+)`)

// onboard 从 brief 抽取目标 host 并完成三件 best-effort 副作用：
//  1. 落探索图 KindObjective 节点（目标）；
//  2. 回填 task.target_host 派生列；
//  3. 返回 host key 供调用方下传。
func (h handler) onboard(ctx context.Context, _, taskID, brief string, taskLogger zerolog.Logger) string {
	matches := hostRe.FindAllStringSubmatch(brief, -1)
	hosts := make([]string, 0, len(matches))
	seen := map[string]bool{}
	for _, m := range matches {
		if h := m[1]; h != "" && !seen[h] {
			seen[h] = true
			hosts = append(hosts, h)
		}
	}
	ok := len(hosts) > 0

	taskLogger.Info().
		Str("brief", brief).
		Bool("onboard_ok", ok).
		Int("hosts_found", len(hosts)).
		Msg("onboard 开始解析")

	if !ok {
		taskLogger.Warn().
			Bool("ok", ok).
			Msg("onboard 解析失败，未创建 Objective")
		return taskID
	}

	if h.graph != nil && taskID != "" {
		for _, targetHost := range hosts {
			content, _ := json.Marshal(map[string]interface{}{
				"target_host": targetHost,
				"description": brief, // 添加 description 供 Planner 读取
			})

			// Objective 节点需要 state 字段（数据库约束 ck_state_by_kind 要求）
			openState := explorationgraph.StateOpen

			node := explorationgraph.Node{
				ID:         uuid.New().String(),
				TaskID:     taskID,
				Kind:       core.KindObjective,
				Content:    content,
				State:      &openState, // 必须设置，否则违反数据库约束
				Priority:   explorationgraph.PriorityMedium,
				SourceType: "user",
				SourceID:   "task_init",
				CreatedAt:  time.Now(),
				UpdatedAt:  time.Now(),
			}

			taskLogger.Info().
				Str("objective_id", node.ID).
				Str("host", targetHost).
				Msg("准备创建 Objective 节点")

			if _, err := h.graph.CreateNode(ctx, node); err != nil {
				// 改进1: Fail-Fast - Objective 创建失败应该中止任务
				taskLogger.Error().Err(err).
					Str("host", targetHost).Msg("创建 objective 节点失败 - 任务将无法正常执行")
				return taskID // 返回原 taskID，但已记录严重错误
			}
			taskLogger.Info().
				Str("objective_id", node.ID).
				Str("host", targetHost).
				Msg("Objective 节点创建成功")
		}
	} else {
		taskLogger.Warn().
			Bool("graph_nil", h.graph == nil).
			Bool("taskID_empty", taskID == "").
			Msg(" 跳过创建 Objective：graph 为 nil 或 taskID 为空")
	}

	host := hosts[0]
	if host != taskID {
		if err := h.tasks.SetTargetHost(ctx, taskID, host); err != nil {
			taskLogger.Warn().Err(err).Str("host", host).
				Msg("回填 task.target_host 失败（不阻塞扫描）")
		}
	}
	return host
}

const terminalWriteTimeout = 10 * time.Second

func terminalCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), terminalWriteTimeout)
}

func (h handler) failTask(ctx context.Context, taskLogger zerolog.Logger, agentRunID, taskID, convID string, err error) error {
	writeCtx, cancel := terminalCtx(ctx)
	defer cancel()
	if setErr := h.agentRuns.SetError(writeCtx, agentRunID, err.Error()); setErr != nil {
		taskLogger.Warn().Err(setErr).Str("agent_run_id", agentRunID).
			Msg("SetError 失败（task 留在 running，原始错误已透传给 caller）")
	}
	// task 终态：失败即 aborted（此前缺失——task 停留 active 直至 reaper 判死，
	// 期间前端显示「进行中」名不副实）。守卫幂等：已终态（如用户抢先停止）不覆盖。
	if taskID != "" {
		if abortErr := h.tasks.Abort(writeCtx, taskID, "执行失败: "+err.Error()); abortErr != nil {
			taskLogger.Warn().Err(abortErr).Msg("task 失败终态迁移被拒（保持既有终态）")
		} else {
			h.appendTaskStatus(writeCtx, taskLogger, convID,
				conversation.TaskStatusFailed, "扫描执行失败",
				map[string]any{"reason": err.Error()})
		}
	}
	return err
}

// handle 是单个 agent 运行的处理入口。
func (h handler) handle(ctx context.Context, p worker.Payload) (retErr error) {
	taskStart := time.Now()
	h.logger.Info().
		Str("agent_run_id", p.AgentRunID).
		Str("task_id", p.TaskID).
		Str("role", string(p.TaskRole)).
		Msg("asynq task ▶ enter")
	defer func() {
		ev := h.logger.Info()
		if retErr != nil {
			ev = h.logger.Warn().Err(retErr)
		}
		ev.Str("agent_run_id", p.AgentRunID).
			Str("task_id", p.TaskID).
			Dur("duration", time.Since(taskStart)).
			Msg("asynq task ◀ exit")
	}()

	run, getErr := h.agentRuns.GetByID(ctx, p.AgentRunID)
	switch {
	case getErr == nil && run.Status != agentrun.StatusPending:
		h.logger.Warn().
			Str("agent_run_id", p.AgentRunID).
			Str("status", string(run.Status)).
			Msg("asynq task 已被处理过，跳过重试（防 PG 僵尸 + 矛盾态）")
		return asynq.SkipRetry
	case getErr != nil:
		// 查询失败不阻断执行（幂等检查 best-effort），但必须可见——否则重复消费只能靠下游发现。
		h.logger.Warn().Err(getErr).Str("agent_run_id", p.AgentRunID).
			Msg("agent_run 查询失败，幂等防重检查被跳过")
	}

	// task 作用域派生：此后整条执行链（含 cognition 内部全部组件）经 taskLogger
	// 出日志，task_id/assignment_id/conv 自动附加，调用点不再手写。assignmentID
	// 取自 task 行；加载失败降级为空串（下游字段缺省，不阻塞执行）。
	var assignmentID string
	tk, tErr := h.tasks.GetByID(ctx, p.TaskID)
	if tErr == nil {
		assignmentID = tk.AssignmentID
	}
	taskLogger := h.logger.With().
		Str("task_id", p.TaskID).
		Str("assignment_id", assignmentID).
		Str("conv", p.ConversationID).
		Logger()

	// task 终态守卫：排队期间被用户停止/判死的任务不得开跑（terminate 只对
	// 运行中的循环生效，队列里的迟到领取靠此闸拦下），agent_run 直落 aborted。
	if tErr == nil && tk.Status != task.StatusActive {
		taskLogger.Info().
			Str("status", string(tk.Status)).
			Msg("task 已终态（排队期间被停止/判死），跳过执行")
		writeCtx, cancel := terminalCtx(ctx)
		defer cancel()
		if err := h.agentRuns.SetAborted(writeCtx, p.AgentRunID); err != nil {
			taskLogger.Warn().Err(err).Str("agent_run_id", p.AgentRunID).Msg("agent_run 置 aborted 失败")
		}
		return asynq.SkipRetry
	}

	if err := h.agentRuns.SetRunning(ctx, p.AgentRunID); err != nil {
		return err
	}

	var input struct {
		Brief string `json:"brief"`
	}
	if err := json.Unmarshal(p.Input, &input); err != nil {
		return h.failTask(ctx, taskLogger, p.AgentRunID, p.TaskID, p.ConversationID, err)
	}

	// 设置超时
	timeout := h.runnerCfg.AgentRunTimeoutSeconds
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
	}

	// 四Agent认知架构
	return h.handleCognition(ctx, p, input.Brief, taskLogger, assignmentID)
}
