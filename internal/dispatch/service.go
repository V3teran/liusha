// Package dispatch 实现平台的下发域：把用户意图（单发扫描 / 任务批次 / 定时模板 /
// 会话消息）展开成 assignment → task → agent run，并入 asynq 队列交付 runner 执行；
// 同时承担会话内消息的意图分流（action → 下发/续接，qa/chat → 回答）。
//
// 依赖方向：dispatch → httpapi（实现其窄接口）→ 各 store；httpapi 不感知本包。
package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/attachment"
	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/chat"
	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/convstream"
	"github.com/V3teran/liusha/internal/cronschedule"
	"github.com/V3teran/liusha/internal/finding"
	fwllm "github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/intent"
	llmconfig "github.com/V3teran/liusha/internal/llmconfig"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/qa"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/textx"
	"github.com/V3teran/liusha/internal/worker"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// auditLog 专记审计写入的 best-effort 失败——审计是安全/合规轨迹，
// 失败不阻塞业务但必须留可见日志（不能完全静默吞掉）。
var auditLog = logx.New("api.audit")

// dispatchLog 记任务下发的结果踪迹——下发是平台首要写操作，此前除审计表外
// 零日志（Service 无 logger），排障「这批发出去没有」只能查库。
var dispatchLog = logx.New("api.dispatch")

// chatLog 记纯聊天回答的 best-effort 失败——会话与用户消息已落库，回答缺失不阻断会话创建。
var chatLog = logx.New("api.chat")

// TaskStatusAdapter 把 task store 适配到 httpapi.TaskStatusResolver：
// 凭证详情里每个会话身份标注归属任务的当前状态。
type TaskStatusAdapter struct {
	Tasks *task.Store
}

// TaskStatus 返回任务状态与存在性（行不存在 = 已级联删除）。
func (a TaskStatusAdapter) TaskStatus(ctx context.Context, taskID string) (string, bool) {
	t, err := a.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return "", false
	}
	return string(t.Status), true
}

// EventStreamAdapter 把 convstream 订阅适配成 httpapi.EventStream（SSE handler 用）。
// convstream.Subscription 自带 Events()/Close()，满足 httpapi.EventSubscription。
type EventStreamAdapter struct{ Rdb *redis.Client }

// Subscribe 满足 httpapi.EventStream：按会话订阅 convstream 的 SSE 事件流。
func (e EventStreamAdapter) Subscribe(ctx context.Context, conversationID string) httpapi.EventSubscription {
	return convstream.Subscribe(ctx, e.Rdb, conversationID)
}

// Service 是下发域的一站式服务：建 scan → 建 agent run → 入 asynq 队列；
// 并实现 httpapi 的会话面窄接口（Chat/FollowUp/Abort/Deleter/TaskDispatch）
// 与 chat/qa 的 Deps（意图分流后的回答与问答）。
//
// 任一步失败都不留中间状态（前面失败直接返错；task 已建但 enqueue 失败会留
// scan，由用户手动 abort 或后续 sweeper——保持简单不上事务，与
// ingestor.enqueueMain 一致语义）。
// 合表后：task.Store 管扫描生命周期，agentrun.Store 建 run。
type Service struct {
	Assignments   *assignment.Store
	Tasks         *task.Store
	AgentRuns     *agentrun.Store
	Enq           *worker.Client
	Audit         *audit.Store        // 0047：create 写审计事件；nil 跳过
	Conversations *conversation.Store // 阶段B：StartChatTask 建会话；nil 时仅 CreateScan 可用
	Schedules     *cronschedule.Store // 任务模块「定时执行」：DispatchScheduled 建模板

	Router    *RoleRouter           // 多轮：意图分类 + 问答（light provider，role→档→framework Router）
	Findings  *finding.Store        // 问答读 task 黑板 finding
	Publisher *convstream.Publisher // 问答回答 publish SSE
	Control   *controlplane.Store   // 用户停止 → terminate 控制事件（停掉 runner 执行）

	// Attachments 是附件域窄接口（*attachment.Store 满足；nil = 附件能力关闭，
	// 引用按空处理）。职责：下发收口点把暂存件搬进任务 inbox + 对话回答内联小文本。
	Attachments AttachmentStore

	// Run 整体超时上限。入队时设为 asynq.Timeout，
	// 否则 asynq 默认 30min 任务 deadline 会架空 runner handler 里 4h 的 WithTimeout——
	// run 跑到 30min 就被 ctx cancel（实测 30min 整 abort、planner 没机会收尾）。
	MaxRunTimeout time.Duration
}

// AttachmentStore 是 dispatch 对附件域的最小依赖（internal/attachment.Store 满足）。
// 接口化是为了测试替身与「附件能力可整体缺席」——store 未装配时消息引用按空处理。
type AttachmentStore interface {
	SaveUpload(dest attachment.Dest, r io.Reader, filename string, sizeCap int64) (attachment.Ref, error)
	Materialize(dest attachment.Dest, refs []attachment.Ref) ([]attachment.Ref, error)
	RemoveStaging(conversationID string) error
	InlineTexts(dest attachment.Dest, refs []attachment.Ref) (string, error)
}

// noWallClockLimit 表达「run 不设墙钟上限」。asynq 无法直接表达无限时：
// Timeout(0) 与「未设置」同形（noTimeout 哨兵就是 0），会被落回默认 30 分钟
// 截断。持续探索模式的真实上限是完成检测器（max_steps），墙钟只是可选护栏
// ——配置为 0 时用远超任何合法 run 时长的哨兵值顶住 asynq 默认。
const noWallClockLimit = 365 * 24 * time.Hour

func wallClockOption(d time.Duration) time.Duration {
	if d <= 0 {
		return noWallClockLimit
	}
	return d
}

// createTask 是建任务的核心：建 assignment + task + agent run + 入 asynq 队列（带
// conversationID）。CreateScan（无会话纯后台）与 StartChatTask（会话发起）共用。
// 会话发起时随入队成功落 dispatched 状态节点（时间线任务边界：何时下发）。
func (a *Service) createTask(ctx context.Context, brief, conversationID string, attachments []attachment.Ref, userMsgSeq int64) (string, string, error) {
	if err := validateBrief(brief); err != nil {
		return "", "", err
	}
	// 一切下发皆走 assignment：单发 = 单元素 assignment(manual) → 1 task。
	asg, err := a.Assignments.Create(ctx, assignment.NewParams{
		Source: assignment.SourceSelf,
		Items:  []assignment.Item{{Brief: brief}},
		Title:  BriefTitle(brief),
	})
	if err != nil {
		return "", "", fmt.Errorf("create assignment: %w", err)
	}
	taskID, runID, err := a.expandItem(ctx, asg.ID, brief, conversationID, attachments, userMsgSeq)
	if err != nil {
		return "", "", err
	}
	if conversationID != "" {
		a.appendTaskStatus(ctx, conversationID, conversation.TaskStatusDispatched,
			"已下发扫描，agent 启动中…", nil)
	}
	return taskID, runID, nil
}

