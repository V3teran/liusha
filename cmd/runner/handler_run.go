package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/task"

	"github.com/V3teran/liusha/internal/worker"
)

// ─────────────────────────────────────────────────────────────
//  Tool-invocation recorder (heartbeat + DB telemetry)
// ─────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────
//  Cognition handler (新架构统一入口)
// ─────────────────────────────────────────────────────────────

// handleCognition 统一的任务执行入口（新架构）。
// 所有任务都走：Planner（6分钟评估） + Executor（5步评估）。
// taskLogger 是 handle() 在任务作用域派生的 logger（task_id/assignment_id/conv
// 自动附加），本函数与下游（runCognition → cognition.Deps）全部经它出日志。
func (h handler) handleCognition(
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
	if err := h.tasks.Heartbeat(ctx, taskID); err != nil {
		taskLogger.Warn().Err(err).Msg("task入口心跳失败")
	}

	virtualHost := h.onboard(ctx, assignmentID, taskID, brief, taskLogger)

	// 获取 Sandbox（单例模式：所有任务共享一个容器，通过工作目录隔离）
	sb, err := h.sandboxMgr.Acquire(ctx, sandbox.AcquireRequest{
		TaskID: taskID,
	})
	if err != nil {
		return h.failTask(ctx, taskLogger, p.AgentRunID, taskID, p.ConversationID, fmt.Errorf("sandboxMgr.Acquire(task=%s): %w", taskID, err))
	}
	defer func() {
		if err := h.sandboxMgr.Release(context.Background(), sb); err != nil {
			taskLogger.Warn().Err(err).Msg("sandboxMgr.Release 失败")
		}
	}()

	taskLogger.Info().
		Str("sandbox_id", sb.ID).
		Str("work_dir", sb.WorkDir).
		Msg("sandbox acquired for task")

	// 执行四Agent认知循环
	report, err := h.runCognition(ctx, taskLogger, taskID, virtualHost, brief, p.ConversationID, sb.Client)

	// 会话身份回收：任务到此必然终态（正常收敛 / 出错进 failTask），本任务的
	// 自动会话快照（task:{id}:session）随之删除——凭证库无过期机制，生命周期
	// 由事件驱动，这里是主通道；进程崩溃没跑到这里时由终态清扫器按任务状态
	// 兜底。独立短超时 ctx（task ctx 可能已取消）；best-effort + 审计。
	reclaimCtx, reclaimCancel := context.WithTimeout(context.Background(), 5*time.Second)
	h.reclaimSessionIdentity(reclaimCtx, taskLogger, taskID, virtualHost)
	reclaimCancel()

	if err != nil {
		return h.failTask(ctx, taskLogger, p.AgentRunID, taskID, p.ConversationID, err)
	}

	// agent_run 终态回写（成功路径）：认知报告随 result 落库，审计面不再残留
	// running 脏态。独立短超时 ctx——任务 ctx 可能已被上游超时收紧。
	doneCtx, doneCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer doneCancel()
	// run 终态按停止原因分流：外部终止（控制平面 terminate / ctx 取消）落
	// aborted——被杀的 run 标 done 会让审计面误读为自然收敛；认知报告仍随
	// result 落库（终止时也有 steps/attempts 可读）。
	reportJSON, _ := json.Marshal(report)
	terminated := strings.Contains(report.StopWhy, "terminate") || strings.Contains(report.StopWhy, "cancel")
	var runErr error
	if terminated {
		runErr = h.agentRuns.SetAborted(doneCtx, p.AgentRunID)
	} else {
		runErr = h.agentRuns.SetDone(doneCtx, p.AgentRunID, reportJSON)
	}
	if runErr != nil {
		taskLogger.Warn().Err(runErr).Str("agent_run_id", p.AgentRunID).Msg("agent_run 终态回写失败（审计面残留 running）")
	}

	// task 终态回写：按停止原因与 agent_run 同口径分流——被终止（控制平面
	// terminate / ctx 取消）落 aborted，自然收敛才落 completed。此前无条件
	// Complete：terminate 路径无人写 aborted（api abort 端点只是其中一条来
	// 路），CAS 守卫形同虚设，被杀任务标 completed、时间线广播「扫描完成」。
	if terminated {
		h.finishTerminatedTask(doneCtx, taskLogger, p, taskID, report)
	} else if err := h.tasks.Complete(doneCtx, taskID); err != nil {
		taskLogger.Warn().Err(err).Msg("task 完成迁移被拒（保持既有终态）")
	} else {
		// 步数口径与时间线卡片一致：一次 agent 推理=一步（dispatched 后的
		// reasoning 终态数）；动作数（detector 的 Action 完成计数）另列字段。
		h.appendTaskStatus(doneCtx, taskLogger, p.ConversationID,
			conversation.TaskStatusCompleted, "扫描完成", h.completionFields(doneCtx, taskLogger, p.ConversationID, taskID, report))
	}
	return nil
}

