package middleware

import (
	"context"
	"encoding/json"

	"github.com/V3teran/liusha/internal/toolfx"
	"github.com/V3teran/liusha/internal/react"
)

// DoneValidate 工厂：返回仅在 name=="done" 时拦截的 Middleware。
//
// 黑客松借鉴共识 C（Done 系统层裁决）：让 Skill 自己声明"算不算完成"，而非让 LLM 自吹。
// 不通过时抛 react.ErrDoneNotReady{Missing}，runtime 把 missing 喂回 Observer/LLM。
//
// validator==nil 时按 AlwaysOK 处理（防御 NPE）。
func DoneValidate(validator toolfx.DoneValidator) toolfx.Middleware {
	if validator == nil {
		validator = toolfx.AlwaysOK{}
	}
	return func(next toolfx.ActionExecutor) toolfx.ActionExecutor {
		return func(ctx context.Context, name string, args json.RawMessage) (toolfx.Result, error) {
			if name != "done" {
				return next(ctx, name, args)
			}
			ok, missing := validator.CanDone(ctx, args)
			if !ok {
				return toolfx.Result{}, react.ErrDoneNotReady{Missing: missing}
			}
			return next(ctx, name, args)
		}
	}
}