// DispatchBatch 满足 httpapi.TaskDispatchAPI：任务模块「新建任务」弹层的立即下发。
//
// 1 assignment(manual, N items) → N task → N conversation（1:1）：对每个 brief
// 复用 StartChatTask 的会话语义（建会话 + 落用户 brief + dispatched 时间线节点 +
// 异步生成标题），展开走同一份 expandItem——批次内任务互不串扰，各绑独立对话。
func (a *Service) DispatchBatch(ctx context.Context, title, description string, briefs []string) (string, []httpapi.DispatchedTask, error) {
	items := make([]assignment.Item, 0, len(briefs))
	for _, b := range briefs {
		items = append(items, assignment.Item{Brief: b})
	}
	asg, err := a.Assignments.Create(ctx, assignment.NewParams{
		Source:      assignment.SourceSelf,
		Items:       items,
		Title:       title,
		Description: description,
	})
	if err != nil {
		return "", nil, fmt.Errorf("create assignment: %w", err)
	}
	dispatchLog.Info().Str("assignment_id", asg.ID).Int("task_count", len(briefs)).
		Msg("任务批次下发开始")

	out := make([]httpapi.DispatchedTask, 0, len(briefs))
	for _, b := range briefs {
		created, err := a.Conversations.CreateConversation(ctx, BriefTitle(b), "")
		if err != nil {
			return "", out, fmt.Errorf("create conversation: %w", err)
		}
		if _, err := a.Conversations.AppendMessage(ctx, created.ID, conversation.RoleUser, conversation.KindMessage, b, nil); err != nil {
			return asg.ID, out, fmt.Errorf("append user message: %w", err)
		}
		taskID, _, err := a.expandItem(ctx, asg.ID, b, created.ID, nil, 0)
		if err != nil {
			return asg.ID, out, err
		}
		if err := a.Conversations.LinkTask(ctx, created.ID, taskID); err != nil {
			chatLog.Warn().Err(err).Str("conversation_id", created.ID).Msg("link task 失败")
		}
		a.appendTaskStatus(ctx, created.ID, conversation.TaskStatusDispatched, "已下发扫描，agent 启动中…", nil)
		go a.genTitle(created.ID, b) //nolint:gosec // G118：标题生成独立生命周期
		out = append(out, httpapi.DispatchedTask{TaskID: taskID, ConversationID: created.ID})
		dispatchLog.Info().
			Str("assignment_id", asg.ID).
			Str("task_id", taskID).
			Str("conversation_id", created.ID).
			Msg("任务已展开入队")
	}
	return asg.ID, out, nil
}

// DispatchScheduled 满足 httpapi.TaskDispatchAPI：定时下发——建 cron_schedule 模板
// （含批次名/描述与目标清单），不立即执行；到点由 Scheduler 克隆 assignment 展开
// （见 Scheduler.fireOne）。cron 表达式当场校验（cronschedule.NextRun）。
func (a *Service) DispatchScheduled(ctx context.Context, title, description string, briefs []string, cronExpr string) (string, time.Time, error) {
	items := make([]assignment.Item, 0, len(briefs))
	for _, b := range briefs {
		items = append(items, assignment.Item{Brief: b})
	}
	sched, err := a.Schedules.Create(ctx, cronschedule.NewParams{
		CronExpr:    cronExpr,
		Items:       items,
		Title:       title,
		Description: description,
	})
	if err != nil {
		return "", time.Time{}, err
	}
	next := time.Time{}
	if sched.NextRunAt != nil {
		next = *sched.NextRunAt
	}
	dispatchLog.Info().
		Str("schedule_id", sched.ID).
		Int("task_count", len(briefs)).
		Time("next_run_at", next).
		Msg("定时下发模板已创建")
	return sched.ID, next, nil
}

// appendTaskStatus 落一条任务生命周期状态节点并广播 SSE（迁移写者共用；
// best-effort——落库失败仅告警，不阻断状态迁移本体）。
func (a *Service) appendTaskStatus(ctx context.Context, conversationID, status, summary string, fields map[string]any) {
	msg, err := a.Conversations.AppendMessage(ctx, conversationID,
		conversation.RoleAssistant, conversation.KindEvent,
		summary, conversation.TaskStatusMeta(status, fields))
	if err != nil {
		chatLog.Warn().Err(err).Str("conversation_id", conversationID).Str("status", status).Msg("任务状态节点落库失败")
		return
	}
	_ = a.Publisher.PublishMessage(ctx, conversationID, msg)
}

// expandItem 把 assignment 下的一个 item（brief）展开成 task + agent run + enqueue。
// 单发（createTask，建单元素 assignment 后展开 1 条）与批量/定时（Scheduler，建多元素
// assignment 后逐条展开）复用同一份展开逻辑，只是 assignment 的建法不同（单发 vs 批量/cron）。
//
// target_host 留空——不在 API 层 parse brief，runner 入口从 brief 抽取后回填（派生列）。
//
// attachments 是随消息提交的附件引用（可空）：task 落位后整批搬进 inbox，清单
// （沙盒绝对路径）拼进 payload brief——agent 从 prompt 直接拿到可读路径；task.brief
// 保持原文（清单是路由信息，不是目标描述，进 brief 会污染标题/批次名）。
// userMsgSeq 是携带这批附件的用户消息 seq（0 = 无会话消息）：落位后据它改写
// 消息 metadata 的引用路径（暂存 → inbox），时间线芯片随之指向文件的新家。
func (a *Service) expandItem(ctx context.Context, assignmentID, brief, conversationID string, attachments []attachment.Ref, userMsgSeq int64) (string, string, error) {
	tk, err := a.Tasks.Create(ctx, task.NewParams{
		AssignmentID: assignmentID,
		Brief:        brief,
	})
	if err != nil {
		return "", "", fmt.Errorf("create task: %w", err)
	}

	payloadBrief := brief
	if len(attachments) > 0 {
		materialized, err := a.materialize(conversationID, assignmentID, tk.ID, attachments)
		if err != nil {
			return "", "", err
		}
		payloadBrief += attachment.Manifest(materialized)
		a.relinkMessageAttachments(ctx, conversationID, userMsgSeq, materialized)
	}

	// payload 只装 brief 原文——目标 URL / host 由 runner 从 brief 自识别回填。
	payloadInput, err := json.Marshal(map[string]string{"brief": payloadBrief})
	if err != nil {
		return "", "", fmt.Errorf("marshal payload: %w", err)
	}
	tid, err := a.enqueuePlannerRun(ctx, tk.ID, conversationID, payloadInput)
	if err != nil {
		return "", "", err
	}

	// 0047：task 创建成功 → 审计事件。metadata 记 brief 前 200 字便于事后查（完整 brief 在 task.brief 列）。
	if a.Audit != nil {
		briefPreview := brief
		if len(briefPreview) > 200 {
			briefPreview = briefPreview[:200]
		}
		meta, _ := json.Marshal(map[string]string{"brief_preview": briefPreview, "agent_run_id": tid})
		// 操作者由认证层写入 request context（HTTP 下发 → user:名）；cron 等系统
		// 路径无操作者 → system。如实区分"谁"发起，而非统一静态标签。
		actor := audit.ActorSystem
		if a, ok := audit.ActorFromContext(ctx); ok {
			actor = a
		}
		if _, err := a.Audit.Append(ctx, audit.Event{
			Actor:      actor,
			Action:     audit.ActionTaskCreate,
			TargetKind: "task",
			TargetID:   tk.ID,
			Metadata:   meta,
		}); err != nil {
			// best-effort：审计失败不阻塞业务返回，但记 Warn 留可见痕迹
			auditLog.Warn().Err(err).Str("action", string(audit.ActionTaskCreate)).Str("target", tk.ID).Msg("task 创建审计写入失败（不阻塞业务）")
		}
	}

	return tk.ID, tid, nil
}

