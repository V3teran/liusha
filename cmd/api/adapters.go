package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/chat"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/llm"
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

// List 列出最近的 task → httpapi.TaskSummary（前端下拉/列表）。各场景混列，按 created_at desc。
func (a taskAPIAdapter) List(ctx context.Context, limit int) ([]httpapi.TaskSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	tasks, err := a.tasks.List(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	out := make([]httpapi.TaskSummary, 0, len(tasks))
	for _, t := range tasks {
		// 输入已统一（brief 主 + target_host 派生，见 D5）：始终输出两字段，不按场景分形状。
		scopeJSON, _ := json.Marshal(map[string]string{"brief": t.Brief, "target_host": t.TargetHost})
		s := httpapi.TaskSummary{
			ID:     t.ID,
			Scope:  string(scopeJSON),
			Status: string(t.Status),

			CreatedAt:    t.CreatedAt.Format(time.RFC3339),
			ErrorMessage: t.ErrorMessage,
		}
		if t.EndedAt != nil {
			s.EndedAt = t.EndedAt.Format(time.RFC3339)
		}
		out = append(out, s)
	}
	return out, nil
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

	router    *llm.Router           // 多轮：意图分类 + 问答（light provider）
	findings  *finding.Store        // 问答读 task 黑板 finding
	publisher *scanstream.Publisher // 问答回答 publish SSE

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
	return a.expandItem(ctx, asg.ID, brief, conversationID)
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
	payloadInput, err := json.Marshal(map[string]string{"brief": brief})
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	return a.enqueuePlannerRun(ctx, taskID, conversationID, payloadInput)
}

// enqueuePlannerRun 建 planner agent run 并入队——expandItem（首跑）与 FollowUp（续跑）
// 共用同一份入队语义。
//
// planner 跑 ~4h，asynq 默认 retry 25 次 → 4 天死循环；且 retry 接管时新 runner 进程
// parentRegistries 是空的，PreDoneCheck 永放行，旧 PG exploitation 留 status=running 僵尸态。
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
	if _, _, err := a.enq.Enqueue(ctx, worker.RoleExecutor, worker.Payload{
		AgentRunID:     tid,
		TaskID:         taskID,
		ConversationID: conversationID, // 阶段B：会话发起时非空 → runner 发过程事件
		// 场景 code：runner 据此数据驱动派发引擎/操作员编排
		Input: payloadInput,
		Role:  worker.RoleExecutor,
	}, asynq.MaxRetry(0), asynq.Timeout(a.maxRunTimeout)); err != nil {
		return "", fmt.Errorf("enqueue: %w", err)
	}
	return tid, nil
}

