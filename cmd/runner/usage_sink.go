package main

import (
	"context"

	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/llminvocation"
)

// usageSink 把框架层 llm.UsageSink 桥到 llminvocation.Store（llm_invocation 表）。
// 审计为 best-effort：Append 内部满载丢行并告警，不阻塞 Generate 路径。
type usageSink struct{ store *llminvocation.Store }

func (s usageSink) RecordUsage(ctx context.Context, r llm.UsageRecord) {
	var taskID *string
	if r.TaskID != "" {
		id := r.TaskID
		taskID = &id
	}
	// ExecutorID 是 agent_run.id 外键（SET NULL）——认知路径无 per-agent run 行，留空；
	// 角色维度落 Role 列。
	_, _ = s.store.Append(ctx, llminvocation.Invocation{
		TaskID:       taskID,
		Provider:     r.Provider,
		Model:        r.Model,
		InTokens:     r.InTokens,
		OutTokens:    r.OutTokens,
		CachedTokens: r.CachedTokens,
		LatencyMs:    r.LatencyMs,
		TTFTMs:       r.TTFTMs,
		IsStream:     r.IsStream,
		FinishReason: r.FinishReason,
		Error:        r.Err,
		Role:         r.Role,
		Messages:     r.Messages,
		Result:       r.Result,
	})
}