// FollowUp 在已有 task 上发起一次续接 run（多轮动作）：重开 task + 建 agent run + 入队
// （brief=追加消息）。复用 task 作用域黑板——新 run 经 BuildUserPrompt 看到先前 finding。
// 入队 Payload 与 createTask 同构，仅 TaskID 复用传入 taskID、不新建 task。
// attachments（可空）经 Materialize 收口进该任务 inbox，清单拼进本轮 payload brief，
// userMsgSeq 同 createTask（落位后改写消息引用路径）。
func (a *Service) FollowUp(ctx context.Context, taskID, conversationID, brief string, attachments []attachment.Ref, userMsgSeq int64) (string, error) {
	if err := validateBrief(brief); err != nil {
		return "", err
	}
	tk, err := a.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return "", fmt.Errorf("read task: %w", err)
	}
	payloadBrief := brief
	if len(attachments) > 0 {
		materialized, err := a.materialize(conversationID, tk.AssignmentID, taskID, attachments)
		if err != nil {
			return "", err
		}
		payloadBrief += attachment.Manifest(materialized)
		a.relinkMessageAttachments(ctx, conversationID, userMsgSeq, materialized)
	}
	if err := a.Tasks.Reopen(ctx, taskID); err != nil {
		return "", fmt.Errorf("reopen task: %w", err)
	}
	// 清上轮生命周期的滞留内容事件：控制消费者终态退出时已清，这里兜底
	// 消费者已死的崩溃场景——内容事件只属于一个生命周期，不得被本轮迟到消费。
	if a.Control != nil {
		if n, err := a.Control.ResolvePending(ctx, taskID); err != nil {
			chatLog.Warn().Err(err).Str("task", taskID).Msg("清理滞留控制事件失败（续接 run 有迟到消费的风险）")
		} else if n > 0 {
			chatLog.Info().Int("resolved", n).Str("task", taskID).Msg("已清理上轮滞留的控制事件")
		}
	}
	payloadInput, err := json.Marshal(map[string]string{"brief": payloadBrief})
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	runID, err := a.enqueuePlannerRun(ctx, taskID, conversationID, payloadInput)
	if err != nil {
		if !errors.Is(err, agentrun.ErrLiveRunExists) {
			return "", err
		}
		// 与恢复监控的同 tick 重投竞争（毫秒级窗口）：先建者赢，复用存活
		// run——任务已被重拉起，但用户的续接指令未进本轮 brief（原 brief 续跑）。
		chatLog.Warn().Str("task", taskID).Str("conversation_id", conversationID).
			Msg("续接与并发重投竞争，复用已存活 run（本轮指令未并入 brief）")
		if latest, lErr := a.AgentRuns.LatestByTask(ctx, taskID); lErr == nil {
			return latest.ID, nil
		}
		return "", err
	}
	// 续接同款下发边界：时间线标记新一轮 run 启动（与首发的 dispatched 同形）。
	if conversationID != "" {
		a.appendTaskStatus(ctx, conversationID, conversation.TaskStatusDispatched,
			"已续接扫描，agent 启动中…", nil)
	}
	return runID, nil
}

// enqueuePlannerRun 建 planner agent run 并入队——expandItem（首跑）与 FollowUp（续跑）
// 共用同一份入队语义。
//
// 任务跑 ~4h，asynq 默认 retry 25 次 → 4 天死循环；且 retry 接管的新 runner 进程没有
// 原进程的认知循环状态（bus/共享沙箱），重跑只会产出脏状态。
// MaxRetry(0)：跑挂就跑挂，让用户手动 abort + 重新触发，不重试。
func (a *Service) enqueuePlannerRun(ctx context.Context, taskID, conversationID string, payloadInput []byte) (string, error) {
	tid, err := a.AgentRuns.Create(ctx, agentrun.NewParams{
		TaskID: taskID,
		Role:   "planner",
		Input:  payloadInput,
	})
	if err != nil {
		return "", fmt.Errorf("create agent run: %w", err)
	}
	if _, _, err := a.Enq.Enqueue(ctx, worker.RoleAgent, worker.Payload{
		AgentRunID:     tid,
		TaskID:         taskID,
		ConversationID: conversationID, // 会话发起时非空 → runner 发过程事件
		// 场景 code：runner 据此数据驱动派发引擎/操作员编排
		Input:    payloadInput,
		TaskRole: worker.RoleAgent,
	}, asynq.MaxRetry(0), asynq.Timeout(wallClockOption(a.MaxRunTimeout))); err != nil {
		return "", fmt.Errorf("enqueue: %w", err)
	}
	return tid, nil
}

