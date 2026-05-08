package middleware

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// observeLog 包级 logger（与 timeout/result_compress 同模式：避免每次调用 logx.New）。
var observeLog zerolog.Logger = logx.New("toolruntime.observe")

// Observe 给每个 tool Execute 加 enter/exit 关键步骤埋点。
//
// 关键事实：name / args 长度 / 耗时 / err 状态 / output 长度 / done 信号。
// 放最外层（time/compress/done_validate 之外），保证整次调用全程时间都被计在内。
func Observe() toolfx.Middleware {
	return func(next toolfx.ActionExecutor) toolfx.ActionExecutor {
		return func(ctx context.Context, name string, args json.RawMessage) (toolfx.Result, error) {
			start := time.Now()
			observeLog.Info().
				Str("tool", name).
				Int("args_bytes", len(args)).
				Msg("tool exec ▶ enter")

			res, err := next(ctx, name, args)
			dur := time.Since(start)

			ev := observeLog.Info()
			if err != nil {
				ev = observeLog.Warn().Err(err)
			}
			ev.Str("tool", name).
				Dur("duration", dur).
				Int("output_bytes", len(res.Output)).
				Bool("done", res.Done).
				Msg("tool exec ◀ exit")
			return res, err
		}
	}
}
