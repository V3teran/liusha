package logx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/logx"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFromContext_AttachesTraceID(t *testing.T) {
	var buf bytes.Buffer
	logx.SetWriterForTest(&buf)
	t.Cleanup(func() { logx.SetWriterForTest(os.Stderr) })

	ctx := logx.WithTraceID(context.Background(), "abc-123")
	logx.From(ctx).Info().Str("event", "hello").Send()

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "abc-123", got["trace_id"])
	assert.Equal(t, "hello", got["event"])
}

func TestDevelopmentMode_PanicsWithoutTraceID(t *testing.T) {
	logx.SetDevelopmentStrictForTest(true)
	t.Cleanup(func() { logx.SetDevelopmentStrictForTest(false) })

	assert.Panics(t, func() {
		logx.From(context.Background()).Info().Msg("no trace")
	})
}

func TestTraceMiddleware_PropagatesW3CTraceparent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(logx.TraceMiddleware())
	var capturedTrace string
	r.GET("/ping", func(c *gin.Context) {
		capturedTrace = logx.TraceIDFromContext(c.Request.Context())
		c.Status(204)
	})

	req := httptest.NewRequest("GET", "/ping", nil)
	req.Header.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, 204, w.Code)
	assert.Equal(t, "0af7651916cd43dd8448eb211c80319c", capturedTrace)
	assert.True(t, strings.HasPrefix(w.Header().Get("traceparent"), "00-0af7651916cd43dd8448eb211c80319c-"))
}

func TestTraceMiddleware_GeneratesTraceIDWhenMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(logx.TraceMiddleware())
	var capturedTrace string
	r.GET("/ping", func(c *gin.Context) {
		capturedTrace = logx.TraceIDFromContext(c.Request.Context())
		c.Status(204)
	})

	req := httptest.NewRequest("GET", "/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Len(t, capturedTrace, 32)
}
