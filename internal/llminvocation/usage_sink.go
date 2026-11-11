package llminvocation

import (
	"context"
	"errors"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// UsageSink 把框架层 llm.UsageSink 桥到本包 Store（llm_invocation 表）。
// runner 与 api 共用：agent 调用经 CallMeta 携带 task/run 归属；api 侧
// （chat/qa/意图分类）无 run 归属，落 NULL 外键 + Role 角色标签。
// 审计为 best-effort：Append 内部满载丢行并告警，不阻塞 Generate 路径。
type UsageSink struct{ Store *Store }

// RecordUsage 满足 framework llm.UsageSink：把一次 LLM 调用计量落 llm_invocation 表。
func (s UsageSink) RecordUsage(ctx context.Context, r llm.UsageRecord) {
	var taskID, agentRunID *string
	if r.TaskID != "" {
		id := r.TaskID
		taskID = &id
	}
	if r.AgentRunID != "" {
		id := r.AgentRunID
		agentRunID = &id
	}
	// 错误分类在写入时判定（单一事实源）：框架层 Classify 是全系统瞬时错误
	// 的唯一分类器，审计面直接消费结论而不再各自解析错误文本。
	errorKind := ""
	if r.Err != "" {
		switch llm.Classify(errors.New(r.Err)) {
		case llm.Class429:
			errorKind = "rate_limited"
		case llm.Class529, llm.Class5xx:
			errorKind = "server"
		case llm.ClassNet:
			errorKind = "network"
		default:
			errorKind = "error"
		}
	}
	// AgentRunID 是 agent_run.id 外键（SET NULL）——由 CallMeta 经 ctx 注入本轮 run 行；
	// 角色维度落 Role 列。
	_ = s.Store.Append(ctx, Invocation{
		ErrorKind:    errorKind,
		TaskID:       taskID,
		AgentRunID:   agentRunID,
		Provider:     r.Provider,
		Model:        r.Model,
		InTokens:     r.InTokens,
		OutTokens:    r.OutTokens,
		CachedTokens: r.CachedTokens,
		LatencyMs:    r.LatencyMs,
		FinishReason: r.FinishReason,
		Error:        r.Err,
		Role:         r.Role,
		Messages:     r.Messages,
		Result:       r.Result,
	})
}
