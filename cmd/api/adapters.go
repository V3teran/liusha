package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/chat"
	llmcfg "github.com/V3teran/liusha/internal/config/llm"
	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/finding"
	fwllm "github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/msgclass"
	"github.com/V3teran/liusha/internal/qa"
	"github.com/V3teran/liusha/internal/scanstream"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// auditLog 专记审计写入的 best-effort 失败——审计是安全/合规轨迹，
// 失败不阻塞业务但必须留可见日志（不能完全静默吞掉）。
var auditLog = logx.New("api.audit")

// chatLog 记纯聊天回答的 best-effort 失败——会话与用户消息已落库，回答缺失不阻断会话创建。
var chatLog = logx.New("api.chat")

// taskStatusAdapter 把 task store 适配到 httpapi.TaskStatusResolver：
// 凭证详情里每个会话身份标注归属任务的当前状态。
type taskStatusAdapter struct {
	tasks *task.Store
}

// TaskStatus 返回任务状态与存在性（行不存在 = 已级联删除）。
func (a taskStatusAdapter) TaskStatus(ctx context.Context, taskID string) (string, bool) {
	t, err := a.tasks.GetByID(ctx, taskID)
	if err != nil {
		return "", false
	}
	return string(t.Status), true
}

// taskAPIAdapter 把 task store 适配到 httpapi.TaskAPI 窄接口。
//
// Abort/List 直接走 task store，无双表试探。
// HTTP API 不暴露 errMsg：用户主动取消 task 即视为正常结束，abort 恒传 ""。
type taskAPIAdapter struct {
	tasks *task.Store
	audit *audit.Store // 0047：abort 写审计事件；nil 时跳过
}

// Abort 把 task 置为 aborted。errMsg 恒空（用户主动 abort 视为正常结束）。
// 0047：成功 abort 后写 audit_log（executor=api_user，target_kind=task）。
func (a taskAPIAdapter) Abort(ctx context.Context, id string) error {
	if _, err := a.tasks.GetByID(ctx, id); err != nil {
		return fmt.Errorf("task %s not found: %w", id, err)
	}
	if err := a.tasks.Abort(ctx, id, ""); err != nil {
		return err
	}
	a.writeAudit(ctx, audit.ActionTaskAbort, "task", id)
	return nil
}

// writeAudit best-effort 写审计事件；失败不阻塞业务，但记 Warn 留可见痕迹。
func (a taskAPIAdapter) writeAudit(ctx context.Context, action, kind, id string) {
	if a.audit == nil {
		return
	}
	if _, err := a.audit.Append(ctx, audit.Event{
		Actor:      audit.ActorAPIUser,
		Action:     action,
		TargetKind: kind,
		TargetID:   id,
	}); err != nil {
		auditLog.Warn().Err(err).Str("action", action).Str("target", id).Msg("审计事件写入失败（不阻塞业务）")
	}
}

// scanAdapter 把 task store + agentrun.Store + worker.Client 组合成
// httpapi.ScanAPI 一站式入口：建 scan → 建 agent agent_run → 入 asynq 队列。
//
// 任一步失败都不留中间状态（前面失败直接返错；task 已建但 enqueue 失败会留
// scan，由用户手动 abort 或后续 sweeper——保持简单不上事务，与
// ingestor.enqueueMain 一致语义）。
// 合表后：task.Store 管扫描生命周期，agentrun.Store 建 run。
type scanAdapter struct {
	assignments   *assignment.Store
	tasks         *task.Store
	agentRuns     *agentrun.Store
	enq           *worker.Client
	audit         *audit.Store        // 0047：create 写审计事件；nil 跳过
	conversations *conversation.Store // 阶段B：StartChatScan 建会话；nil 时仅 CreateScan 可用

	router    *roleRouter           // 多轮：意图分类 + 问答（light provider，role→档→framework Router）
	findings  *finding.Store        // 问答读 task 黑板 finding
	publisher *scanstream.Publisher // 问答回答 publish SSE
	control   *controlplane.Store   // 用户停止 → terminate 控制事件（停掉 runner 执行）

	// run 整体超时上限。入队时设为 asynq.Timeout，
	// 否则 asynq 默认 30min 任务 deadline 会架空 runner handler 里 4h 的 WithTimeout——
	// run 跑到 30min 就被 ctx cancel（实测 30min 整 abort、planner 没机会收尾）。
	maxRunTimeout time.Duration
}