// PauseTask 把进行中的任务置为暂停中（task.paused CAS，同步生效——列表即刻
// 翻转）。运行中的认知循环在下一个控制轮询周期（≤5s）观察到标记：停 agents、
// 以 user_paused 结束本 run。任务行保持 active，探索图/工作区/会话身份原样
// 保留，worker 槽位即刻释放。已暂停的任务幂等返回（不重复落时间线）。
func (a *Service) PauseTask(ctx context.Context, taskID string) error {
	tk, err := a.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return err
	}
	if tk.Status != task.StatusActive {
		return task.ErrNotActive
	}
	changed, err := a.Tasks.MarkPaused(ctx, taskID)
	if err != nil {
		return fmt.Errorf("dispatch: pause task: %w", err)
	}
	if !changed {
		return nil
	}
	if convID, rErr := a.Conversations.ResolveConversationByTask(ctx, taskID); rErr == nil && convID != "" {
		a.appendTaskStatus(ctx, convID, conversation.TaskStatusPaused,
			"扫描已暂停（进度保留）", map[string]any{"reason": "用户暂停（任务行）"})
	}
	return nil
}

// ResumeTask 解除暂停并恢复执行：CAS 清除 task.paused 后，若上一 run 已因暂停
// 退出（无 pending/running run）则重投全新 planner run——认知循环从探索图历史
// 续跑（与断点恢复同一形态，不做原地解冻）。暂停尚未传导到循环（run 仍在跑）
// 时不重投：循环下一轮看到标记已清除，原地继续，天然避免双 run 并发。
// 未暂停的任务幂等返回。
func (a *Service) ResumeTask(ctx context.Context, taskID string) error {
	tk, err := a.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return err
	}
	if tk.Status != task.StatusActive {
		return task.ErrNotActive
	}
	if !tk.Paused {
		return nil
	}
	if changed, err := a.Tasks.MarkResumed(ctx, taskID); err != nil {
		return fmt.Errorf("dispatch: resume task: %w", err)
	} else if !changed {
		return nil // 并发的继续请求已处理
	}
	if latest, lErr := a.AgentRuns.LatestByTask(ctx, taskID); lErr == nil {
		if latest.Status == agentrun.StatusPending || latest.Status == agentrun.StatusRunning {
			return nil // 上一 run 还活着（暂停未传导）：它看到标记清除后原地继续
		}
	} else if !errors.Is(lErr, agentrun.ErrNoRun) {
		return fmt.Errorf("dispatch: resume task: %w", lErr)
	}
	conversationID := ""
	if id, rErr := a.Conversations.ResolveConversationByTask(ctx, taskID); rErr == nil {
		conversationID = id
	}
	input, _ := json.Marshal(map[string]string{"brief": tk.Brief})
	if _, err := a.enqueuePlannerRun(ctx, taskID, conversationID, input); err != nil {
		if errors.Is(err, agentrun.ErrLiveRunExists) {
			// 与恢复监控的同 tick 重投竞争（毫秒级窗口）：任务已被并发生产者
			// 拉起，「继续」的目的已达成，幂等返回。
			dispatchLog.Info().Str("task", taskID).Msg("继续与并发重投竞争，复用已存活 run")
			return nil
		}
		return fmt.Errorf("dispatch: resume task: %w", err)
	}
	if conversationID != "" {
		a.appendTaskStatus(ctx, conversationID, conversation.TaskStatusResumed, "扫描已继续", nil)
	}
	return nil
}

// TerminateTask 按 taskID 停止：Abort 终态 + 会话时间线通知（与 AbortConversationTask
// 同链路，入口从会话换成任务）。运行中的循环由任务行观察停摆（≤一个控制轮询
// 周期）——行状态与 API 响应同事务生效，没有事件投递失败的停摆缺口。
func (a *Service) TerminateTask(ctx context.Context, taskID string) error {
	if err := a.Tasks.Abort(ctx, taskID, "用户停止（任务行）"); err != nil {
		return err
	}
	if convID, err := a.Conversations.ResolveConversationByTask(ctx, taskID); err == nil && convID != "" {
		a.appendTaskStatus(ctx, convID, conversation.TaskStatusAborted,
			"扫描已停止", map[string]any{"reason": "用户停止（任务行）"})
	}
	return nil
}

// AbortConversationTask 满足 httpapi.AbortAPI：abort 会话关联的 task。
//
// 两件事一体（缺一则「停止」名不副实）：
//  1. task 置 aborted（DB 终态，幂等/守卫见 task.Store.Abort）；
//  2. 时间线落 aborted 节点（用户可见的停止边界）。
//
// 运行中的认知循环按任务行观察退出（控制消费者每周期读行，终态即停 agents
// 结束 run）——生命周期信号走任务行而非事件队列。
func (a *Service) AbortConversationTask(ctx context.Context, conversationID string) error {
	fetched, err := a.Conversations.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if fetched.TaskID == "" {
		return fmt.Errorf("conversation 无关联 task")
	}
	if err := a.Tasks.Abort(ctx, fetched.TaskID, "用户停止"); err != nil {
		return err
	}
	a.appendTaskStatus(ctx, conversationID, conversation.TaskStatusAborted,
		"扫描已停止", map[string]any{"reason": "用户停止"})
	return nil
}

// DeleteConversation 满足 httpapi.ConversationDeleter：删会话+消息，但关联 task 仍在跑时拒删。
// 「先停后删」的服务端把关——返回 httpapi.ErrConversationTaskActive → handler 映射 409。
// 防「删了会话、扫描脱缰后台跑、UI 再停不掉、还在烧 token」的孤儿：不变量在服务端守，不靠前端。
// 仅在确证 active 时拦截；无 task / 终态 / task 读不到则照常删。
func (a *Service) DeleteConversation(ctx context.Context, conversationID string) error {
	fetched, err := a.Conversations.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if fetched.TaskID != "" {
		if tk, gerr := a.Tasks.GetByID(ctx, fetched.TaskID); gerr == nil && tk.Status == task.StatusActive {
			return httpapi.ErrConversationTaskActive
		}
	}
	if err := a.Conversations.DeleteConversation(ctx, conversationID); err != nil {
		return err
	}
	// 会话删除收口：暂存区附件随会话消失（从未随消息下发的中转件没有存在意义）。
	// best-effort——删除失败不回滚会话删除，仅留 Warn 痕迹。
	if a.Attachments != nil {
		if err := a.Attachments.RemoveStaging(conversationID); err != nil {
			chatLog.Warn().Err(err).Str("conversation_id", conversationID).Msg("清理会话暂存附件失败")
		}
	}
	return nil
}

// dispatchAnswerTimeout 是异步分发的总预算：意图分类（秒级）+ 一次完整回答生成。
// 独立于 HTTP 请求——浏览器断开/刷新不取消，回答仍落库，重连补历史可见。
const dispatchAnswerTimeout = 5 * time.Minute

