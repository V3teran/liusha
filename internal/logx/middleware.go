package logx

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/gin-gonic/gin"
)

// TraceMiddleware 从 W3C `traceparent` 请求头里提取 trace_id；若缺失或格式不合法则新生成一个。
// 随后把 trace_id 塞入 request context，并把 `traceparent` 回写到响应头，
// 以便下游跨服务调用继续传递同一条链路。
func TraceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID, traceparent := extractOrMint(c.GetHeader("traceparent"))
		ctx := WithTraceID(c.Request.Context(), traceID)
		c.Request = c.Request.WithContext(ctx)
		c.Header("traceparent", traceparent)
		c.Next()
	}
}

// extractOrMint 解析 W3C traceparent；格式不合法时生成新的 trace_id 并构造 traceparent 回传。
func extractOrMint(header string) (string, string) {
	parts := strings.Split(header, "-")
	if len(parts) == 4 && len(parts[1]) == 32 {
		return parts[1], header
	}
	traceID := NewTraceID()
	spanID := randHex(8)
	return traceID, "00-" + traceID + "-" + spanID + "-01"
}

// randHex 生成 n 字节的随机十六进制字符串（长度为 2n）。
func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
