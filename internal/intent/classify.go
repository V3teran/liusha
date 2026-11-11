// Package intent 用便宜 LLM 判定用户会话消息的类别：动作（触发扫描）还是问答（读结果回答）。
package intent

import (
	"context"
	"fmt"
	"strings"

	llm "github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/textx"
)

// Kind 是消息类别。
type Kind string

// 意图分类结果：action（扫描意图）/ chat（纯聊天）。
const (
	Action Kind = "action" // 触发/继续扫描
	QA     Kind = "qa"     // 就已有结果提问
)

// gen 是 Classify 依赖的最小 LLM 接口（llm.Generator 满足）。
type gen interface {
	Generate(ctx context.Context, msgs []llm.Message, tools []llm.ToolSchema) (llm.Result, error)
}

const systemPrompt = `你是渗透测试会话的消息分类器。判断用户最新消息属于哪类：
- action：让 agent 去执行——发起/继续扫描、验证某条请求、深挖某个点、重放或构造请求测试（含用户直接贴出的请求/命令要 agent 照打）。
- qa：就已挖到的结果提问、解释、总结，不需要 agent 再动手。
只依据最新一条用户消息判断；此前的对话仅用于消解指代（如「继续」「再来一次」指什么）。
只输出一个词：action 或 qa。无法确定时输出 qa。`

// classifyMaxRunes 是分类对象消息的钳制上限（rune）：分类只需要消息的意图
// 形态，不需要全文——渗透测试贴的大请求保留前段（方法/路径/意图都在头部）
// 足以判类，钳制防病态长文撑爆便宜档调用的输入。
const classifyMaxRunes = 8000

// Classify 调 simple 档模型分类。recent 是最近对话轮次（时间正序、可空）——
// 仅为消解「继续/再来一次」类指代；分类对象始终是最新一条。
//
// 两类失败语义不同，调用方的处置也不同：
//   - 生成失败（provider 不可达/限流/超时）→ 返回 error。意图闸失明不能静默
//     降级成 qa——那会把用户的扫描请求悄悄变成聊天回答（主功能静默失效），
//     交给调用方按各自入口的容错策略处置（HTTP 入口报错可重试，会话内入口
//     发可见的失败提示）。
//   - 输出无法解析为 action/qa → 归 qa 不报错：这是分类器契约内的保守缺省
//     （prompt 已指示「无法确定时输出 qa」），输出畸形是模型质量问题，不是闸失明。
func Classify(ctx context.Context, g gen, userMessage string, recent []llm.Message) (Kind, error) {
	msgs := make([]llm.Message, 0, len(recent)+2)
	msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: systemPrompt})
	msgs = append(msgs, recent...)
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: textx.TruncateRunes(userMessage, classifyMaxRunes, "…（后文截断）")})
	res, err := g.Generate(ctx, msgs, nil)
	if err != nil {
		return QA, fmt.Errorf("intent classify: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(res.Content)) {
	case "action":
		return Action, nil
	case "qa":
		return QA, nil
	default:
		return QA, nil
	}
}
