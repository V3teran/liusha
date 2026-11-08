package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/attachment"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/logx"
)

// ErrConversationTaskActive 表示会话关联任务仍在进行。
// 业界做法（GitHub Actions / 云控制台）：活跃作业不许删，只能先 Cancel。否则删了会话 = 扫描脱缰
// 后台跑、UI 再停不掉、还在烧 token 的孤儿。Deleter 实现据此返回，handler 映射为 409。
var ErrConversationTaskActive = errors.New("会话关联任务进行中，请先停止再删除")

// sseLog 是 SSE 流的诊断 logger（连接/订阅/补历史/实时转发/退出全链路）。
// 包级构建一次（避免每连接触发 logx 全局写入的 race）。LIUSHA_LOG_LEVEL=debug 看逐帧。
var sseLog = logx.New("httpapi.sse")

// conversation_handler.go：会话式平台的 HTTP 入口。
//
//   - POST /chat            发起会话扫描：建 conversation + task（assignment 派生），入队带 conversationID
//   - GET  /conversations            列表（UI 侧栏）
//   - GET  /conversations/:id/messages   回看（afterSeq 增量）
//   - GET  /conversations/:id/stream     SSE：补历史 + 实时推 agent 过程事件
//
// 认证统一走 RequireAuth 的 liusha_session 会话 Cookie：SSE 的 EventSource 同源
// 自动带 Cookie，与普通端点同一路径，无特例。

// ChatAPI 是发起会话扫描的窄接口（cmd/api 注入 *dispatch.Service：建 conversation + task + 入队带 conversationID）。
type ChatAPI interface {
	StartChatTask(ctx context.Context, brief string, attachments []attachment.Ref) (conversationID, taskID string, err error)
}

// ConversationsAPI 是会话/消息读取窄接口（*conversation.Store 自动满足）。
type ConversationsAPI interface {
	ListConversations(ctx context.Context, limit, offset int, source string) ([]conversation.Conversation, bool, error)
	CountConversations(ctx context.Context, source string) (int, error)
	ListMessages(ctx context.Context, conversationID string, afterSeq int64, limit int) ([]conversation.Message, error)
}

// EventSubscription 是一次会话事件订阅（cmd/api 用 convstream.Subscription 适配）。
type EventSubscription interface {
	Events() <-chan []byte
	Close() error
}

// EventStream 订阅某会话的实时事件 channel（cmd/api 注入 redis-backed 适配器）。
type EventStream interface {
	Subscribe(ctx context.Context, conversationID string) EventSubscription
}

// ChatRequest 是 POST /chat 请求体。Attachments 是随首条消息提交的附件引用
// （上传端点已落位，服务端按会话合法根逐条校验后随任务落 inbox）。
type ChatRequest struct {
	Brief       string           `json:"brief"`
	Attachments []attachment.Ref `json:"attachments"`
}

// maxAttachmentsPerMessage 是单条消息携带的附件数上界。扫描链路的 payload
// 只对用户文本做 rune 钳制，附件清单在校验之后拼接、直进 planner prompt
// ——条数在这里收口，有界是清单机制的前提。
const maxAttachmentsPerMessage = 10

// ChatResponse 是 POST /chat 响应：前端用 conversation_id 订阅 SSE。
type ChatResponse struct {
	ConversationID string `json:"conversation_id"`
	TaskID         string `json:"task_id"`
}

// chatHandler 处理 POST /chat：校验 brief 非空，发起会话扫描。
func chatHandler(api ChatAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ChatRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
			return
		}
		if req.Brief == "" && len(req.Attachments) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "brief 不能为空"})
			return
		}
		if len(req.Attachments) > maxAttachmentsPerMessage {
			c.JSON(http.StatusBadRequest, gin.H{"error": "单条消息最多携带 10 个附件"})
			return
		}
		conversationID, taskID, err := api.StartChatTask(c.Request.Context(), req.Brief, req.Attachments)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, ChatResponse{ConversationID: conversationID, TaskID: taskID})
	}
}

