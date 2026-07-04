package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/scenario"
)

// ErrConversationScanActive：对话关联的 active_scan 仍在跑，拒绝删除（先停后删）。
// 业界做法（GitHub Actions / 云控制台）：活跃作业不许删，只能先 Cancel。否则删了对话 = 扫描脱缰
// 后台跑、UI 再停不掉、还在烧 token 的孤儿。Deleter 实现据此返回，handler 映射为 409。
var ErrConversationScanActive = errors.New("对话关联扫描进行中，请先停止再删除")

// sseLog 是 SSE 流的诊断 logger（连接/订阅/补历史/实时转发/退出全链路）。
// 包级构建一次（避免每连接触发 logx 全局写入的 race）。LIUSHA_LOG_LEVEL=debug 看逐帧。
var sseLog = logx.New("httpapi.sse")

// conversation_handler.go：对话式平台（阶段B3/B4）的 HTTP 入口。
//
//   - POST /chat            发起对话扫描：建 conversation + active_scan，入队带 conversationID
//   - GET  /conversations            列表（UI 侧栏）
//   - GET  /conversations/:id/messages   回看（afterSeq 增量）
//   - GET  /conversations/:id/stream     SSE：补历史 + 实时推 agent 过程事件
//
// SSE 认证：当前走标准 X-API-Key header（curl/fetch 可带）。前端 EventSource 不能带自定义
// header——阶段D 前端用 fetch+ReadableStream 或 query-param token 解决，此处不动认证。

// ChatAPI 是发起对话扫描的窄接口（cmd/api 注入 adapter：建 conversation + scan + 入队带 convID）。
// roleID 是用户选的场景 role（空时 adapter 用默认 active role 兜底）。
type ChatAPI interface {
	StartChatScan(ctx context.Context, brief, roleID string) (conversationID, scanID string, err error)
}

// RolesAPI 列出可选场景 role（前端对话选择用）。*scenario 加载结果由 cmd/api 适配注入。
type RolesAPI interface {
	ListRoles() []scenario.Role
}

// roleDTO 是 GET /roles 的对外视图——只暴露选择所需字段，不含内部 SystemPrompt/SourceFile。
type roleDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Mode        string `json:"mode"`
}

// rolesHandler 处理 GET /roles：列出可选场景供前端选择。
func rolesHandler(api RolesAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		roles := api.ListRoles()
		out := make([]roleDTO, 0, len(roles))
		for _, r := range roles {
			out = append(out, roleDTO{ID: r.ID, Name: r.Name, Description: r.Description, Mode: string(r.Mode)})
		}
		c.JSON(http.StatusOK, gin.H{"roles": out})
	}
}

// ConversationsAPI 是对话/消息读取窄接口（*conversation.Store 自动满足）。
type ConversationsAPI interface {
	ListConversations(ctx context.Context, limit int) ([]conversation.Conversation, error)
	ListMessages(ctx context.Context, convID string, afterSeq int64, limit int) ([]conversation.Message, error)
}

// EventSubscription 是一次对话事件订阅（cmd/api 用 scanstream.Subscription 适配）。
type EventSubscription interface {
	Events() <-chan []byte
	Close() error
}

// EventStream 订阅某对话的实时事件 channel（cmd/api 注入 redis-backed 适配器）。
type EventStream interface {
	Subscribe(ctx context.Context, conversationID string) EventSubscription
}

// ChatRequest 是 POST /chat 请求体。
type ChatRequest struct {
	Brief  string `json:"brief"`
	RoleID string `json:"role_id"` // 场景 role（空时 adapter 用默认 active role 兜底）
}

// ChatResponse 是 POST /chat 响应：前端用 conversation_id 订阅 SSE。
type ChatResponse struct {
	ConversationID string `json:"conversation_id"`
	ScanID         string `json:"scan_id"`
}

// chatHandler 处理 POST /chat：校验 brief 非空，发起对话扫描，成功后下发 SSE 鉴权 cookie。
func chatHandler(api ChatAPI, streamSecret []byte, secure bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ChatRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
			return
		}
		if req.Brief == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "brief 不能为空"})
			return
		}
		convID, scanID, err := api.StartChatScan(c.Request.Context(), req.Brief, req.RoleID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		setStreamCookie(c, streamSecret, convID, secure)
		c.JSON(http.StatusOK, ChatResponse{ConversationID: convID, ScanID: scanID})
	}
}