// AbortConversationScan 满足 httpapi.AbortAPI：abort 会话关联的 task。
func (a *scanAdapter) AbortConversationScan(ctx context.Context, convID string) error {
	conv, err := a.conversations.GetConversation(ctx, convID)
	if err != nil {
		return err
	}
	if conv.TaskID == "" {
		return fmt.Errorf("conversation 无关联 task")
	}
	return a.tasks.Abort(ctx, conv.TaskID, "用户停止")
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

// HandleMessage 满足 httpapi.FollowUpAPI：落 user 消息 → 意图闸 → 分流。
//
// 两类会话共用一道意图闸（light LLM 判 action/qa）：
//   - 已绑 task 的会话：action → Reopen 同一 task 续接（沿用原场景，finding 累积）；
//     qa → qa.Answer 就已挖 finding 提问。
//     qa/闲聊 → chat.Answer 通用助手回答（不读 finding、不下发 task）。
//
// 升级为 action 时用于建 task；已绑 task 的会话续接沿用原 task 场景，忽略本参数。
func (a *scanAdapter) HandleMessage(ctx context.Context, convID, content string) (string, bool, error) {
	conv, err := a.conversations.GetConversation(ctx, convID)
	if err != nil {
		return "", false, err
	}
	if _, err := a.conversations.AppendMessage(ctx, convID, conversation.RoleUser, conversation.KindMessage, content, nil); err != nil {
		return "", false, err
	}

	// 意图分流（action/qa）对纯聊天、active、passive 通用：判定走 light LLM。
	g, err := a.router.For(ctx, "inspector") // light provider
	if err != nil {
		return "", false, err
	}
	isAction := msgclass.Classify(ctx, g, content) == msgclass.KindAction

	// 纯聊天会话（无 task）：升级为 action 时用当前场景建 task；否则通用助手回答。
	// 升级路径：createScan 已把 brief 作为首个 run 的 payload 入队，无需再 FollowUp。
	if conv.TaskID == "" {
		if !isAction {
			if err := chat.New(a).Answer(ctx, convID, content); err != nil {
				return "", false, err
			}
			return "qa", false, nil
		}
		taskID, _, err := a.createScan(ctx, content, convID)
		if err != nil {
			return "", false, err
		}
		if err := a.conversations.LinkTask(ctx, convID, taskID); err != nil {
			return "", false, fmt.Errorf("link task: %w", err)
		}
		go a.genTitle(convID, content) //nolint:gosec // G118：标题生成独立于请求生命周期，进程级后台任务
		return "action", false, nil
	}

	// 已绑 task 的会话：action → 续接同一 task（finding 累积，不新建）；qa → 就已有 finding 提问。
	tk, err := a.tasks.GetByID(ctx, conv.TaskID)
	if err != nil {
		return "", false, err
	}
	if isAction {
		if tk.Status == task.StatusActive {
			return "action", true, nil // 忙：agent 在跑，本轮指导经 conversationContext 下次读到
		}
		// FollowUp 失败仅记录，不阻塞应答流程
		_, _ = a.FollowUp(ctx, conv.TaskID, convID, content)
		return "action", false, nil
	}
	// qa：就已有 finding/流量提问，各场景同一套问答
	if err := qa.New(a).Answer(ctx, convID, conv.TaskID, content); err != nil {
		return "", false, err
	}
	return "qa", false, nil
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

// Generate 满足 qa.Deps：调 light provider。
func (a *scanAdapter) Generate(ctx context.Context, msgs []llm.Message, tools []llm.ToolSchema) (llm.Result, error) {
	g, err := a.router.For(ctx, "inspector")
	if err != nil {
		return llm.Result{}, err
	}
	return g.Generate(ctx, msgs, tools)
}

// AppendAssistant 满足 qa.Deps：落 assistant 消息，返回 SSE payload。
func (a *scanAdapter) AppendAssistant(ctx context.Context, convID, content string) ([]byte, error) {
	msg, err := a.conversations.AppendMessage(ctx, convID, conversation.RoleAssistant, conversation.KindMessage, content, nil)
	if err != nil {
		return nil, err
	}
	return json.Marshal(msg)
}

// Publish 满足 qa.Deps：推 SSE。
func (a *scanAdapter) Publish(ctx context.Context, convID string, payload []byte) error {
	return a.publisher.Publish(ctx, convID, payload)
}

// CreateScan 满足 httpapi.ScanAPI（无会话的纯后台扫描入口）。
func (a *scanAdapter) CreateScan(ctx context.Context, brief string) (string, string, error) {
	return a.createScan(ctx, brief, "")
}

// StartChatScan 满足 httpapi.ChatAPI：建会话（记 brief）+ 落用户首条消息，然后过意图闸
// （light LLM 判 action/qa）——action 才发起扫描。返回 conversationID 供前端订阅 SSE。
//
// 首次对话与追加消息（HandleMessage）走同一道意图闸：避免把闲聊/答疑误判成动作而白烧一次扫描。
func (a *scanAdapter) StartChatScan(ctx context.Context, brief string) (string, string, error) {
	conv, err := a.conversations.CreateConversation(ctx, briefTitle(brief), "")
	if err != nil {
		return "", "", fmt.Errorf("create conversation: %w", err)
	}
	convID := conv.ID
	if _, err := a.conversations.AppendMessage(ctx, convID, conversation.RoleUser, conversation.KindMessage, brief, nil); err != nil {
		return "", "", fmt.Errorf("append user message: %w", err)
	}

	// 消息分流闸：light LLM 判 action/qa（解析失败默认 qa，见 msgclass.Classify）。
	g, err := a.router.For(ctx, "inspector") // light provider
	if err != nil {
		return "", "", fmt.Errorf("msgclass provider: %w", err)
	}
	if msgclass.Classify(ctx, g, brief) != msgclass.KindAction {
		// 纯聊天：不下发 task，用通用助手回答（落 assistant 消息 + SSE，前端补历史即见）。
		// 失败不阻断会话创建——会话与用户消息已落库，回答缺失可由用户再发一句触发。
		if err := chat.New(a).Answer(ctx, convID, brief); err != nil {
			chatLog.Warn().Err(err).Str("conv", convID).Msg("纯聊天回答失败")
		}
		return convID, "", nil
	}

	taskID, _, err := a.createScan(ctx, brief, convID)
	if err != nil {
		return "", "", err
	}
	if err := a.conversations.LinkTask(ctx, convID, taskID); err != nil {
		return "", "", fmt.Errorf("link task: %w", err)
	}
	// 异步生成智能标题（light LLM 把 brief 总结成短标题）——不阻塞会话创建响应；
	// 失败则保留 briefTitle 截断兜底。前端下次 refresh 列表即见新标题。
	go a.genTitle(convID, brief) //nolint:gosec // G118：标题生成独立于请求生命周期
	return convID, taskID, nil
}

// genTitle 用 light LLM 把 brief 总结成 ≤16 字的简短标题，回填 conversation.title。
// 异步调用（独立 context，不随请求结束被 cancel）；失败静默（保留 briefTitle 兜底）。
func (a *scanAdapter) genTitle(convID, brief string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	g, err := a.router.For(ctx, "inspector") // light provider，便宜
	if err != nil {
		return
	}
	prompt := "把下面的渗透测试任务描述总结成一个不超过 16 字的简短中文标题，" +
		"突出目标和测试类型（如「DVWA SQL注入渗透」）。只输出标题本身，不要引号、不要解释。\n\n任务：" + brief
	res, err := g.Generate(ctx, []llm.Message{{Role: llm.RoleUser, Content: prompt}}, nil)
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