// FollowUpAPI 处理会话追加消息：落 user 消息后立即返回，意图分类/分流在服务端异步完成。
// intent 固定返回 "accepted"。扫描进行中/暂停中收到 action 指令的反馈由服务端异步
// 落时间线提示（意图闸内判），不经同步响应——请求路径零 LLM 调用、零状态裁决。
type FollowUpAPI interface {
	HandleMessage(ctx context.Context, conversationID, content string, attachments []attachment.Ref) (intent string, err error)
}

// FollowUpRequest 是 POST /conversations/:id/messages 请求体（Attachments 语义同 ChatRequest）。
type FollowUpRequest struct {
	Content     string           `json:"content"`
	Attachments []attachment.Ref `json:"attachments"`
}

func followUpHandler(api FollowUpAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		conversationID := c.Param("id")
		var req FollowUpRequest
		if err := c.ShouldBindJSON(&req); err != nil || (req.Content == "" && len(req.Attachments) == 0) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "content required"})
			return
		}
		if len(req.Attachments) > maxAttachmentsPerMessage {
			c.JSON(http.StatusBadRequest, gin.H{"error": "单条消息最多携带 10 个附件"})
			return
		}
		intent, err := api.HandleMessage(c.Request.Context(), conversationID, req.Content, req.Attachments)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"intent": intent})
	}
}

// AbortAPI 停止会话关联扫描（cmd/api 注入）。
type AbortAPI interface {
	AbortConversationTask(ctx context.Context, conversationID string) error
}

// abortConversationHandler 处理 POST /conversations/:id/abort：停掉会话关联的运行中 task。
func abortConversationHandler(api AbortAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := api.AbortConversationTask(c.Request.Context(), c.Param("id")); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"aborted": true})
	}
}

// ConversationDeleter 删除会话及其消息（*conversation.Store 满足）。小接口、可选注册（同 Abort）。
type ConversationDeleter interface {
	DeleteConversation(ctx context.Context, id string) error
}

// deleteConversationHandler 处理 DELETE /conversations/:id：删会话+消息（不动 scan/finding 成果）。
// 关联任务仍在跑时返回 409（ErrConversationTaskActive）——后端兜底「先停后删」，前端守卫可被绕过，
// 不变量必须在服务端把守，否则单次删除就能制造停不掉的孤儿扫描。
func deleteConversationHandler(api ConversationDeleter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := api.DeleteConversation(c.Request.Context(), c.Param("id")); err != nil {
			if errors.Is(err, ErrConversationTaskActive) {
				c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "task_active": true})
				return
			}
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"deleted": true})
	}
}

// maxConversationTitleRunes 手动重命名标题上限（rune 计）。与 briefTitle 自动摘要的 40 留同量级余量。
const maxConversationTitleRunes = 80

// ConversationRenamer 重命名会话标题（*conversation.Store 满足）。小接口、可选注册（同 Deleter）。
type ConversationRenamer interface {
	SetTitle(ctx context.Context, id, title string) error
}

