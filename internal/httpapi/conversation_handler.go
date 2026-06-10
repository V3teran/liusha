package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/scenario"
)

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
		// 下发 SSE 鉴权 cookie（30min，HttpOnly+SameSite=Lax；同源部署故不需 SameSite=None）。
		// Secure 由 c.SetCookie 第 6 参控制；dev http 同源下设 false 也能种，prod 反代 https 应 true。
		// secure 由 cmd/api 读 LIUSHA_COOKIE_SECURE 经 Deps.CookieSecure 注入。
		if len(streamSecret) > 0 {
			const ttl = 30 * 60 // 秒
			tok := signStreamToken(streamSecret, convID, time.Now().Add(ttl*time.Second))
			c.SetSameSite(http.SameSiteLaxMode)
			c.SetCookie(streamCookieName, tok, ttl, "/conversations", "", secure, true)
		}
		c.JSON(http.StatusOK, ChatResponse{ConversationID: convID, ScanID: scanID})
	}
}

// FollowUpAPI 是多轮动作续接的窄接口（cmd/api 注入 adapter 实现）。
type FollowUpAPI interface {
	// GetConversationScan 返回对话关联的 scanID 与该 scan 当前状态。
	GetConversationScan(ctx context.Context, convID string) (scanID, scanStatus string, err error)
	// AppendUserMessage 把用户追加消息落库（KindMessage / RoleUser）。
	AppendUserMessage(ctx context.Context, convID, content string) error
	// FollowUpScan 在已有 scan 上重开+入队续接 run，返回 hunterID。
	FollowUpScan(ctx context.Context, scanID, convID, scenarioID, brief string) (string, error)
}

// FollowUpRequest 是 POST /conversations/:id/messages 请求体。
type FollowUpRequest struct {
	Content string `json:"content"`
}

// followUpHandler 处理追加消息：落消息 → scan 空闲则触发续接 run，正在跑则 409 busy（队列 Plan 2）。
func followUpHandler(api FollowUpAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Param("id")
		var req FollowUpRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Content == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "content 不能为空"})
			return
		}
		scanID, status, err := api.GetConversationScan(c.Request.Context(), convID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "conversation 不存在"})
			return
		}
		if err := api.AppendUserMessage(c.Request.Context(), convID, req.Content); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if status == "active" {
			c.JSON(http.StatusConflict, gin.H{"error": "扫描进行中，停止后再发", "busy": true})
			return
		}
		if _, err := api.FollowUpScan(c.Request.Context(), scanID, convID, "", req.Content); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"intent": "action", "scan_id": scanID})
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

		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no") // 禁 nginx 缓冲，保证实时

		flusher, ok := c.Writer.(http.Flusher)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
			return
		}
		// SSE 长连接：清除 http.Server.WriteTimeout，否则数十秒后连接被切断。
		// gin responseWriter 实现 Unwrap，ResponseController 可达底层 conn；不支持则降级。
		_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})

		// 先订阅，避免补历史与订阅之间漏事件。
		sub := stream.Subscribe(ctx, convID)
		defer sub.Close()

		// 补历史（after_seq / Last-Event-ID 之后）。
		lastSeq := parseAfterSeq(c)
		history, err := convs.ListMessages(ctx, convID, lastSeq, 2000)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		for _, m := range history {
			writeSSEMessage(c.Writer, m.Seq, m)
			lastSeq = m.Seq
		}
		flusher.Flush()

		// 实时：按 seq>lastSeq 去重转发。
		for {
			select {
			case <-ctx.Done():
				return
			case payload, ok := <-sub.Events():
				if !ok {
					return
				}
				var m conversation.Message
				if err := json.Unmarshal(payload, &m); err != nil {
					continue
				}
				if m.Seq <= lastSeq {
					continue // 与补历史重叠，跳过
				}
				writeSSERaw(c.Writer, m.Seq, payload)
				lastSeq = m.Seq
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
