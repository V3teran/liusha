package toolfx

import (
	"context"
	"encoding/json"
)

// DoneValidator 是 Skill 注册的"完成裁决器"：done_validate 中间件在 name=="done" 时调用它。
//
// 借鉴黑客松共识 C：Done 系统层裁决（不是 LLM 自吹）。
// 返回 missing 让 runtime/Observer 把"还缺啥"显式喂回 LLM。
type DoneValidator interface {
	CanDone(ctx context.Context, args json.RawMessage) (ok bool, missing []string)
}

// AlwaysOK 是默认的 DoneValidator——每次都放行，用于不需要严格校验的 Skill。
type AlwaysOK struct{}

// CanDone 总是返回 (true, nil)。
func (AlwaysOK) CanDone(context.Context, json.RawMessage) (bool, []string) {
	return true, nil
}
