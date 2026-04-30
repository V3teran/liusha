package middleware

import (
	"context"
	"encoding/json"

	"github.com/V3teran/liusha/internal/tool"
	"github.com/V3teran/liusha/internal/react"
)

// DoneValidate 工厂：返回仅在 name=="done" 时拦截的 Middleware。
//
// 黑客松借鉴共识 C（Done 系统层裁决）：让 Skill 自己声明"算不算完成"，而非让 LLM 自吹。
// 不通过时抛 react.ErrDoneNotReady{Missing}，runtime 把 missing 喂回 Observer/LLM。
//
// validator==nil 时按 AlwaysOK 处理（防御 NPE）。
func DoneValidate(validator tool.DoneValidator) tool.Middleware {
	if validator == nil {
		validator = tool.AlwaysOK{}
	}
	return func(next tool.ActionExecutor) tool.ActionExecutor {
		return func(ctx context.Context, name string, args json.RawMessage) (tool.Result, error) {
			if name != "done" {
				return next(ctx, name, args)
			}
			ok, missing := validator.CanDone(ctx, args)
			if !ok {
				return tool.Result{}, react.ErrDoneNotReady{Missing: missing}
			}
			return next(ctx, name, args)
		}
	}
}
