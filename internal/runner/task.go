package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/cognition"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/task"

	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/worker"
)

// ─────────────────────────────────────────────────────────────
//  任务执行生命周期（终态回写 / 时间线 / 身份回收）
// ─────────────────────────────────────────────────────────────

// executeTask 是单个任务的执行全生命周期：心跳 → 沙箱获取 → 驱动认知循环 → 会话身份
// 回收 → agent_run/task 终态回写。tk 是开跑时读到的任务行（目标主机在本 run 内取定）。
// 所有任务都走同一条链；taskLogger 是 Handle() 在任务作用域派生的 logger
// （task_id/assignment_id/conv 自动附加），本函数与下游（runCognition →
// cognition.Deps）全部经它出日志。
func (h *Handler) executeTask(
	ctx context.Context,
	p worker.Payload,
	tk task.Task,
	taskLogger zerolog.Logger,
) error {
	taskID := p.TaskID
	if err := h.Tasks.Heartbeat(ctx, taskID); err != nil {
		taskLogger.Warn().Err(err).Msg("task入口心跳失败")
	}

	virtualHost := credentialHost(tk)

	// 沙盒实际安装了什么决定本轮可用的工具（可用 = agent 允许 ∩ 沙盒已安装）。
	installed, err := awaitInventory(ctx, h.Sandbox, sandboxWait, time.Second)
	if err != nil {
		writeCtx, cancel := terminalCtx(ctx)
		defer cancel()
		switch {
		case ctx.Err() != nil:
			// 等沙盒期间停机：与停机中断同样收尾。
			if runErr := h.AgentRuns.SetAborted(writeCtx, p.AgentRunID); runErr != nil {
				taskLogger.Warn().Err(runErr).Msg("中断 run 终态回写失败（由僵尸 run 回收兜底）")
			}
			taskLogger.Info().Msg("等待沙盒期间停机：任务保持进行中，由恢复监控续跑")
			return nil
		case sandbox.Unavailable(err):
			// 等满上限仍不可达：run 落 error、任务保持 active。恢复监控按出错续跑并计入重投
			// 上限——沙盒恢复则续跑，持续不可用则任务在上限后落 aborted，不会无限重投。
			msg := fmt.Sprintf("沙盒 %v 内不可达: %v", sandboxWait, err)
			if runErr := h.AgentRuns.SetError(writeCtx, p.AgentRunID, msg); runErr != nil {
				taskLogger.Warn().Err(runErr).Msg("run 出错终态回写失败（由僵尸 run 回收兜底）")
			}
			taskLogger.Warn().Err(err).Msg("沙盒不可达：本次 run 出错，任务保持进行中，由恢复监控重试")
			return nil
		default:
			// 沙盒拒绝请求（令牌错误等）：配置问题不会自愈，任务以错误结束并说明原因。
			return h.failTask(ctx, taskLogger, p.AgentRunID, taskID, p.ConversationID,
				fmt.Errorf("沙盒拒绝请求，请检查 sandbox.url / sandbox.token 配置: %w", err))
		}
	}
	// run 无论以何种原因结束（收敛、终止、暂停、停机中断、出错），都结束本任务在沙盒里遗留
	// 的进程与浏览器会话：续跑的 run 从探索图恢复，不依赖上一 run 的进程。独立 ctx——任务
	// ctx 此时可能已取消。
	defer h.releaseSandbox(taskLogger, taskID)

	// 执行四Agent认知循环
	report, err := h.runCognition(ctx, taskLogger, tk.AssignmentID, p.AgentRunID, taskID, virtualHost, p.ConversationID, installed)

	exit := classifyRunExit(ctx.Err(), err, report.StopReason)

	// 中断（进程停机 / asynq 租约丢失）：任务没有结束，只是本进程不再跑它。run 落
	// aborted（不计入恢复监控的中断上限——那只数 error run），task 保持 active、
	// 探索图目标保持 open、会话身份不回收；恢复监控的「搁浅任务」规则发现活跃任务
	// 无存活 run 即以新 run 续跑。返回 nil：消息就此了结，不由 asynq 重投。
	if exit == runExitInterrupted {
		writeCtx, cancel := terminalCtx(ctx)
		defer cancel()
		if runErr := h.AgentRuns.SetAborted(writeCtx, p.AgentRunID); runErr != nil {
			taskLogger.Warn().Err(runErr).Str("agent_run_id", p.AgentRunID).Msg("中断 run 终态回写失败（由僵尸 run 回收兜底）")
		}
		taskLogger.Info().Str("agent_run_id", p.AgentRunID).Msg("runner 停机中断：任务保持进行中，由恢复监控续跑")
		return nil
	}

	if exit != runExitPaused {
		// 会话身份回收在收尾落定任务状态之后：任务确实已结束才回收（见 reclaimIfEnded）。
		defer h.reclaimIfEnded(taskLogger, taskID, virtualHost)
	}

	if exit == runExitFailed {
		return h.failTask(ctx, taskLogger, p.AgentRunID, taskID, p.ConversationID, err)
	}

	// agent_run 终态回写：认知报告随 result 落库，审计面不残留 running 脏态。
	// 独立短超时 ctx——任务 ctx 可能已被上游超时收紧。
	doneCtx, doneCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer doneCancel()

	if exit == runExitPaused {
		// 暂停的 run 未抵达目标终点：落 aborted（区别于自然收敛的 done）；
		// task 不迁移、时间线不落节点（「已暂停」节点由 api 的暂停操作落）。
		if runErr := h.AgentRuns.SetAborted(doneCtx, p.AgentRunID); runErr != nil {
			taskLogger.Warn().Err(runErr).Str("agent_run_id", p.AgentRunID).Msg("agent_run 暂停终态回写失败（审计面残留 running）")
		}
		return nil
	}

	// run 终态按停止原因分流：被停止（task_terminated）与超时落 aborted——被杀的 run 标
	// completed 会让审计面误读为自然收敛；认知报告仍随 result 落库（终止时也有 steps/attempts 可读）。
	reportJSON, _ := json.Marshal(report)
	terminated := exit == runExitTerminated || exit == runExitTimedOut
	var runErr error
	if terminated {
		runErr = h.AgentRuns.SetAborted(doneCtx, p.AgentRunID)
	} else {
		runErr = h.AgentRuns.SetCompleted(doneCtx, p.AgentRunID, reportJSON)
	}
	if runErr != nil {
		taskLogger.Warn().Err(runErr).Str("agent_run_id", p.AgentRunID).Msg("agent_run 终态回写失败（审计面残留 running）")
	}

	// task 终态回写：被停止时任务行已由停止方写成终态（及其时间线节点），这里不再碰；
	// 超时由 runner 终止任务；自然收敛才落 completed。
	switch exit {
	case runExitTerminated:
		taskLogger.Info().Msg("任务已被停止：run 收尾不写任务行")
	case runExitTimedOut:
		h.finishTimedOutTask(doneCtx, taskLogger, p, taskID, report)
	default:
		h.finishConvergedTask(doneCtx, taskLogger, p, taskID, report)
	}
	return nil
}