// finishTerminatedTask 处理被终止任务的 task 终态与时间线：task 落 aborted
// （与 agent_run 的 aborted 分流、objective 的 aborted 终态同一语义）。api 的
// abort 端点可能已先行写 aborted + 「扫描已停止」节点（CAS 先到者赢）——此时
// 不重复落节点；runner 侧节点只在本次真正完成迁移时写。
func (h handler) finishTerminatedTask(ctx context.Context, taskLogger zerolog.Logger, p worker.Payload, taskID string, report executor.Report) {
	tk, gErr := h.tasks.GetByID(ctx, taskID)
	if gErr != nil {
		taskLogger.Warn().Err(gErr).Msg("终止收尾读取 task 失败（终态交由 reaper 兜底）")
		return
	}
	if tk.Status != task.StatusActive {
		return // api abort / reaper 已落终态及其时间线节点
	}
	if err := h.tasks.Abort(ctx, taskID, "扫描被终止（"+report.StopWhy+"）"); err != nil {
		taskLogger.Warn().Err(err).Msg("task 终止迁移被拒（保持既有终态）")
		return
	}
	h.appendTaskStatus(ctx, taskLogger, p.ConversationID,
		conversation.TaskStatusAborted, "扫描已终止",
		h.completionFields(ctx, taskLogger, p.ConversationID, taskID, report))
}

// completionFields 汇总终态时间线节点的报告字段（completed/aborted 共用口径）。
func (h handler) completionFields(ctx context.Context, taskLogger zerolog.Logger, conversationID, taskID string, report executor.Report) map[string]any {
	// 步数口径与时间线卡片一致：一次 agent 推理=一步（dispatched 后的
	// reasoning 终态数）；动作数（detector 的 Action 完成计数）另列字段。
	reasoningSteps := 0
	if conversationID != "" {
		if n, cErr := h.conversations.CountReasoningSinceDispatched(ctx, conversationID); cErr != nil {
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
		"steps":    stepsField,
		"actions":  report.Steps,
		"promoted": report.Promoted,
		"attempts": report.Attempts,
		"stop_why": report.StopWhy,
		"findings": h.findingsCount(ctx, taskID),
	}
}

// appendTaskStatus 落任务生命周期节点并广播 SSE（会话任务专用；无会话纯后台
// 扫描跳过）。best-effort——失败仅告警，不影响状态迁移本体。
func (h handler) appendTaskStatus(ctx context.Context, taskLogger zerolog.Logger, convID, status, summary string, fields map[string]any) {
	if convID == "" {
		return
	}
	msg, err := h.conversations.AppendMessage(ctx, convID,
		conversation.RoleAssistant, conversation.KindEvent,
		summary, conversation.TaskStatusMeta(status, fields))
	if err != nil {
		taskLogger.Warn().Err(err).Str("conv", convID).Str("status", status).Msg("任务状态节点落库失败")
		return
	}
	_ = h.eventPublisher.PublishMessage(ctx, convID, msg)
}

// findingsCount 数 task 黑板 finding（completed 节点报告字段；读失败返回 -1 缺省隐藏）。
func (h handler) findingsCount(ctx context.Context, taskID string) int {
	if h.findings == nil {
		return -1
	}
	fs, err := h.findings.ListByTask(ctx, taskID)
	if err != nil {
		return -1
	}
	return len(fs)
}

// reclaimSessionIdentity 删除本任务的自动会话身份（task:{taskID}:session）。
// best-effort：失败仅告警（终态清扫器会按任务状态兜底回收）；成功落审计。
func (h handler) reclaimSessionIdentity(ctx context.Context, taskLogger zerolog.Logger, taskID, host string) {
	if h.creds == nil || host == "" || taskID == "" {
		return
	}
	name := credential.SessionIdentityName(taskID)
	if err := h.creds.DeleteIdentity(ctx, host, name); err != nil {
		taskLogger.Warn().Err(err).Str("host", host).
			Msg("会话身份回收失败（终态清扫器将按任务状态兜底）")
		return
	}
	if h.audit != nil {
		if _, aErr := h.audit.Append(ctx, audit.Event{
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
