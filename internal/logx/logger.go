package logx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"
)

type ctxKey int

const traceIDKey ctxKey = iota

var (
	writerMu         sync.RWMutex
	writer io.Writer = os.Stderr
	strict atomic.Bool
)

// Init 设置全局日志级别，并在开发模式下开启严格校验：
// 严格模式下若 context 缺少 trace_id 将直接 panic，用于在开发阶段立刻暴露链路遗漏。
func Init(level string, developmentStrict bool) {
	lvl, err := zerolog.ParseLevel(level)
	if err != nil {
		lvl = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(lvl)
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	strict.Store(developmentStrict)
}

// SetDevelopmentStrictForTest 仅供测试切换严格模式。
func SetDevelopmentStrictForTest(v bool) { strict.Store(v) }

// SetWriterForTest 仅供测试替换底层 writer，用于捕获 JSON 日志做断言。
func SetWriterForTest(w io.Writer) {
	writerMu.Lock()
	defer writerMu.Unlock()
	writer = w
}

// WithTraceID 把 trace_id 注入 context；后续 From(ctx) 会自动把它打到日志字段里。
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

// TraceIDFromContext 读取 context 里的 trace_id；缺失则返回空串。
func TraceIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(traceIDKey).(string); ok {
		return v
	}
	return ""
}

// From 基于 context 构造一个带 trace_id 字段的 zerolog.Logger。
// 开发严格模式下，缺失 trace_id 视为上游链路缺陷，直接 panic。
func From(ctx context.Context) *zerolog.Logger {
	traceID := TraceIDFromContext(ctx)
	if traceID == "" && strict.Load() {
		panic("logx: 严格模式下禁止在无 trace_id 的 context 上写日志")
	}
	writerMu.RLock()
	w := writer
	writerMu.RUnlock()
	l := zerolog.New(w).With().Timestamp().Str("trace_id", traceID).Logger()
	return &l
}

// NewTraceID 生成 32 位十六进制 trace_id，符合 W3C Trace Context trace-id 段规范。
func NewTraceID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b[:])
}