// finishConvergedTask 处理自然收敛（或动作预算用尽）的任务终态、时间线与经验蒸馏。
func (h *Handler) finishConvergedTask(doneCtx context.Context, taskLogger zerolog.Logger, p worker.Payload, taskID string, report cognition.Report) {
	if err := h.completeTask(doneCtx, taskID, report); errors.Is(err, task.ErrStaleConvergence) {
		// 收敛之后目标被调整或又有了待执行动作：本 run 按它的依据收敛了，任务却还有事
		// 要做——保持 active，由恢复监控的「搁浅任务」规则以新 run 续跑。
		taskLogger.Info().Msg("收敛之后任务目标已调整或有了新的待执行动作：任务保持进行中，由恢复监控续跑")
	} else if err != nil {
		taskLogger.Warn().Err(err).Msg("task 完成迁移被拒（保持既有终态）")
	} else {
		// 步数口径与时间线卡片一致：一次 agent 推理=一步（dispatched 后的
		// reasoning 终态数）；动作数（detector 的 Action 完成计数）另列字段。
		h.appendTaskStatus(doneCtx, taskLogger, p.ConversationID,
			conversation.TaskStatusCompleted, "任务完成", h.completionFields(doneCtx, taskLogger, p.ConversationID, taskID, report))

		// 经验蒸馏（仅自然收敛触发；被终止任务的过程性结论可能误导后续任务）：
		// 把复现门验证过的结论蒸馏成跨任务教训写回 corpus。独立短超时 ctx 异步
		// best-effort——教训写入失败不影响任务交付（与 reclaimSessionIdentity
		// 同一惯例）。
		if h.Distiller != nil {
			// #nosec G118 -- 蒸馏有意脱离请求上下文：任务已终态，教训写入应超出 handler 生命周期（独立短超时）。
			go func(taskID string) {
				dctx, cancel := context.WithTimeout(context.Background(), distillTimeout)
				defer cancel()
				if n, err := h.Distiller.DistillTask(dctx, taskID); err != nil {
					taskLogger.Warn().Err(err).Str("task_id", taskID).Msg("任务经验蒸馏失败（corpus 无新教训）")
				} else if n > 0 {
					taskLogger.Info().Str("task_id", taskID).Int("lessons", n).Msg("任务经验已沉淀到知识库")
				}
			}(taskID)
		}
	}
}

