package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/cognition"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/task"

	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/worker"
)

// ─────────────────────────────────────────────────────────────
//  任务执行生命周期（终态回写 / 时间线 / 身份回收）
// ─────────────────────────────────────────────────────────────

// executeTask 是单个任务的执行全生命周期：心跳 → 图上线（objective）→ 沙箱获取
// → 驱动认知循环 → 会话身份回收 → agent_run/task 终态回写。
// 所有任务都走同一条链；taskLogger 是 Handle() 在任务作用域派生的 logger
// （task_id/assignment_id/conv 自动附加），本函数与下游（runCognition →
// cognition.Deps）全部经它出日志。
func (h *Handler) executeTask(
	ctx context.Context,
	p worker.Payload,
	brief string,
	taskLogger zerolog.Logger,
	assignmentID string,
) error {
	if brief == "" {
		return h.failTask(ctx, taskLogger, p.AgentRunID, p.TaskID, p.ConversationID, fmt.Errorf("任务缺少 brief"))
	}

	taskID := p.TaskID
	if err := h.Tasks.Heartbeat(ctx, taskID); err != nil {
		taskLogger.Warn().Err(err).Msg("task入口心跳失败")
	}

	virtualHost := h.onboard(ctx, assignmentID, taskID, brief, taskLogger)

	// 沙盒实际安装了什么决定本轮可用的工具（可用 = agent 允许 ∩ 沙盒已安装）。
	installed, err := h.Sandbox.Inventory(ctx)
	if err != nil {
		if ctx.Err() == nil && !sandbox.Unavailable(err) {
			// 沙盒拒绝请求（令牌错误等）：配置问题不会自愈，任务以错误结束并说明原因。
			return h.failTask(ctx, taskLogger, p.AgentRunID, taskID, p.ConversationID,
				fmt.Errorf("沙盒拒绝请求，请检查 sandbox.url / sandbox.token 配置: %w", err))
		}
		// 沙盒暂不可达（启动中、重启中）或本进程停机：与停机中断同样收尾——run 落 aborted、
		// task 保持 active，恢复监控的「搁浅任务」规则在沙盒恢复后以新 run 续跑。
		writeCtx, cancel := terminalCtx(ctx)
		defer cancel()
		if runErr := h.AgentRuns.SetAborted(writeCtx, p.AgentRunID); runErr != nil {
			taskLogger.Warn().Err(runErr).Msg("中断 run 终态回写失败（由僵尸 run 回收兜底）")
		}
		taskLogger.Warn().Err(err).Msg("沙盒暂不可达：任务保持进行中，由恢复监控续跑")
		return nil
	}
	// run 无论以何种原因结束（收敛、终止、暂停、停机中断、出错），都结束本任务在沙盒里遗留
	// 的进程与浏览器会话：续跑的 run 从探索图恢复，不依赖上一 run 的进程。独立 ctx——任务
	// ctx 此时可能已取消。
	defer h.releaseSandbox(taskLogger, taskID)

	// 执行四Agent认知循环
	report, err := h.runCognition(ctx, taskLogger, assignmentID, p.AgentRunID, taskID, virtualHost, brief, p.ConversationID, installed)

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
		// 会话身份回收：任务到此必然终态（正常收敛 / 出错进 failTask），本任务的
		// 自动会话快照（task:{id}:session）随之删除——凭证库无过期机制，生命周期
		// 由事件驱动，这里是主通道；进程崩溃没跑到这里时由终态清扫器按任务状态
		// 兜底。独立短超时 ctx（task ctx 可能已取消）；best-effort + 审计。
		reclaimCtx, reclaimCancel := context.WithTimeout(context.Background(), 5*time.Second)
		h.reclaimSessionIdentity(reclaimCtx, taskLogger, taskID, virtualHost)
		reclaimCancel()
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

	// run 终态按停止原因分流：外部终止（任务行终态 task_terminated / run 超时）落
	// aborted——被杀的 run 标 completed 会让审计面误读为自然收敛；认知报告仍随
	// result 落库（终止时也有 steps/attempts 可读）。
	reportJSON, _ := json.Marshal(report)
	terminated := exit == runExitTerminated
	var runErr error
	if terminated {
		runErr = h.AgentRuns.SetAborted(doneCtx, p.AgentRunID)
	} else {
		runErr = h.AgentRuns.SetCompleted(doneCtx, p.AgentRunID, reportJSON)
	}
	if runErr != nil {
		taskLogger.Warn().Err(runErr).Str("agent_run_id", p.AgentRunID).Msg("agent_run 终态回写失败（审计面残留 running）")
	}

	// task 终态回写：按停止原因与 agent_run 同口径分流——外部终止（任务行已
	// 终态 / run 超时）落 aborted，自然收敛才落 completed。此前无条件
	// Complete：终止路径无人写 aborted，CAS 守卫形同虚设，被杀任务标
	// completed、时间线广播「任务完成」。
	if terminated {
		h.finishTerminatedTask(doneCtx, taskLogger, p, taskID, report)
	} else if err := h.Tasks.Complete(doneCtx, taskID); err != nil {
		taskLogger.Warn().Err(err).Msg("task 完成迁移被拒（保持既有终态）")
	} else {
		// 步数口径与时间线卡片一致：一次 agent 推理=一步（dispatched 后的
		// reasoning 终态数）；动作数（detector 的 Action 完成计数）另列字段。
		h.appendTaskStatus(doneCtx, taskLogger, p.ConversationID,
			conversation.TaskStatusCompleted, "任务完成", h.completionFields(doneCtx, taskLogger, p.ConversationID, taskID, report))

		// 经验蒸馏（仅自然收敛触发；被终止任务的过程性结论可能误导后续任务）：
		// 把复现门验证过的结论蒸馏成跨任务教训写回 corpus。独立短超时 ctx 异步
		// best-effort——教训写入失败不影响任务交付（与 reclaimSessionIdentity
		// 同一惯例；capture brief/taskID 进 goroutine）。
		if h.Distiller != nil {
			// #nosec G118 -- 蒸馏有意脱离请求上下文：任务已终态，教训写入应超出 handler 生命周期（独立短超时）。
			go func(taskID, brief string) {
				dctx, cancel := context.WithTimeout(context.Background(), distillTimeout)
				defer cancel()
				if n, err := h.Distiller.DistillTask(dctx, taskID, brief); err != nil {
					taskLogger.Warn().Err(err).Str("task_id", taskID).Msg("任务经验蒸馏失败（corpus 无新教训）")
				} else if n > 0 {
					taskLogger.Info().Str("task_id", taskID).Int("lessons", n).Msg("任务经验已沉淀到知识库")
				}
			}(taskID, brief)
		}
	}
	return nil
}