// renameConversationHandler 处理 PATCH /conversations/:id：改标题。
// body: {"title": "..."}。空 title 由 store 存 NULL（回落到首条消息摘要展示）。
func renameConversationHandler(api ConversationRenamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Title string `json:"title"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效请求体：" + err.Error()})
			return
		}
		title := strings.TrimSpace(body.Title)
		if len([]rune(title)) > maxConversationTitleRunes {
			c.JSON(http.StatusBadRequest, gin.H{"error": "标题过长"})
			return
		}
		if err := api.SetTitle(c.Request.Context(), c.Param("id"), title); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"title": title})
	}
}

// listConversationsHandler 处理 GET /conversations?limit=&offset=&_id=&source=：分页会话列表（UI 侧栏翻页）。
// _id / source 可选，空则不过滤——过滤下沉到 SQL，保证分页边界与「当前过滤下的
// 总条数」一致（若仍由前端在已分页的单页结果上再过滤，页码和条数会对不上）。
// source ∈ {direct,proxy}：前端「自主下发 / 代理模式」双 tab（纯聊天归 direct 侧，见 store）。
// has_more：本页拉满 limit+1 条时才可能有下一页（store 已裁剪到 limit，见 ListConversations）。
// maxConversationListLimit 与 conversation.Store maxListLimit 一致（两层钳制数值契约）。
const maxConversationListLimit = 500

func listConversationsHandler(api ConversationsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		source := c.Query("source")
		limit := parseLimit(c, 30)
		if limit > maxConversationListLimit {
			limit = maxConversationListLimit
		}
		convs, hasMore, err := api.ListConversations(c.Request.Context(), limit, parseOffset(c), source)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		total, err := api.CountConversations(c.Request.Context(), source)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"conversations": convs, "has_more": hasMore, "total": total})
	}
}

// messagesHandler 处理 GET /conversations/:id/messages：回看（after_seq 增量）。
func messagesHandler(api ConversationsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		conversationID := c.Param("id")
		msgs, err := api.ListMessages(c.Request.Context(), conversationID, parseAfterSeq(c), parseLimit(c, 500))
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		// nil 切片序列化为 null——零消息会话（种子/纯聊天）会让前端分页器在 null 上炸掉、
		// 整个会话打不开。归一为空数组（对齐 content-types/traffic 的口径）。
		if msgs == nil {
			msgs = []conversation.Message{}
		}
		c.JSON(http.StatusOK, gin.H{"messages": msgs})
	}
}

// streamHandler 处理 GET /conversations/:id/stream：SSE 推 agent 过程事件。
//
// 时序去重：先订阅（缓冲实时事件）→ 补历史到 lastSeq → 放出实时，按 seq>lastSeq 过滤。
// seq 单调，天然去重补历史与实时的重叠部分；断线重连带 Last-Event-ID / after_seq 续读。
func streamHandler(convs ConversationsAPI, stream EventStream) gin.HandlerFunc {
	return func(c *gin.Context) {
		conversationID := c.Param("id")
		ctx := c.Request.Context()
		afterSeq := parseAfterSeq(c)
		sseLog.Info().Str("conv", conversationID).Int64("after_seq", afterSeq).Msg("SSE 连接建立")

		// 1. 设置 SSE 响应头
		setupSSEHeaders(c)

		// 2. 验证 Flusher 支持
		flusher, ok := validateFlusher(c, conversationID)
		if !ok {
			return
		}

		// 3. 订阅实时事件（先订阅，避免补历史与订阅之间漏事件）
		sub := stream.Subscribe(ctx, conversationID)
		defer func() { _ = sub.Close() }()
		sseLog.Debug().Str("conv", conversationID).Msg("SSE: 已订阅 redis channel")

		// 4. 补发历史消息
		lastSeq, err := replayHistory(ctx, c, convs, conversationID, afterSeq, flusher)
		if err != nil {
			return
		}

		// 5. 实时转发事件流
		forwardRealtimeEvents(ctx, c, sub, conversationID, lastSeq, flusher)
	}
}

// writeSSEMessage 把 message 序列化成 SSE 帧（id=seq 支持 Last-Event-ID 重连）。
func writeSSEMessage(w http.ResponseWriter, seq int64, m conversation.Message) {
	payload, err := json.Marshal(m)
	if err != nil {
		return
	}
	writeSSERaw(w, seq, payload)
}

// writeSSERaw 写一帧 SSE：id: {seq}\ndata: {json}\n\n。
func writeSSERaw(w http.ResponseWriter, seq int64, payload []byte) {
	_, _ = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", seq, payload)
}

// writeSSEEvent 写一帧命名 SSE：event: {name}\ndata: {json}\n\n（无 id，不参与 Last-Event-ID 续传）。
// 用于流式推理增量等瞬时帧——前端按事件名单独监听，不混入默认 message 流的 seq 去重。
func writeSSEEvent(w http.ResponseWriter, name string, payload []byte) {
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, payload)
}

func parseLimit(c *gin.Context, def int) int {
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// parseOffset 取分页偏移（GET /conversations?offset=）。缺失/非法/负数 → 0（首页）。
func parseOffset(c *gin.Context) int {
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// parseAfterSeq 取增量起点：优先 Last-Event-ID（SSE 重连自动带），回退 after_seq query。
func parseAfterSeq(c *gin.Context) int64 {
	if v := c.GetHeader("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	if v := c.Query("after_seq"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return 0
}