// completeTask 按停止原因落 completed：规划收敛以收敛依据为前置（依据失效返回
// task.ErrStaleConvergence）；动作预算用尽则无条件完成——预算是硬上限。
func (h *Handler) completeTask(ctx context.Context, taskID string, report cognition.Report) error {
	if report.StopReason == cognition.StopReasonConverged {
		return h.Tasks.CompleteConverged(ctx, taskID, report.GoalBasis)
	}
	return h.Tasks.Complete(ctx, taskID)
}

// reclaimIfEnded 在 run 收尾落定任务状态之后回收会话身份：只回收确已结束的任务——收敛
// 依据失效（任务保持进行中待续跑）、停止后紧接着在对话里续接（任务已重开）时，续跑的
// run 要复用同一登录态。读不到任务状态时保留（终态清扫器按同一规则兜底）。独立短超时
// ctx（task ctx 可能已取消）。
func (h *Handler) reclaimIfEnded(taskLogger zerolog.Logger, taskID, host string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tk, err := h.Tasks.GetByID(ctx, taskID)
	if err != nil {
		taskLogger.Warn().Err(err).Msg("读取任务状态失败，会话身份暂不回收（终态清扫器兜底）")
		return
	}
	if tk.Status == task.StatusActive {
		return
	}
	h.reclaimSessionIdentity(ctx, taskLogger, taskID, host)
}

// distillTimeout 是任务经验蒸馏的总预算（LLM 生成 + embedding + 写库）。
const distillTimeout = 2 * time.Minute

// finishTimedOutTask 处理 run 超时的任务终态与时间线：task 落 aborted。停止方或 reaper 可能
// 已先行写了终态（CAS 先到者赢），Abort 随之幂等或被拒，此时不重复落节点。
func (h *Handler) finishTimedOutTask(ctx context.Context, taskLogger zerolog.Logger, p worker.Payload, taskID string, report cognition.Report) {
	applied, err := h.Tasks.Abort(ctx, taskID, "任务运行超时（"+report.StopReason+"）")
	if err != nil {
		taskLogger.Warn().Err(err).Msg("task 终止迁移被拒（保持既有终态）")
		return
	}
	if !applied {
		return // 停止方或恢复监控已先行写了终态及其时间线节点
	}
	h.appendTaskStatus(ctx, taskLogger, p.ConversationID,
		conversation.TaskStatusAborted, "任务已终止",
		h.completionFields(ctx, taskLogger, p.ConversationID, taskID, report))
}

// completionFields 汇总终态时间线节点的报告字段（completed/aborted 共用口径）。
func (h *Handler) completionFields(ctx context.Context, taskLogger zerolog.Logger, conversationID, taskID string, report cognition.Report) map[string]any {
	// 步数 = 一次 agent 推理一步（dispatched 后的 reasoning 终态数），与时间线卡片同
	// 口径；动作、结果、假设数来自图统计，各是各的字段，不互相顶替。
	steps := 0
	if conversationID != "" {
		if n, cErr := h.Conversations.CountReasoningSinceDispatched(ctx, conversationID); cErr != nil {
			taskLogger.Warn().Err(cErr).Str("conv", conversationID).Msg("推理步数统计失败（steps 记 0）")
		} else {
			steps = n
		}
	}
	return map[string]any{
		"steps":       steps,
		"actions":     report.Actions,
		"results":     report.Results,
		"hypotheses":  report.Hypotheses,
		"stop_reason": report.StopReason,
		"findings":    h.findingsCount(ctx, taskID),
	}
}

// appendTaskStatus 落任务生命周期节点并广播 SSE（会话任务专用；无会话纯后台
// 扫描跳过）。best-effort——失败仅告警，不影响状态迁移本体。
func (h *Handler) appendTaskStatus(ctx context.Context, taskLogger zerolog.Logger, conversationID, status, summary string, fields map[string]any) {
	appendTaskStatus(ctx, h.Conversations, h.EventPublisher, taskLogger, conversationID, status, summary, fields)
}

