package logx

import (
	"context"
	"errors"

	"github.com/rs/zerolog"
)

// ErrorUnlessCanceled 返回记录 err 的日志事件：上下文取消引起的错误（任务终止、进程
// 停止时在途的查询与 LLM 调用）是正常收尾，记 debug；其余记 error。超时不属于取消，
// 照常记 error。
func ErrorUnlessCanceled(l zerolog.Logger, err error) *zerolog.Event {
	if errors.Is(err, context.Canceled) {
		return l.Debug()
	}
	return l.Error()
}