// eventStreamAdapter 把 scanstream 订阅适配成 httpapi.EventStream（SSE handler 用）。
// scanstream.Subscription 自带 Events()/Close()，满足 httpapi.EventSubscription。
type eventStreamAdapter struct{ rdb *redis.Client }

func (e eventStreamAdapter) Subscribe(ctx context.Context, conversationID string) httpapi.EventSubscription {
	return scanstream.Subscribe(ctx, e.rdb, conversationID)
}

// createScan 是建 scan 的核心：建 assignment + task + agent run + 入 asynq 队列（带
// conversationID）。CreateScan（无会话纯后台）与 StartChatScan（会话发起）共用。
// 会话发起时随入队成功落 dispatched 状态节点（时间线任务边界：何时下发）。
func (a *scanAdapter) createScan(ctx context.Context, brief, conversationID string) (string, string, error) {
	// 一切下发皆走 assignment（§3.1）：单发 = 单元素 assignment(manual) → 1 task。
	asg, err := a.assignments.Create(ctx, assignment.NewParams{

		Source: assignment.SourceManual,
		Items:  []assignment.Item{{Brief: brief}},
		Title:  briefTitle(brief),
	})
	if err != nil {
		return "", "", fmt.Errorf("create assignment: %w", err)
	}
	taskID, runID, err := a.expandItem(ctx, asg.ID, brief, conversationID)
	if err != nil {
		return "", "", err
	}
	if conversationID != "" {
		a.appendTaskStatus(ctx, conversationID, conversation.TaskStatusDispatched,
			"已下发扫描，agent 启动中…", nil)
	}
	return taskID, runID, nil
}

// appendTaskStatus 落一条任务生命周期状态节点并广播 SSE（迁移写者共用；
// best-effort——落库失败仅告警，不阻断状态迁移本体）。
func (a *scanAdapter) appendTaskStatus(ctx context.Context, convID, status, summary string, fields map[string]any) {
	msg, err := a.conversations.AppendMessage(ctx, convID,
		conversation.RoleAssistant, conversation.KindEvent,
		summary, conversation.TaskStatusMeta(status, fields))
	if err != nil {
		chatLog.Warn().Err(err).Str("conv", convID).Str("status", status).Msg("任务状态节点落库失败")
		return
	}
	_ = a.publisher.PublishMessage(ctx, convID, msg)
}