// appendTaskStatus 是 runner 内落任务状态节点的唯一实现（run 收尾与恢复监控共用）。
func appendTaskStatus(ctx context.Context, convs *conversation.Store, pub messagePublisher, logger zerolog.Logger,
	conversationID, status, summary string, fields map[string]any) {
	if conversationID == "" || convs == nil {
		return
	}
	msg, err := convs.AppendMessage(ctx, conversationID,
		conversation.RoleAssistant, conversation.KindEvent,
		summary, conversation.TaskStatusMeta(status, fields))
	if err != nil {
		logger.Warn().Err(err).Str("conv", conversationID).Str("status", status).Msg("任务状态节点落库失败")
		return
	}
	if pub != nil {
		if err := pub.PublishMessage(ctx, conversationID, msg); err != nil {
			logger.Warn().Err(err).Str("conv", conversationID).Msg("任务状态节点广播失败（已落库，重连补历史可见）")
		}
	}
}

// findingsCount 数 task 的 finding（completed 节点报告字段；读失败返回 -1 缺省隐藏）。
func (h *Handler) findingsCount(ctx context.Context, taskID string) int {
	if h.Findings == nil {
		return -1
	}
	fs, err := h.Findings.ListByTask(ctx, taskID)
	if err != nil {
		return -1
	}
	return len(fs)
}

// reclaimSessionIdentity 删除本任务的自动会话身份（task:{taskID}:session）。
// best-effort：失败仅告警（终态清扫器会按任务状态兜底回收）；成功落审计。
func (h *Handler) reclaimSessionIdentity(ctx context.Context, taskLogger zerolog.Logger, taskID, host string) {
	if h.Creds == nil || host == "" || taskID == "" {
		return
	}
	name := credential.SessionIdentityName(taskID)
	if err := h.Creds.DeleteIdentity(ctx, host, name); err != nil {
		taskLogger.Warn().Err(err).Str("host", host).
			Msg("会话身份回收失败（终态清扫器将按任务状态兜底）")
		return
	}
	if h.Audit != nil {
		if _, aErr := h.Audit.Append(ctx, audit.Event{
			Actor:      audit.ActorRunner,
			Action:     audit.ActionCredentialDelete,
			TargetKind: "credential",
			TargetID:   credential.CanonicalHost(host),
			Metadata:   json.RawMessage(`{"identity":"` + name + `","scope":"task-terminal"}`),
		}); aErr != nil {
			taskLogger.Warn().Err(aErr).Msg("会话身份回收审计写入失败")
		}
	}
}

// runExit 是一次 run 结束后的收尾分类，决定 agent_run 与 task 各落什么状态。
type runExit int

const (
	runExitCompleted   runExit = iota // 自然收敛：run completed，task completed
	runExitTerminated                 // 任务行已被停止方写成终态（用户停止 / 判死）：run aborted，任务行不动
	runExitTimedOut                   // run 超时：run aborted，task 由 runner 终止
	runExitPaused                     // 用户暂停：run aborted，task 保持 active+paused
	runExitInterrupted                // 进程停机 / 租约丢失：run aborted，task 保持 active，由恢复监控续跑
	runExitFailed                     // 执行出错：failTask（run error，task aborted）
)

// classifyRunExit 按 handler ctx 的结束原因、认知循环错误与停止原因分类。
//
// handler ctx 被取消（context.Canceled）只有两个来源：runner 停机中断在途 run、asynq 租约
// 丢失——两者都是「本进程不再跑它」，不是任务结束。run 超时是 DeadlineExceeded，由 runner 终止
// 任务。用户停止时任务行早已是终态（停止方写入，循环据此退出），收尾不再碰任务行：停止后
// 紧接着续接会重开任务，旧 run 迟到的收尾若再写任务行，会把刚续接的任务又终止一次。
// 暂停优先于中断：两者都不动任务，暂停还要保留 paused 标记交给继续操作。
func classifyRunExit(ctxErr, err error, stopReason string) runExit {
	if err == nil && stopReason == cognition.StopReasonPaused {
		return runExitPaused
	}
	if errors.Is(ctxErr, context.Canceled) {
		return runExitInterrupted
	}
	if err != nil {
		return runExitFailed
	}
	switch {
	case stopReason == cognition.StopReasonTerminated:
		return runExitTerminated
	case errors.Is(ctxErr, context.DeadlineExceeded) || stopReason == cognition.StopReasonCancelled:
		return runExitTimedOut
	}
	return runExitCompleted
}

// releaseSandbox 结束任务在沙盒里遗留的进程与浏览器会话（best-effort，失败只告警：
// 遗留进程不影响任务结局，下次释放同一任务时一并清理）。
func (h *Handler) releaseSandbox(taskLogger zerolog.Logger, taskID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := h.Sandbox.Release(ctx, taskID)
	if err != nil {
		taskLogger.Warn().Err(err).Msg("释放沙盒任务资源失败")
		return
	}
	taskLogger.Info().Int("processes", res.Processes).Int("browsers", res.Browsers).Msg("沙盒任务资源已释放")
}
