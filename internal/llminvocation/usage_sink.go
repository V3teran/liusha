package llminvocation

import (
	"context"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// UsageSink 把框架层 llm.UsageSink 桥到本包 Store（llm_invocation 表）。
// runner 与 api 共用：agent 调用经 CallMeta 携带 task/run 归属；api 侧对话内调用
// （问答、意图分类、标题生成）携带所在对话与 Role 角色标签。
// 审计为 best-effort：Append 内部满载丢行并告警，不阻塞 Generate 路径。
type UsageSink struct{ Store invocationAppender }

// invocationAppender 是审计落库需要的最小写入能力。
type invocationAppender interface {
	Append(Invocation)
}

// RecordUsage 满足 framework llm.UsageSink：把一次 LLM 调用计量落 llm_invocation 表。
func (s UsageSink) RecordUsage(ctx context.Context, r llm.UsageRecord) {
	taskID, agentRunID, conversationID := optionalID(r.TaskID), optionalID(r.AgentRunID), optionalID(r.ConversationID)
	// 错误类别取记录携带的分类：框架层 Classify 对真实错误的结论是全系统瞬时错误的
	// 唯一分类事实源，审计面直接采用。拿错误文本在这里重新分类是行不通的——哨兵错误
	// 只存在于错误链上，本地的在途名额节流会因此被判成「其它错误」。
	errorKind := ""
	if r.Err != "" {
		errorKind = ErrorKindOf(r.ErrClass)
	}
	// AgentRunID 是 agent_run.id 外键（SET NULL）——由 CallMeta 经 ctx 注入本轮 run 行；
	// 角色维度落 Role 列。
	s.Store.Append(Invocation{
		ErrorKind:      errorKind,
		TaskID:         taskID,
		AgentRunID:     agentRunID,
		ConversationID: conversationID,
		Provider:       r.Provider,
		Model:          r.Model,
		InTokens:       r.InTokens,
		OutTokens:      r.OutTokens,
		CachedTokens:   r.CachedTokens,
		LatencyMs:      r.LatencyMs,
		FinishReason:   r.FinishReason,
		Error:          r.Err,
		Role:           r.Role,
		Messages:       r.Messages,
		Result:         r.Result,
	})
}

// optionalID 把空串折成 nil（可空外键列落 NULL）。
func optionalID(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

// ErrorKinds 是失败调用的错误类别（成功为空串）：审计页按它区分限流、服务端、网络与其它错误。
// 与库里的约束 llm_invocation_error_kind_check 一致（集成测试守住）。
var ErrorKinds = []string{"rate_limited", "server", "network", "error"}

// ErrorKindOf 把框架的错误分类映射为错误类别。
func ErrorKindOf(c llm.ErrorClass) string {
	switch c {
	case llm.Class429, llm.ClassBusy: // 排不到在途名额是本地为账户限流做的节流，与限流同列
		return "rate_limited"
	case llm.Class529, llm.Class5xx:
		return "server"
	case llm.ClassNet:
		return "network"
	default:
		return "error"
	}
}