// expandItem 把 assignment 下的一个 item（brief）展开成 task + agent run + enqueue。
// 单发（createScan，建单元素 assignment 后展开 1 条）与批量/定时（cron Scheduler，建多元素
// assignment 后逐条展开）复用同一份展开逻辑，只是 assignment 的建法不同（§3.1 单发 vs 批量/cron）。
//
// target_host 留空——不在 API 层 parse brief，runner 入口从 brief 抽取后回填（派生列，见 D5）。
func (a *scanAdapter) expandItem(ctx context.Context, assignmentID, brief, conversationID string) (string, string, error) {
	// payload 只装 brief 原文——目标 URL / host 由 runner 从 brief 自识别回填。
	payloadInput, err := json.Marshal(map[string]string{"brief": brief})
	if err != nil {
		return "", "", fmt.Errorf("marshal payload: %w", err)
	}
	tk, err := a.tasks.Create(ctx, task.NewParams{
		AssignmentID: assignmentID,
		Brief:        brief,
	})
	if err != nil {
		return "", "", fmt.Errorf("create task: %w", err)
	}

	tid, err := a.enqueuePlannerRun(ctx, tk.ID, conversationID, payloadInput)
	if err != nil {
		return "", "", err
	}

	// 0047：task 创建成功 → 审计事件。metadata 记 brief 前 200 字便于事后查（完整 brief 在 task.brief 列）。
	if a.audit != nil {
		briefPreview := brief
		if len(briefPreview) > 200 {
			briefPreview = briefPreview[:200]
		}
		meta, _ := json.Marshal(map[string]string{"brief_preview": briefPreview, "agent_run_id": tid})
		if _, err := a.audit.Append(ctx, audit.Event{
			Actor:      audit.ActorAPIUser,
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
// 入队 Payload 与 createScan 同构，仅 TaskID 复用传入 taskID、不新建 task。
func (a *scanAdapter) FollowUp(ctx context.Context, taskID, conversationID, brief string) (string, error) {
	if err := a.tasks.Reopen(ctx, taskID); err != nil {
		return "", fmt.Errorf("reopen task: %w", err)
	}
	// 清滞留的 terminate：上轮停止的终止指令不得误杀本轮续接（best-effort）。
	if a.control != nil {
		if n, err := a.control.ResolvePendingTerminates(ctx, taskID); err != nil {
			chatLog.Warn().Err(err).Str("task", taskID).Msg("清理滞留 terminate 失败（续接 run 有被迟到终止的风险）")
		} else if n > 0 {
			chatLog.Info().Int("resolved", n).Str("task", taskID).Msg("已清理上轮滞留的 terminate 控制事件")
		}
	}
	payloadInput, err := json.Marshal(map[string]string{"brief": brief})
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	runID, err := a.enqueuePlannerRun(ctx, taskID, conversationID, payloadInput)
	if err != nil {
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
func (a *scanAdapter) enqueuePlannerRun(ctx context.Context, taskID, conversationID string, payloadInput []byte) (string, error) {
	tid, err := a.agentRuns.Create(ctx, agentrun.NewParams{
		TaskID: taskID,
		Role:   "planner",
		Input:  payloadInput,
	})
	if err != nil {
		return "", fmt.Errorf("create agent run: %w", err)
	}
	if _, _, err := a.enq.Enqueue(ctx, worker.RoleAgent, worker.Payload{
		AgentRunID:     tid,
		TaskID:         taskID,
		ConversationID: conversationID, // 会话发起时非空 → runner 发过程事件
		// 场景 code：runner 据此数据驱动派发引擎/操作员编排
		Input:    payloadInput,
		TaskRole: worker.RoleAgent,
	}, asynq.MaxRetry(0), asynq.Timeout(a.maxRunTimeout)); err != nil {
		return "", fmt.Errorf("enqueue: %w", err)
	}
	return tid, nil
}

// AbortConversationScan 满足 httpapi.AbortAPI：abort 会话关联的 task。
//
// 三件事一体（缺一则「停止」名不副实）：
//  1. task 置 aborted（DB 终态，幂等/守卫见 task.Store.Abort）；
//  2. 发 terminate 控制事件——runner 控制平面消费后停认知循环（此前缺失：
//     只改 DB 的话 agent 继续烧 token 直到自然收敛）；
//  3. 时间线落 aborted 节点（用户可见的停止边界）。
func (a *scanAdapter) AbortConversationScan(ctx context.Context, convID string) error {
	conv, err := a.conversations.GetConversation(ctx, convID)
	if err != nil {
		return err
	}
	if conv.TaskID == "" {
		return fmt.Errorf("conversation 无关联 task")
	}
	if err := a.tasks.Abort(ctx, conv.TaskID, "用户停止"); err != nil {
		return err
	}
	if a.control != nil {
		payload, _ := json.Marshal(map[string]string{"reason": "用户停止"})
		if _, err := a.control.Create(ctx, conv.TaskID, controlplane.CommandTerminate, payload); err != nil {
			// best-effort：控制事件失败不回滚 abort（DB 终态已定，循环至迟被
			// 心跳停摆回收），但必须留痕。
			chatLog.Warn().Err(err).Str("task", conv.TaskID).Msg("terminate 控制事件写入失败（循环停摆延迟到 reaper）")
		}
	}
	a.appendTaskStatus(ctx, convID, conversation.TaskStatusAborted,
		"扫描已停止", map[string]any{"reason": "用户停止"})
	return nil
}

// DeleteConversation 满足 httpapi.ConversationDeleter：删会话+消息，但关联 task 仍在跑时拒删。
// 「先停后删」的服务端把关——返回 httpapi.ErrConversationScanActive → handler 映射 409。
// 防「删了会话、扫描脱缰后台跑、UI 再停不掉、还在烧 token」的孤儿（见 reference_deep_subagent_context 同源思路：
// 不变量在服务端守，不靠前端）。仅在确证 active 时拦截；无 task / 终态 / task 读不到则照常删。
func (a *scanAdapter) DeleteConversation(ctx context.Context, convID string) error {
	conv, err := a.conversations.GetConversation(ctx, convID)
	if err != nil {
		return err
	}
	if conv.TaskID != "" {
		if tk, gerr := a.tasks.GetByID(ctx, conv.TaskID); gerr == nil && tk.Status == task.StatusActive {
			return httpapi.ErrConversationScanActive
		}
	}
	return a.conversations.DeleteConversation(ctx, convID)
}

// dispatchAnswerTimeout 是异步分发的总预算：意图分类（秒级）+ 一次完整回答生成。
// 独立于 HTTP 请求——浏览器断开/刷新不取消，回答仍落库，重连补历史可见。
const dispatchAnswerTimeout = 5 * time.Minute

// HandleMessage 满足 httpapi.FollowUpAPI：落 user 消息后立即返回（请求路径零 LLM 调用），
// 意图分类与分流全部异步（dispatchFollowUp）。busy 判定随之下沉到异步分支：扫描进行中
// 收到 action 指令不再 409，而是往时间线落提示消息——前端拿到 200 即清空输入框、
// 追加的消息立即出现在时间线，回答/扫描进展经 SSE 流出。
func (a *scanAdapter) HandleMessage(ctx context.Context, convID, content string) (string, bool, error) {
	conv, err := a.conversations.GetConversation(ctx, convID)
	if err != nil {
		return "", false, err
	}
	if _, err := a.conversations.AppendMessage(ctx, convID, conversation.RoleUser, conversation.KindMessage, content, nil); err != nil {
		return "", false, err
	}
	go a.dispatchFollowUp(convID, conv.TaskID, content) //nolint:gosec // G118：分发独立于请求生命周期
	return "accepted", false, nil
}

// dispatchFollowUp 追加消息的异步分发：意图闸 → 分流。detached ctx，
// 独立于请求生命周期（请求 ctx 会随响应返回被取消，不能用于 LLM/落库）。
func (a *scanAdapter) dispatchFollowUp(convID, taskID, content string) {
	ctx, cancel := context.WithTimeout(context.Background(), dispatchAnswerTimeout)
	defer cancel()

	kind, err := a.classifyIntent(ctx, convID, content)
	if err != nil {
		// 会话内入口无 HTTP 响应可承载失败——发可见提示，不静默降级 qa
		// （那会把用户的动作请求悄悄变成聊天回答）。
		a.publishNotice(ctx, convID, "（意图分类暂不可用，请稍后重试）")
		return
	}
	isAction := kind == msgclass.KindAction

	// 纯聊天会话（无 task）：action 升级为扫描（建 task + 绑会话）；否则通用助手回答。
	if taskID == "" {
		if !isAction {
			if err := chat.New(a).Answer(ctx, convID, content); err != nil {
				chatLog.Warn().Err(err).Str("conv", convID).Msg("纯聊天回答失败")
				a.publishNotice(ctx, convID, "（回答生成失败，请重试）")
			}
			return
		}
		newTaskID, _, err := a.createScan(ctx, content, convID)
		if err != nil {
			chatLog.Warn().Err(err).Str("conv", convID).Msg("扫描下发失败")
			a.publishNotice(ctx, convID, "（扫描下发失败："+err.Error()+"）")
			return
		}
		if err := a.conversations.LinkTask(ctx, convID, newTaskID); err != nil {
			chatLog.Warn().Err(err).Str("conv", convID).Msg("link task 失败")
		}
		go a.genTitle(convID, content) //nolint:gosec // G118：标题生成独立生命周期
		return
	}

	// 已绑 task 的会话：action → 续接同一 task（finding 累积，不新建）；qa → 就已有 finding 提问。
	tk, err := a.tasks.GetByID(ctx, taskID)
	if err != nil {
		chatLog.Warn().Err(err).Str("conv", convID).Msg("读取 task 失败")
		a.publishNotice(ctx, convID, "（服务暂不可用，请稍后重试）")
		return
	}
	if isAction {
		if tk.Status == task.StatusActive {
			// 忙：agent 在跑。不排队不拒绝，时间线给可见反馈（原 409 busy 语义）。
			a.publishNotice(ctx, convID, "（扫描进行中，本条指令未下发；停止扫描后可重发）")
			return
		}
		if _, err := a.FollowUp(ctx, taskID, convID, content); err != nil {
			chatLog.Warn().Err(err).Str("conv", convID).Msg("续接扫描失败")
			a.publishNotice(ctx, convID, "（扫描下发失败："+err.Error()+"）")
		}
		return
	}
	if err := qa.New(a).Answer(ctx, convID, taskID, content); err != nil {
		chatLog.Warn().Err(err).Str("conv", convID).Msg("qa 回答失败")
		a.publishNotice(ctx, convID, "（回答生成失败，请重试）")
	}
}

// ---- qa.Deps 实现 ----

// FindingsSummary 满足 qa.Deps：把 task 黑板 finding 渲染成文本摘要。
func (a *scanAdapter) FindingsSummary(ctx context.Context, _, taskID string) (string, error) {
	fs, err := a.findings.ListByTask(ctx, taskID)
	if err != nil {
		return "", err
	}
	if len(fs) == 0 {
		return "（暂无 finding）", nil
	}
	var b strings.Builder
	for i, f := range fs {
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, f.Severity, f.Summary)
	}
	return b.String(), nil
}

// roleRouter 按业务 role 路由：role → llmcfg.AgentComplexity 归档（inspector 等
// 轻量路由 key 落 simple）→ framework Router（complexity 档 → provider，带
// retry/fallback/审计包装）。唯一职责是把 role 语义接到 complexity 栈上。
type roleRouter struct {
	router *fwllm.Router
}

// providerGen 把已带 retry/fallback 的 Provider 适配成 Generator 形状
// （chat/qa/msgclass 的最小依赖接口）。
type providerGen struct{ p fwllm.Provider }

func (g providerGen) Generate(ctx context.Context, msgs []fwllm.Message, tools []fwllm.ToolSchema) (fwllm.Result, error) {
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
func (r roleRouter) generateStream(ctx context.Context, role string, msgs []fwllm.Message, tools []fwllm.ToolSchema, onText, onThinking func(chunk string)) (fwllm.Result, error) {
	p, err := r.router.For(ctx, fwllm.ParseComplexity(llmcfg.AgentComplexity(role)))
	if err != nil {
		return fwllm.Result{}, err
	}
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
func (r roleRouter) generator(ctx context.Context, role string) (providerGen, error) {
	p, err := r.router.For(ctx, fwllm.ParseComplexity(llmcfg.AgentComplexity(role)))
	if err != nil {
		return providerGen{}, err
	}
	return providerGen{p: p}, nil
}

// GenerateStream 满足 chat/qa.Deps：流式生成（inspector 档），正文/思考增量分别回调广播。
func (a *scanAdapter) GenerateStream(ctx context.Context, msgs []fwllm.Message, tools []fwllm.ToolSchema, onText, onThinking func(chunk string)) (fwllm.Result, error) {
	return a.router.generateStream(ctx, "inspector", msgs, tools, onText, onThinking)
}

// PublishDelta 满足 chat/qa.Deps：广播一帧回答增量（agent_name=assistant，
// channel ∈ {text, thinking}——前端 assistant 道的双通道）。
func (a *scanAdapter) PublishDelta(ctx context.Context, conversationID, channel, text string) error {
	return a.publisher.PublishDelta(ctx, conversationID, "assistant", channel, text)
}

// AppendAssistant 满足 qa.Deps：落 assistant 消息（metadata.thinking 携带思考终稿），
// 返回 SSE payload。
//
// metadata 规范化：thinking 为空时不序列化该字段（保持 wire/数据库清洁），
// 避免前端收到 `{"thinking":""}` 空字符串或 `{"thinking":{}}` 空对象导致渲染异常。
func (a *scanAdapter) AppendAssistant(ctx context.Context, convID, content, thinking string) ([]byte, error) {
	var meta json.RawMessage
	if thinking != "" {
		// 仅在 thinking 非空时序列化进 metadata，避免空字符串污染数据
		m, err := json.Marshal(map[string]string{"thinking": thinking})
		if err != nil {
			// 序列化失败（极端情况，如 thinking 含非法 UTF-8）：降级为 nil metadata，
			// 不阻断消息落库，但记录告警以便排查。
			chatLog.Warn().Err(err).Str("conv", convID).Msg("thinking 序列化失败，metadata 降级为 nil")
		} else {
			meta = m
		}
	}
	// thinking 为空或序列化失败时 meta=nil → metadata 列存 NULL → 前端 Metadata 为 null（符合类型契约）
	msg, err := a.conversations.AppendMessage(ctx, convID, conversation.RoleAssistant, conversation.KindMessage, content, meta)
	if err != nil {
		return nil, err
	}
	return json.Marshal(msg)
}

// Publish 满足 qa.Deps：推 SSE。
func (a *scanAdapter) Publish(ctx context.Context, convID string, payload []byte) error {
	return a.publisher.Publish(ctx, convID, payload)
}

// StartChatScan 满足 httpapi.ChatAPI：建会话 + 落用户首条消息 + **同步意图闸**。
//
// 分类必须留在请求路径：ChatResponse 的 task_id 是程序化调用方（e2e 触发器、
// API 集成）拿任务句柄的唯一通道，异步分发会让该字段结构性恒空（契约破裂，
// 消费方无从发现任务）。分类是单 token 的 light LLM 调用（秒级），同步代价
// 可忽略；真正慢的是 qa 回答生成与扫描执行——两者保持异步（原「GLM 慢时浏览器
// 干等几十秒」的病灶在慢回答，不在分类）。
func (a *scanAdapter) StartChatScan(ctx context.Context, brief string) (string, string, error) {
	conv, err := a.conversations.CreateConversation(ctx, briefTitle(brief), "")
	if err != nil {
		return "", "", fmt.Errorf("create conversation: %w", err)
	}
	convID := conv.ID
	if _, err := a.conversations.AppendMessage(ctx, convID, conversation.RoleUser, conversation.KindMessage, brief, nil); err != nil {
		return "", "", fmt.Errorf("append user message: %w", err)
	}

	kind, clsErr := a.classifyIntent(ctx, convID, brief)
	if clsErr != nil {
		return convID, "", clsErr
	}
	if kind != msgclass.KindAction {
		go a.answerChatAsync(convID, brief) //nolint:gosec // G118：回答独立于请求生命周期
		return convID, "", nil
	}

	taskID, _, err := a.createScan(ctx, brief, convID)
	if err != nil {
		return convID, "", fmt.Errorf("dispatch scan: %w", err)
	}
	if err := a.conversations.LinkTask(ctx, convID, taskID); err != nil {
		chatLog.Warn().Err(err).Str("conv", convID).Msg("link task 失败")
	}
	go a.genTitle(convID, brief) //nolint:gosec // G118：标题生成独立生命周期
	return convID, taskID, nil
}

// classifyIntent 用 light provider 判定消息意图。provider 不可用返回显式 error
// （入口方自行决定报错还是提示），不静默降级 qa。
func (a *scanAdapter) classifyIntent(ctx context.Context, convID, content string) (msgclass.MessageKind, error) {
	g, err := a.router.generator(ctx, "inspector") // light provider
	if err != nil {
		chatLog.Warn().Err(err).Str("conv", convID).Msg("意图分类 provider 失败")
		return msgclass.KindQA, fmt.Errorf("intent classify provider: %w", err)
	}
	return msgclass.Classify(ctx, g, content)
}

// answerChatAsync 纯聊天的异步回答：增量经 SSE 打字机流出，全文落库兜底。
// 回答生成慢（数十秒级），独立 context 不随请求结束被 cancel。
func (a *scanAdapter) answerChatAsync(convID, brief string) {
	ctx, cancel := context.WithTimeout(context.Background(), dispatchAnswerTimeout)
	defer cancel()
	if err := chat.New(a).Answer(ctx, convID, brief); err != nil {
		chatLog.Warn().Err(err).Str("conv", convID).Msg("纯聊天回答失败")
		a.publishNotice(ctx, convID, "（回答生成失败，请重试）")
	}
}

// publishNotice 落一条 assistant 提示消息并广播（异步分支失败/忙碌时给时间线的可见反馈，
// 替代原 409/500 错误路径——异步分支没有 HTTP 响应可承载错误）。
func (a *scanAdapter) publishNotice(ctx context.Context, convID, text string) {
	payload, err := a.AppendAssistant(ctx, convID, text, "")
	if err != nil {
		chatLog.Warn().Err(err).Str("conv", convID).Str("text", text).Msg("提示消息落库失败")
		return
	}
	_ = a.Publish(ctx, convID, payload)
}

// genTitle 用 light LLM 把 brief 总结成 ≤16 字的简短标题，回填 conversation.title。
// 异步调用（独立 context，不随请求结束被 cancel）；失败静默（保留 briefTitle 兜底）。
func (a *scanAdapter) genTitle(convID, brief string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	g, err := a.router.generator(ctx, "inspector") // light provider，便宜
	if err != nil {
		return
	}
	prompt := "把下面的渗透测试任务描述总结成一个不超过 16 字的简短中文标题，" +
		"突出目标和测试类型（如「DVWA SQL注入渗透」）。只输出标题本身，不要引号、不要解释。\n\n任务：" + brief
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
	_ = a.conversations.SetTitle(ctx, convID, title)
}

// briefTitle 取 brief 前 40 字（rune 安全，不截半个中文）作会话标题。
func briefTitle(brief string) string {
	const maxRunes = 40
	r := []rune(brief)
	if len(r) > maxRunes {
		return string(r[:maxRunes])
	}
	return brief
}