// HandleMessage 满足 httpapi.FollowUpAPI：落 user 消息后立即返回（请求路径零 LLM 调用），
// 意图分类与分流全部异步（dispatchFollowUp）。扫描进行中/暂停中收到 action 指令的反馈
// 由异步分支往时间线落提示消息——前端拿到 200 即清空输入框、追加的消息立即出现在
// 时间线，回答/扫描进展经 SSE 流出。
func (a *Service) HandleMessage(ctx context.Context, conversationID, content string, attachments []attachment.Ref) (string, error) {
	fetched, err := a.Conversations.GetConversation(ctx, conversationID)
	if err != nil {
		return "", err
	}
	msg, err := a.Conversations.AppendMessage(ctx, conversationID, conversation.RoleUser, conversation.KindMessage, content, attachmentMeta(attachments))
	if err != nil {
		return "", err
	}
	go a.dispatchFollowUp(conversationID, fetched.TaskID, content, attachments, msg.Seq) //nolint:gosec // G118：分发独立于请求生命周期
	return "accepted", nil
}

// dispatchFollowUp 追加消息的异步分发：意图闸 → 分流。detached ctx，
// 独立于请求生命周期（请求 ctx 会随响应返回被取消，不能用于 LLM/落库）。
// attachments 随消息元数据已落库；分流到这里只决定它们的去向——
// qa/chat 内联小文本进回答 prompt，action 随下发/续接收口进任务 inbox。
func (a *Service) dispatchFollowUp(conversationID, taskID, content string, attachments []attachment.Ref, userMsgSeq int64) {
	ctx, cancel := context.WithTimeout(context.Background(), dispatchAnswerTimeout)
	defer cancel()

	// 纯附件消息先折算再分类——空文本分类是噪音输入，且必须与 StartChatTask
	// 同一口径（同一份折算规则喂给意图闸，两条入口不各判各的）。
	content = effectiveBrief(content, attachments)
	kind, err := a.classifyIntent(ctx, conversationID, content)
	if err != nil {
		// 会话内入口无 HTTP 响应可承载失败——发可见提示，不静默降级 qa
		// （那会把用户的动作请求悄悄变成聊天回答）。
		a.publishNotice(ctx, conversationID, "（意图分类暂不可用，请稍后重试）")
		return
	}
	isAction := kind == intent.Action

	// 纯聊天会话（无 task）：action 升级为扫描（建 task + 绑会话）；否则通用助手回答。
	if taskID == "" {
		if !isAction {
			if err := chat.New(a).Answer(ctx, conversationID, a.withInlineTexts(content, conversationID, "", "", attachments)); err != nil {
				chatLog.Warn().Err(err).Str("conversation_id", conversationID).Msg("纯聊天回答失败")
				a.publishNotice(ctx, conversationID, "（回答生成失败，请重试）")
			}
			return
		}
		newTaskID, _, err := a.createTask(ctx, content, conversationID, attachments, userMsgSeq)
		if err != nil {
			chatLog.Warn().Err(err).Str("conversation_id", conversationID).Msg("扫描下发失败")
			a.publishNotice(ctx, conversationID, "（扫描下发失败："+err.Error()+"）")
			return
		}
		if err := a.Conversations.LinkTask(ctx, conversationID, newTaskID); err != nil {
			chatLog.Warn().Err(err).Str("conversation_id", conversationID).Msg("link task 失败")
		}
		go a.genTitle(conversationID, content) //nolint:gosec // G118：标题生成独立生命周期
		return
	}

	// 已绑 task 的会话：action → 续接同一 task（finding 累积，不新建）；qa → 就已有 finding 提问。
	tk, err := a.Tasks.GetByID(ctx, taskID)
	if err != nil {
		chatLog.Warn().Err(err).Str("conversation_id", conversationID).Msg("读取 task 失败")
		a.publishNotice(ctx, conversationID, "（服务暂不可用，请稍后重试）")
		return
	}
	if isAction {
		if tk.Status == task.StatusActive {
			// 忙：agent 在跑。不排队不拒绝，时间线给可见反馈（原 409 busy 语义）。
			// 暂停中的任务同闸拦截但给暂停专属指引——「扫描进行中」对暂停任务是误导。
			if tk.Paused {
				a.publishNotice(ctx, conversationID, "（扫描已暂停，本条指令未下发；在任务页点「继续」恢复后再下发）")
			} else {
				a.publishNotice(ctx, conversationID, "（扫描进行中，本条指令未下发；停止扫描后可重发）")
			}
			return
		}
		if _, err := a.FollowUp(ctx, taskID, conversationID, content, attachments, userMsgSeq); err != nil {
			chatLog.Warn().Err(err).Str("conversation_id", conversationID).Msg("续接扫描失败")
			a.publishNotice(ctx, conversationID, "（扫描下发失败："+err.Error()+"）")
		}
		return
	}
	if err := qa.New(a).Answer(ctx, conversationID, taskID, a.withInlineTexts(content, conversationID, taskID, tk.AssignmentID, attachments)); err != nil {
		chatLog.Warn().Err(err).Str("conversation_id", conversationID).Msg("qa 回答失败")
		a.publishNotice(ctx, conversationID, "（回答生成失败，请重试）")
	}
}

// attachmentMeta 把附件引用序列化进用户消息 metadata（时间线渲染芯片 + 回看）。
// 空引用/序列化失败返回 nil（metadata 列保持 NULL，与 thinking 同一纪律）。
func attachmentMeta(refs []attachment.Ref) json.RawMessage {
	if len(refs) == 0 {
		return nil
	}
	b, err := json.Marshal(map[string]any{"attachments": refs})
	if err != nil {
		return nil
	}
	return b
}

// effectiveBrief 把「纯附件消息」折算成可校验的 brief：文本为空但带附件时，
// 用附件名列表占位——标题/批次名/task.brief/意图分类都取它（planner payload
// 在此之上再拼沙盒路径清单）。有文本或无附件时原样返回。
func effectiveBrief(content string, refs []attachment.Ref) string {
	if strings.TrimSpace(content) != "" || len(refs) == 0 {
		return content
	}
	names := make([]string, 0, len(refs))
	for _, r := range refs {
		names = append(names, r.Name)
	}
	return "[附件] " + strings.Join(names, "、")
}

// withInlineTexts 把可内联的附件文本拼进对话 prompt 内容（qa/纯聊天的回答上下文；
// inspector 无沙盒工具，读盘上的文件只能走内联这一条通道）。dest 界定引用的
// 合法根（本会话暂存 / pending / 本任务 inbox），与落位同源校验。
func (a *Service) withInlineTexts(content, conversationID, taskID, assignmentID string, refs []attachment.Ref) string {
	if a.Attachments == nil || len(refs) == 0 {
		return content
	}
	inline, err := a.Attachments.InlineTexts(attachment.Dest{
		ConversationID: conversationID,
		AssignmentID:   assignmentID,
		TaskID:         taskID,
	}, refs)
	if err != nil {
		chatLog.Warn().Err(err).Msg("附件内联渲染失败（回答退化为仅指令文本）")
		return content
	}
	if inline == "" {
		return content
	}
	return content + "\n\n" + inline
}