// streamCookieTTLSeconds 是 SSE 鉴权 cookie 时效（秒）。前端打开会话/重连时按需重签，
// 故 TTL 只需覆盖单次连接周期，长扫描靠重连重签维持。
const streamCookieTTLSeconds = 30 * 60

// setStreamCookie 为某会话签发/刷新 SSE 鉴权 cookie（HttpOnly+SameSite=Lax 签名 token）。
//
// path 必须 "/"：前端经 vite proxy 带 /api 前缀（EventSource 连 /api/conversations/:id/stream），
// path="/conversations" 会让浏览器视角的 /api/conversations 不匹配 → SSE 不带 cookie → 鉴权失败。
// stream cookie 是 HttpOnly 签名 token，仅 SSE handler 校验，发到其他端点被忽略，path="/" 无害。
// secure 由 cmd/api 读 LIUSHA_COOKIE_SECURE 注入（dev http 设 false 也能种，prod https 应 true）。
func setStreamCookie(c *gin.Context, streamSecret []byte, convID string, secure bool) {
	if len(streamSecret) == 0 || convID == "" {
		return
	}
	tok := signStreamToken(streamSecret, convID, time.Now().Add(streamCookieTTLSeconds*time.Second))
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(streamCookieName, tok, streamCookieTTLSeconds, "/", "", secure, true)
}

// streamAuthHandler 处理 POST /conversations/:id/stream-auth（X-API-Key 保护）。
//
// 把"流鉴权"从"会话创建（/chat）"解耦：前端打开任意会话前、SSE 断线重连前调本端点，
// 拿到/刷新该会话的 stream cookie 再开 EventSource。根治"打开旧会话/长扫描/重连"
// 实时推送失效（旧实现只在 /chat 下发一次性 30min cookie）。
func streamAuthHandler(streamSecret []byte, secure bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "id required"})
			return
		}
		if len(streamSecret) == 0 {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "stream 鉴权未启用"})
			return
		}
		setStreamCookie(c, streamSecret, id, secure)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// FollowUpAPI 处理对话追加消息：内部判意图（action/qa）+ 落消息 + 分流。
// 返回 intent（"action"|"qa"）、busy（action 但扫描进行中 → 应排队/拒绝）、err。
type FollowUpAPI interface {
	HandleMessage(ctx context.Context, convID, content string) (intent string, busy bool, err error)
}

// FollowUpRequest 是 POST /conversations/:id/messages 请求体。
type FollowUpRequest struct {
	Content string `json:"content"`
}

func followUpHandler(api FollowUpAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Param("id")
		var req FollowUpRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Content == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "content 不能为空"})
			return
		}
		intent, busy, err := api.HandleMessage(c.Request.Context(), convID, req.Content)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if busy {
			c.JSON(http.StatusConflict, gin.H{"error": "扫描进行中，停止后再发", "busy": true, "intent": intent})
			return
		}
		c.JSON(http.StatusOK, gin.H{"intent": intent})
	}
}

// AbortAPI 停止对话关联扫描（cmd/api 注入）。
type AbortAPI interface {
	AbortConversationScan(ctx context.Context, convID string) error
}

// abortConversationHandler 处理 POST /conversations/:id/abort：停掉对话关联的 active_scan。
func abortConversationHandler(api AbortAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := api.AbortConversationScan(c.Request.Context(), c.Param("id")); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"aborted": true})
	}
}

// ConversationDeleter 删除对话及其消息（*conversation.Store 满足）。小接口、可选注册（同 Abort）。
type ConversationDeleter interface {
	DeleteConversation(ctx context.Context, id string) error
}

// deleteConversationHandler 处理 DELETE /conversations/:id：删对话+消息（不动 scan/finding 成果）。
// 关联扫描仍在跑时返回 409（ErrConversationScanActive）——后端兜底「先停后删」，前端守卫可被绕过，
// 不变量必须在服务端把守，否则单次删除就能制造停不掉的孤儿扫描。
func deleteConversationHandler(api ConversationDeleter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := api.DeleteConversation(c.Request.Context(), c.Param("id")); err != nil {
			if errors.Is(err, ErrConversationScanActive) {
				c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "scan_active": true})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"deleted": true})
	}
}

// maxConversationTitleRunes 手动重命名标题上限（rune 计）。与 briefTitle 自动摘要的 40 留同量级余量。
const maxConversationTitleRunes = 80

