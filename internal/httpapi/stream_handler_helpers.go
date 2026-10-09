package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/V3teran/liusha/internal/conversation"
	"github.com/gin-gonic/gin"
)

// setupSSEHeaders 设置 SSE 必需的 HTTP 响应头
func setupSSEHeaders(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // 禁 nginx 缓冲，保证实时
}

// validateFlusher 验证 ResponseWriter 是否支持 Flush
func validateFlusher(c *gin.Context, convID string) (http.Flusher, bool) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		sseLog.Error().Str("conv", convID).Msg("SSE: ResponseWriter 不支持 Flusher")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return nil, false
	}
	// SSE 长连接：清除 http.Server.WriteTimeout，否则数十秒后连接被切断
	_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})
	return flusher, true
}

// replayHistory 补发历史消息（after_seq / Last-Event-ID 之后）
func replayHistory(ctx context.Context, c *gin.Context, convs ConversationsAPI, convID string, afterSeq int64, flusher http.Flusher) (int64, error) {
	history, err := convs.ListMessages(ctx, convID, afterSeq, 2000)
	if err != nil {
		sseLog.Error().Err(err).Str("conv", convID).Msg("SSE: 补历史失败")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return afterSeq, err
	}

	lastSeq := afterSeq
	for _, m := range history {
		writeSSEMessage(c.Writer, m.Seq, m)
		lastSeq = m.Seq
	}
	flusher.Flush()

	sseLog.Info().Str("conv", convID).Int("history", len(history)).Int64("last_seq", lastSeq).
		Msg("SSE: 补历史完成，进入实时转发")

	return lastSeq, nil
}

// forwardRealtimeEvents 实时转发事件流，按 seq>lastSeq 去重
func forwardRealtimeEvents(ctx context.Context, c *gin.Context, sub EventSubscription, convID string, lastSeq int64, flusher http.Flusher) {
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

			// 处理流式增量帧（delta）
			if isDeltaFrame(payload) {
				writeSSEEvent(c.Writer, "delta", payload)
				flusher.Flush()
				deltas++
				continue
			}

			// 处理普通消息帧
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

// isDeltaFrame 检查是否为流式推理增量帧
func isDeltaFrame(payload []byte) bool {
	var probe struct {
		Delta bool `json:"delta"`
	}
	_ = json.Unmarshal(payload, &probe)
	return probe.Delta
}