// materialize 是下发的附件收口点：把随消息提交的引用整批落位任务 inbox。
// Attachments 未装配时是调用方的 bug（引用只会在装配了上传通道的部署里出现）——
// 明确报错而非静默丢引用（丢引用 = agent 看不到承诺过的附件）。
func (a *Service) materialize(conversationID, assignmentID, taskID string, refs []attachment.Ref) ([]attachment.Ref, error) {
	if a.Attachments == nil {
		return nil, fmt.Errorf("attachment: 附件能力未装配，无法处理随消息附件")
	}
	materialized, err := a.Attachments.Materialize(attachment.Dest{
		ConversationID: conversationID,
		AssignmentID:   assignmentID,
		TaskID:         taskID,
	}, refs)
	if err != nil {
		return nil, err // 错误文本已带 attachment: 域前缀，不再叠一层
	}
	return materialized, nil
}

// relinkMessageAttachments 把用户消息 metadata 里的附件引用改写为落位后路径
// （暂存 → inbox）。引用落库先于落位（消息先行、下发异步），落库那一刻的路径
// 只是中转态；时间线芯片的下载通道以改写后的为准。best-effort——改写失败仅
// 芯片下载 404（属数据错，刷新无效），文件本体无损，故留 Warn 不阻断下发。
func (a *Service) relinkMessageAttachments(ctx context.Context, conversationID string, userMsgSeq int64, refs []attachment.Ref) {
	if conversationID == "" || userMsgSeq <= 0 || len(refs) == 0 {
		return
	}
	if err := a.Conversations.SetMessageMetadata(ctx, conversationID, userMsgSeq, attachmentMeta(refs)); err != nil {
		chatLog.Warn().Err(err).Str("conversation_id", conversationID).Int64("seq", userMsgSeq).
			Msg("附件引用落位改写失败（时间线芯片仍指暂存路径）")
	}
}

// ---- qa.Deps 实现 ----

// FindingsSummary 满足 qa.Deps：把 task 黑板 finding 渲染成文本摘要。
func (a *Service) FindingsSummary(ctx context.Context, _, taskID string) (string, error) {
	fs, err := a.Findings.ListByTask(ctx, taskID)
	if err != nil {
		return "", err
	}
	return renderFindingsSummary(fs), nil
}

// qaFindingsTopN 是问答上下文携带的 finding 条数上限：清单型输入按 top-k +
// 溢出行治理——持续探索模式的 finding 随任务时长线性增长，全量渲染会无界
// 吃掉问答调用的输入预算。
const qaFindingsTopN = 20

// severityRank 给自由文本 severity 定序（大小写不敏感前缀匹配，与 finding
// 模型的建议值对齐）：问答最相关的是最严重的成果而非最新的，截断只丢低危。
// 同级保持 ListByTask 的 created_at DESC 稳定序。
func severityRank(sev string) int {
	l := strings.ToLower(sev)
	switch {
	case strings.HasPrefix(l, "critical"):
		return 5
	case strings.HasPrefix(l, "high"):
		return 4
	case strings.HasPrefix(l, "medium"):
		return 3
	case strings.HasPrefix(l, "low"):
		return 2
	case strings.HasPrefix(l, "info"):
		return 1
	default:
		return 0
	}
}

// renderFindingsSummary 渲染问答用的 finding 摘要：严重度降序取 top-N，
// 溢出以计数行显式留痕（不静默）。
func renderFindingsSummary(fs []finding.Finding) string {
	if len(fs) == 0 {
		return "（暂无 finding）"
	}
	sorted := make([]finding.Finding, len(fs))
	copy(sorted, fs)
	sort.SliceStable(sorted, func(i, j int) bool {
		return severityRank(sorted[i].Severity) > severityRank(sorted[j].Severity)
	})
	shown := sorted
	if len(sorted) > qaFindingsTopN {
		shown = sorted[:qaFindingsTopN]
	}
	var b strings.Builder
	for i, f := range shown {
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, f.Severity, f.Summary)
	}
	if len(sorted) > len(shown) {
		fmt.Fprintf(&b, "（另有 %d 条较低严重度的 finding，完整清单见漏洞页）\n", len(sorted)-len(shown))
	}
	return b.String()
}

// maxBriefRunes 是任务描述（brief）的入口长度契约。brief 逐字进 executor
// system prompt 与 planner 根目标，压缩机制不裁 system——超大 brief 绕过
// 全部预算防线，只能在执行期被长度哨兵拦下（任务已下发、浪费一次跑）。
// 契约在入口拒绝而非截断：上下文装配截断无损语义，任务指令被静默截断会
// 腐蚀意图（agent 照半截指令干活），两者纪律不同。
const maxBriefRunes = 20000

// validateBrief 校验任务描述的入口契约。
func validateBrief(brief string) error {
	if n := len([]rune(brief)); n > maxBriefRunes {
		return fmt.Errorf("任务描述过长（%d 字 > 上限 %d），请精简后重发", n, maxBriefRunes)
	}
	return nil
}

// RoleRouter 按业务 role 路由：role → llmconfig.AgentComplexity 归档（inspector 等
// 轻量路由 key 落 simple）→ framework Router（complexity 档 → provider，带
// retry/fallback/审计包装）。唯一职责是把 role 语义接到 complexity 栈上。
type RoleRouter struct {
	Router *fwllm.Router
}

// providerGen 把已带 retry/fallback 的 Provider 适配成 Generator 形状
// （chat/qa/intent 的最小依赖接口）。
// providerGen 是 RoleRouter 解析出的生成器：Provider 已在 Router 侧包好
// retry/fallback，audit 归属（Role）随生成器携带——ctx 在调用时注入。
type providerGen struct {
	p         fwllm.Provider
	auditRole string // llm_invocation.role 标签（inspector 等；api 侧调用无 task/run 归属）
}

func (g providerGen) Generate(ctx context.Context, msgs []fwllm.Message, tools []fwllm.ToolSchema) (fwllm.Result, error) {
	if g.auditRole != "" {
		ctx = fwllm.WithCallMeta(ctx, fwllm.CallMeta{Role: g.auditRole})
	}
	resp, err := g.p.Complete(ctx, fwllm.Request{Messages: msgs, Tools: tools})
	if err != nil {
		return fwllm.Result{}, err
	}
	return g.resultOf(resp), nil
}

