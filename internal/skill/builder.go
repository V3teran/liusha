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

	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/react"
)

// Builder 为某个 skill 装配子 ReAct Config。
//
// scanner 启动时按 skill 名注册到 Delegate.Builders（如 "vuln-web-bac" → bac.NewSubBuilder）。
type Builder func(ctx context.Context, params BuilderParams) (react.Config, error)

// BuilderParams 子 ReAct 启动参数（由 delegate 工具从 LLM 调用参数解析后传入）。
//
// Observer 由调用方注入（一般是主 ReAct 的同实例 observer），
// 让子 ReAct 也享受过程判官（每 5 步评估、abort/steer），跟主 ReAct 行为一致。
//
// CredentialLocations 由主 ReAct 上游 classify_traffic 工具识别后透传，描述
// "原始流量在哪些位置携带凭证"——子 ReAct 用它构造带占位 token 的 anonymous 假认证。
// 为空时，子 ReAct 退化为旧行为（anonymous 不带任何 credential）。
type BuilderParams struct {
	EngagementID        string
	FlowID              int64
	Host                string
	URL                 string
	Method              string
	LLM                 llm.Generator
	Observer            react.Observer
	CredentialLocations []credential.CredentialLocation
}
