package middleware

import (
	"context"
	"encoding/json"

	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/toolfx"
)

// DoneSuccessHook 在 done 校验通过后被调用（done 工具实际执行前）。
// 用途：done 通过后的轻量清理/审计（v1.2 收尾后已无内置消费者，调用方传 nil 即可）。
// hook 内错误自行 swallow，不应影响 done 路径。
type DoneSuccessHook func(ctx context.Context, args json.RawMessage)

// DoneValidate 工厂：返回仅在 name=="done" 时拦截的 Middleware。
//
// 黑客松借鉴共识 C（Done 系统层裁决）：让 Skill 自己声明"算不算完成"，而非让 LLM 自吹。
// 不通过时抛 react.ErrDoneNotReady{Missing}，runtime 把 missing 喂回 Observer/LLM。
//
// validator==nil 时按 AlwaysOK 处理（防御 NPE）。
// onSuccess==nil 时跳过钩子；非 nil 时在校验通过、forward 到 next 之前调用。
func DoneValidate(validator toolfx.DoneValidator, onSuccess DoneSuccessHook) toolfx.Middleware {
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
			if onSuccess != nil {
				onSuccess(ctx, args)
			}
			return next(ctx, name, args)
		}
	}
}