// resultOf 把非流式 Response 收敛成 Generator Result（附 provider/model 归属）。
func (g providerGen) resultOf(resp fwllm.Response) fwllm.Result {
	return fwllm.Result{
		Content: resp.Content, ToolCalls: resp.ToolCalls, Usage: resp.Usage,
		FinishReason: resp.FinishReason, Thinking: resp.Thinking,
		Provider: g.p.ProviderID(), Model: g.p.ModelID(),
	}
}

// generateStream 流式生成：正文与扩展思考增量分别回调（可空；provider 不支持
// 流式时退化为无增量的一次性返回）。返回值与 generate 同构。
func (r *RoleRouter) generateStream(ctx context.Context, role string, msgs []fwllm.Message, tools []fwllm.ToolSchema, onText, onThinking func(chunk string)) (fwllm.Result, error) {
	p, err := r.Router.For(ctx, fwllm.ParseComplexity(llmconfig.AgentComplexity(role)))
	if err != nil {
		return fwllm.Result{}, err
	}
	// 审计标签：api 侧调用无 task/run 归属，Role 标签区分 chat/qa/意图分类等路径。
	ctx = fwllm.WithCallMeta(ctx, fwllm.CallMeta{Role: role})
	ch, err := p.Stream(ctx, fwllm.Request{Messages: msgs, Tools: tools})
	if err != nil {
		if errors.Is(err, fwllm.ErrStreamingUnsupported) {
			resp, cerr := p.Complete(ctx, fwllm.Request{Messages: msgs, Tools: tools})
			if cerr != nil {
				return fwllm.Result{}, cerr
			}
			return providerGen{p: p}.resultOf(resp), nil
		}
		return fwllm.Result{}, err
	}
	resp, err := fwllm.CollectStream(ctx, ch, onText, onThinking)
	if err != nil {
		return fwllm.Result{}, err
	}
	return providerGen{p: p}.resultOf(resp), nil
}

// generator 解析 role 对应的 Generator（Provider 已在 Router 侧包好 retry/fallback）。
func (r *RoleRouter) generator(ctx context.Context, role string) (providerGen, error) {
	p, err := r.Router.For(ctx, fwllm.ParseComplexity(llmconfig.AgentComplexity(role)))
	if err != nil {
		return providerGen{}, err
	}
	return providerGen{p: p, auditRole: role}, nil
}

// GenerateStream 满足 chat/qa.Deps：流式生成（inspector 档），正文/思考增量分别回调广播。
func (a *Service) GenerateStream(ctx context.Context, msgs []fwllm.Message, tools []fwllm.ToolSchema, onText, onThinking func(chunk string)) (fwllm.Result, error) {
	return a.Router.generateStream(ctx, "inspector", msgs, tools, onText, onThinking)
}

// PublishDelta 满足 chat/qa.Deps：广播一帧回答增量（agent_name=assistant，
// channel ∈ {text, thinking}——前端 assistant 道的双通道）。
func (a *Service) PublishDelta(ctx context.Context, conversationID, channel, text string) error {
	return a.Publisher.PublishDelta(ctx, conversationID, "assistant", channel, text)
}

// AppendAssistant 满足 qa.Deps：落 assistant 消息（metadata.thinking 携带思考终稿），
// 返回 SSE payload。
//
// metadata 规范化：thinking 为空时不序列化该字段（保持 wire/数据库清洁），
// 避免前端收到 `{"thinking":""}` 空字符串或 `{"thinking":{}}` 空对象导致渲染异常。
func (a *Service) AppendAssistant(ctx context.Context, conversationID, content, thinking string) ([]byte, error) {
	var meta json.RawMessage
	if thinking != "" {
		// 仅在 thinking 非空时序列化进 metadata，避免空字符串污染数据
		m, err := json.Marshal(map[string]string{"thinking": thinking})
		if err != nil {
			// 序列化失败（极端情况，如 thinking 含非法 UTF-8）：降级为 nil metadata，
			// 不阻断消息落库，但记录告警以便排查。
			chatLog.Warn().Err(err).Str("conversation_id", conversationID).Msg("thinking 序列化失败，metadata 降级为 nil")
		} else {
			meta = m
		}
	}
	// thinking 为空或序列化失败时 meta=nil → metadata 列存 NULL → 前端 Metadata 为 null（符合类型契约）
	msg, err := a.Conversations.AppendMessage(ctx, conversationID, conversation.RoleAssistant, conversation.KindMessage, content, meta)
	if err != nil {
		return nil, err
	}
	return json.Marshal(msg)
}

// Publish 满足 qa.Deps：推 SSE。
func (a *Service) Publish(ctx context.Context, conversationID string, payload []byte) error {
	err := a.Publisher.Publish(ctx, conversationID, payload)
	if err != nil {
		// 推送失败的用户端表现是「没有实时输出」——best-effort 不阻断，
		// 但必须留痕，否则无从排查（重连补历史是唯一恢复通道）。
		chatLog.Warn().Err(err).Str("conversation_id", conversationID).Msg("SSE 推送失败（补历史可见，实时流缺失）")
	}
	return err
}

// StartChatTask 满足 httpapi.ChatAPI：建会话 + 落用户首条消息 + **同步意图闸**。
//
// 分类必须留在请求路径：ChatResponse 的 task_id 是程序化调用方（e2e 触发器、
// API 集成）拿任务句柄的唯一通道，异步分发会让该字段结构性恒空（契约破裂，
// 消费方无从发现任务）。分类是单 token 的 light LLM 调用（秒级），同步代价
// 可忽略；真正慢的是 qa 回答生成与扫描执行——两者保持异步（原「GLM 慢时浏览器
// 干等几十秒」的病灶在慢回答，不在分类）。
func (a *Service) StartChatTask(ctx context.Context, brief string, attachments []attachment.Ref) (string, string, error) {
	// 纯附件消息：时间线存原文（空文本 + 芯片），分类/标题/下发用附件名折算值。
	dispatchBrief := effectiveBrief(brief, attachments)
	created, err := a.Conversations.CreateConversation(ctx, BriefTitle(dispatchBrief), "")
	if err != nil {
		return "", "", fmt.Errorf("create conversation: %w", err)
	}
	conversationID := created.ID
	msg, err := a.Conversations.AppendMessage(ctx, conversationID, conversation.RoleUser, conversation.KindMessage, brief, attachmentMeta(attachments))
	if err != nil {
		return "", "", fmt.Errorf("append user message: %w", err)
	}

	kind, clsErr := a.classifyIntent(ctx, conversationID, dispatchBrief)
	if clsErr != nil {
		return conversationID, "", clsErr
	}
	if kind != intent.Action {
		go a.answerChatAsync(conversationID, a.withInlineTexts(dispatchBrief, conversationID, "", "", attachments)) //nolint:gosec // G118：回答独立于请求生命周期
		return conversationID, "", nil
	}

	taskID, _, err := a.createTask(ctx, dispatchBrief, conversationID, attachments, msg.Seq)
	if err != nil {
		return conversationID, "", fmt.Errorf("dispatch task: %w", err)
	}
	if err := a.Conversations.LinkTask(ctx, conversationID, taskID); err != nil {
		chatLog.Warn().Err(err).Str("conversation_id", conversationID).Msg("link task 失败")
	}
	go a.genTitle(conversationID, dispatchBrief) //nolint:gosec // G118：标题生成独立生命周期
	return conversationID, taskID, nil
}