// ConversationRenamer 重命名对话标题（*conversation.Store 满足）。小接口、可选注册（同 Deleter）。
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"title": title})
	}
}

// listConversationsHandler 处理 GET /conversations：最近活跃对话列表。
func listConversationsHandler(api ConversationsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		convs, err := api.ListConversations(c.Request.Context(), parseLimit(c, 50))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"conversations": convs})
	}
}

// messagesHandler 处理 GET /conversations/:id/messages：回看（after_seq 增量）。
func messagesHandler(api ConversationsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Param("id")
		msgs, err := api.ListMessages(c.Request.Context(), convID, parseAfterSeq(c), parseLimit(c, 500))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
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
		convID := c.Param("id")
		ctx := c.Request.Context()
		afterSeq := parseAfterSeq(c)
		sseLog.Info().Str("conv", convID).Int64("after_seq", afterSeq).Msg("SSE 连接建立")

		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no") // 禁 nginx 缓冲，保证实时

		flusher, ok := c.Writer.(http.Flusher)
		if !ok {
			sseLog.Error().Str("conv", convID).Msg("SSE: ResponseWriter 不支持 Flusher")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
			return
		}
		// SSE 长连接：清除 http.Server.WriteTimeout，否则数十秒后连接被切断。
		// gin responseWriter 实现 Unwrap，ResponseController 可达底层 conn；不支持则降级。
		_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})

		// 先订阅，避免补历史与订阅之间漏事件。
		sub := stream.Subscribe(ctx, convID)
		defer sub.Close()
		sseLog.Debug().Str("conv", convID).Msg("SSE: 已订阅 redis channel")

		// 补历史（after_seq / Last-Event-ID 之后）。
		lastSeq := afterSeq
		history, err := convs.ListMessages(ctx, convID, lastSeq, 2000)
		if err != nil {
			sseLog.Error().Err(err).Str("conv", convID).Msg("SSE: 补历史失败")
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		for _, m := range history {
			writeSSEMessage(c.Writer, m.Seq, m)
			lastSeq = m.Seq
		}
		flusher.Flush()
		sseLog.Info().Str("conv", convID).Int("history", len(history)).Int64("last_seq", lastSeq).
			Msg("SSE: 补历史完成，进入实时转发")

		// 实时：按 seq>lastSeq 去重转发。
		var live, deltas, dups int
		for {
			select {
			case <-ctx.Done():
				sseLog.Info().Str("conv", convID).Int("live", live).Int("deltas", deltas).Int("dups", dups).
					Msg("SSE: 客户端断开（ctx done）")
				return
			case payload, ok := <-sub.Events():
				if !ok {
					sseLog.Warn().Str("conv", convID).Int("live", live).Int("deltas", deltas).
						Msg("SSE: redis 订阅 channel 关闭")
					return
				}
				// 流式推理增量：瞬时帧（无 seq、不落库），写命名事件 event:delta，前端单独累积，不参与 seq 去重。
				var probe struct {
					Delta bool `json:"delta"`
				}
				if json.Unmarshal(payload, &probe); probe.Delta {
					writeSSEEvent(c.Writer, "delta", payload)
					flusher.Flush()
					deltas++
					continue
				}
				var m conversation.Message
				if err := json.Unmarshal(payload, &m); err != nil {
					sseLog.Warn().Err(err).Str("conv", convID).Msg("SSE: 实时帧 unmarshal 失败，跳过")
					continue
				}
				if m.Seq <= lastSeq {
					dups++
					continue // 与补历史重叠，跳过
				}
				writeSSERaw(c.Writer, m.Seq, payload)
				lastSeq = m.Seq
				live++
				sseLog.Debug().Str("conv", convID).Int64("seq", m.Seq).Str("kind", string(m.Kind)).
					Str("role", string(m.Role)).Msg("SSE: 实时转发一帧")
				flusher.Flush()
			}
		}
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
	fmt.Fprintf(w, "id: %d\ndata: %s\n\n", seq, payload)
}

// writeSSEEvent 写一帧命名 SSE：event: {name}\ndata: {json}\n\n（无 id，不参与 Last-Event-ID 续传）。
// 用于流式推理增量等瞬时帧——前端按事件名单独监听，不混入默认 message 流的 seq 去重。
func writeSSEEvent(w http.ResponseWriter, name string, payload []byte) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, payload)
}

func parseLimit(c *gin.Context, def int) int {
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
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