// distillTimeout 是任务经验蒸馏的总预算（LLM 生成 + embedding + 写库）。
const distillTimeout = 2 * time.Minute

// finishTerminatedTask 处理被终止任务的 task 终态与时间线：task 落 aborted
// （与 agent_run 的 aborted 分流、objective 的 aborted 终态同一语义）。api 的
// 停止入口（TerminateTask）或 reaper 可能已先行写 aborted + 「任务已停止」节点（CAS 先到者赢）——此时
// 不重复落节点；runner 侧节点只在本次真正完成迁移时写。
func (h *Handler) finishTerminatedTask(ctx context.Context, taskLogger zerolog.Logger, p worker.Payload, taskID string, report executor.Report) {
	tk, gErr := h.Tasks.GetByID(ctx, taskID)
	if gErr != nil {
		taskLogger.Warn().Err(gErr).Msg("终止收尾读取 task 失败（终态交由 reaper 兜底）")
		return
	}
	if tk.Status != task.StatusActive {
		return // api abort / reaper 已落终态及其时间线节点
	}
	if err := h.Tasks.Abort(ctx, taskID, "任务被终止（"+report.StopReason+"）"); err != nil {
		taskLogger.Warn().Err(err).Msg("task 终止迁移被拒（保持既有终态）")
		return
	}
	h.appendTaskStatus(ctx, taskLogger, p.ConversationID,
		conversation.TaskStatusAborted, "任务已终止",
		h.completionFields(ctx, taskLogger, p.ConversationID, taskID, report))
}

// completionFields 汇总终态时间线节点的报告字段（completed/aborted 共用口径）。
func (h *Handler) completionFields(ctx context.Context, taskLogger zerolog.Logger, conversationID, taskID string, report executor.Report) map[string]any {
	// 步数口径与时间线卡片一致：一次 agent 推理=一步（dispatched 后的
	// reasoning 终态数）；动作数（detector 的 Action 完成计数）另列字段。
	reasoningSteps := 0
	if conversationID != "" {
		if n, cErr := h.Conversations.CountReasoningSinceDispatched(ctx, conversationID); cErr != nil {
			taskLogger.Warn().Err(cErr).Str("conv", conversationID).Msg("推理步数统计失败（steps 回退动作数口径）")
		} else {
			reasoningSteps = n
		}
	}
	stepsField := reasoningSteps
	if stepsField == 0 {
		stepsField = report.Steps
	}
	return map[string]any{
		"steps":       stepsField,
		"actions":     report.Steps,
		"promoted":    report.Promoted,
		"attempts":    report.Attempts,
		"stop_reason": report.StopReason,
		"findings":    h.findingsCount(ctx, taskID),
	}
}

// appendTaskStatus 落任务生命周期节点并广播 SSE（会话任务专用；无会话纯后台
// 扫描跳过）。best-effort——失败仅告警，不影响状态迁移本体。
func (h *Handler) appendTaskStatus(ctx context.Context, taskLogger zerolog.Logger, conversationID, status, summary string, fields map[string]any) {
	if conversationID == "" {
		return
	}
	msg, err := h.Conversations.AppendMessage(ctx, conversationID,
		conversation.RoleAssistant, conversation.KindEvent,
		summary, conversation.TaskStatusMeta(status, fields))
	if err != nil {
		taskLogger.Warn().Err(err).Str("conv", conversationID).Str("status", status).Msg("任务状态节点落库失败")
		return
	}
	_ = h.EventPublisher.PublishMessage(ctx, conversationID, msg)
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
	runExitTerminated                 // 外部终止（任务行已终态 / run 超时）：run 与 task 都 aborted
	runExitPaused                     // 用户暂停：run aborted，task 保持 active+paused
	runExitInterrupted                // 进程停机 / 租约丢失：run aborted，task 保持 active，由恢复监控续跑
	runExitFailed                     // 执行出错：failTask（run error，task aborted）
)

// classifyRunExit 按 handler ctx 的结束原因、认知循环错误与停止原因分类。
//
// handler ctx 被取消（context.Canceled）只有两个来源：runner 停机中断在途 run、asynq 租约
// 丢失——两者都是「本进程不再跑它」，不是任务结束。run 超时是 DeadlineExceeded，按外部终止处理。
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
	if stopReason == cognition.StopReasonTerminated || strings.Contains(stopReason, "cancel") {
		return runExitTerminated
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
	taskLogger.Info().Int("process_groups", res.ProcessGroups).Int("browsers", res.Browsers).Msg("沙盒任务资源已释放")
}