// 会话上文参与 chat/qa/意图分类的体量钳制：轮次条数、单条 rune、总 token。
// 上文是增强不是主体——超出即丢最旧（对话记忆的价值重心在近端），绝不因
// 上文体量放大回答成本。
const (
	recentTurnCount    = 12   // 参与上文的 user/assistant 消息条数上限
	recentTurnTokens   = 4000 // 上文总 token 预算
	recentTurnMaxRunes = 4000 // 单条钳制 rune（与时间线落库截断同口径）
)

// History 满足 chat/qa.Deps：取会话的对话上文。LatestMessages 取最近 N 条
// 真实对话消息（agent 过程事件天然排除），从最新往回收纳、超总预算丢最旧，
// 最后剔除与当前消息内容相同的末条 user（当前消息已由入口先落库，避免重复
// 进上下文——按内容自尾匹配对重复消息同样正确）。
func (a *Service) History(ctx context.Context, conversationID, excludeContent string) ([]fwllm.Message, error) {
	msgs, err := a.Conversations.LatestMessages(ctx, conversationID, recentTurnCount+1)
	if err != nil {
		return nil, err
	}
	// 自尾剔除当前消息（末条 content 相同的 user；按元素删除而非截断，
	// 保住其后可能存在的并发消息）
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == conversation.RoleUser && msgs[i].Content == excludeContent {
			msgs = append(msgs[:i], msgs[i+1:]...)
			break
		}
	}
	// 从最新往回收纳，超预算即止（丢最旧保最新）
	kept := make([]fwllm.Message, 0, len(msgs))
	used := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		content := textx.TruncateRunes(msgs[i].Content, recentTurnMaxRunes, "…（截断）")
		t := fwllm.EstimateTokens(content)
		if used+t > recentTurnTokens && len(kept) > 0 {
			break
		}
		used += t
		kept = append(kept, fwllm.Message{Role: fwllm.MessageRole(msgs[i].Role), Content: content})
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return kept, nil
}

// classifyIntent 用 light provider 判定消息意图。provider 不可用返回显式 error
// （入口方自行决定报错还是提示），不静默降级 qa。会话上文读取失败仅告警并
// 去上文分类——分类闸失明才是主功能失效，上文只是指代消解的增强。
func (a *Service) classifyIntent(ctx context.Context, conversationID, content string) (intent.Kind, error) {
	g, err := a.Router.generator(ctx, "inspector") // light provider
	if err != nil {
		chatLog.Warn().Err(err).Str("conversation_id", conversationID).Msg("意图分类 provider 失败")
		return intent.QA, fmt.Errorf("intent classify provider: %w", err)
	}
	recent, hErr := a.History(ctx, conversationID, content)
	if hErr != nil {
		chatLog.Warn().Err(hErr).Str("conversation_id", conversationID).Msg("读会话上文失败，去上文分类")
		recent = nil
	}
	return intent.Classify(ctx, g, content, recent)
}

// answerChatAsync 纯聊天的异步回答：增量经 SSE 打字机流出，全文落库兜底。
// 回答生成慢（数十秒级），独立 context 不随请求结束被 cancel。
func (a *Service) answerChatAsync(conversationID, brief string) {
	ctx, cancel := context.WithTimeout(context.Background(), dispatchAnswerTimeout)
	defer cancel()
	if err := chat.New(a).Answer(ctx, conversationID, brief); err != nil {
		chatLog.Warn().Err(err).Str("conversation_id", conversationID).Msg("纯聊天回答失败")
		a.publishNotice(ctx, conversationID, "（回答生成失败，请重试）")
	}
}

// publishNotice 落一条 assistant 提示消息并广播（异步分支失败/忙碌时给时间线的可见反馈，
// 替代原 409/500 错误路径——异步分支没有 HTTP 响应可承载错误）。
func (a *Service) publishNotice(ctx context.Context, conversationID, text string) {
	payload, err := a.AppendAssistant(ctx, conversationID, text, "")
	if err != nil {
		chatLog.Warn().Err(err).Str("conversation_id", conversationID).Str("text", text).Msg("提示消息落库失败")
		return
	}
	_ = a.Publish(ctx, conversationID, payload)
}

// genTitle 用 light LLM 把 brief 总结成 ≤16 字的简短标题，回填 conversation.title。
// 异步调用（独立 context，不随请求结束被 cancel）；失败静默（保留 BriefTitle 兜底）。
func (a *Service) genTitle(conversationID, brief string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	g, err := a.Router.generator(ctx, "inspector") // light provider，便宜
	if err != nil {
		return
	}
	// 标题只需要大意：brief 钳长后进 prompt（标题生成是单发便宜档调用，
	// 不该为病态长 brief 买单）。
	prompt := "把下面的渗透测试任务描述总结成一个不超过 16 字的简短中文标题，" +
		"突出目标和测试类型（如「DVWA SQL注入渗透」）。只输出标题本身，不要引号、不要解释。\n\n任务：" + textx.TruncateRunes(brief, 2000, "…（截断）")
	res, err := g.Generate(ctx, []fwllm.Message{{Role: fwllm.RoleUser, Content: prompt}}, nil)
	if err != nil {
		return
	}
	title := strings.TrimSpace(strings.Trim(strings.TrimSpace(res.Content), `"'「」`))
	if r := []rune(title); len(r) > 24 { // 防 LLM 超长，硬截兜底
		title = string(r[:24])
	}
	if title == "" {
		return
	}
	_ = a.Conversations.SetTitle(ctx, conversationID, title)
}

// BriefTitle 取 brief 前 40 字（rune 安全，不截半个中文）作会话标题。
func BriefTitle(brief string) string {
	const maxRunes = 40
	r := []rune(brief)
	if len(r) > maxRunes {
		return string(r[:maxRunes])
	}
	return brief
}
