// Package skill 此文件定义子 ReAct skill 装配的接口约定（Builder + BuilderParams），
// 与 Loader 同包：skill 包代表"skill 系统"——加载 SKILL.md（Card / Loader）
// 与装配可执行 react.Config（Builder）两个职责合并在一处。
//
// 解耦关系：
//   - tools/scan 只负责"按 skill 名调 Builder + 跑子 ReAct"
//   - builders/vuln/<kind> 实现具体 skill 的 Builder
//   - 两者通过 skill.Builder 类型解耦
package skill

import (
	"context"
	"encoding/json"

	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/react"
)

// Builder 为某个 skill 装配 ReAct Config。
//
// 单层 hunter agent：scanner 拉到 flow 直接调 hunter.NewBuilder 拿 react.Config 跑 react.Run。
type Builder func(ctx context.Context, params BuilderParams) (react.Config, error)

// BuilderParams hunter agent 启动参数。
//
// scanner main loop 接到 flow 后填充：完整 raw 流量 (request + response) +
// host + LLM Generator + Reviewer。hunter agent 在 user prompt 一次性看到
// 全部材料（请求 + 响应），自由组合工具挖漏洞。
type BuilderParams struct {
	EngagementID string
	TaskID       string
	FlowID       int64
	Host         string
	URL          string
	Method       string
	LLM          llm.Generator
	Reviewer     react.Reviewer

	// 请求 raw（builder 拼到 user prompt）。
	RequestHeaders json.RawMessage
	RequestBody    []byte

	// 响应 raw：让 hunter 一次性看到完整流量，省去 LLM 再调 curl 拉响应的开销。
	ResponseStatus  int
	ResponseHeaders json.RawMessage
	ResponseBody    []byte
}
