package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/V3teran/liusha/internal/proxy"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/rs/zerolog"
)

// TraceCollector 从 agent_traffic 收集 traffic 并构建 ExecutionTrace
type TraceCollector struct {
	agentStore *traffic.AgentStore
	logger     zerolog.Logger
}

// NewTraceCollector 创建 TraceCollector
func NewTraceCollector(agentStore *traffic.AgentStore, logger zerolog.Logger) *TraceCollector {
	return &TraceCollector{
		agentStore: agentStore,
		logger:     logger.With().Str("component", "trace_collector").Logger(),
	}
}

// CollectTrace 从 agent_traffic 收集指定 task 的所有 traffic，构建 ExecutionTrace
//
// 注意：由于 agent_traffic 表没有 action_id 字段，我们收集整个 task 的 traffic
// 而不是单个 action 的 traffic。对于 Evaluator 来说，完整的 task traffic 已经足够。
func (tc *TraceCollector) CollectTrace(ctx context.Context, taskID, actionID string) (ExecutionTrace, error) {
	tc.logger.Debug().
		Str("task_id", taskID).
		Str("action_id", actionID).
		Msg("开始收集 traffic trace（收集整个 task 的 traffic）")

	// 从 agent_traffic 读取这个 task 产生的所有 traffic
	// 注意：不按 action_id 过滤，因为表中没有该字段
	filter := traffic.AgentListFilter{
		Limit: 500, // 足够大，一个 task 不太可能产生 500+ 请求
	}

	summaries, err := tc.agentStore.ListByTaskFiltered(ctx, taskID, filter)
	if err != nil {
		return ExecutionTrace{}, err
	}

	tc.logger.Debug().
		Int("traffic_count", len(summaries)).
		Msg("收集到 traffic 记录")

	// 如果没有 traffic，返回空 trace
	if len(summaries) == 0 {
		tc.logger.Warn().Msg("没有找到 traffic 记录")
		return ExecutionTrace{
			Traffic: []TrafficRecord{},
			Session: SessionState{Cookies: make(map[string]string)},
			Meta:    TraceMeta{TotalCount: 0},
		}, nil
	}

	// 构建 trace
	builder := NewTraceBuilder()
	builder.trace.Meta.ActionID = actionID
	builder.trace.Meta.TaskID = taskID
	builder.trace.Meta.StartTime = summaries[len(summaries)-1].CreatedAt // 最后一个是最早的
	builder.trace.Meta.EndTime = summaries[0].CreatedAt                  // 第一个是最新的

	// 反转顺序（从最早到最新）
	for i := len(summaries) - 1; i >= 0; i-- {
		summary := summaries[i]

		// 读取完整的 traffic 记录（包含 body）
		fullTraffic, err := tc.agentStore.GetByID(ctx, summary.ID)
		if err != nil {
			tc.logger.Warn().
				Err(err).
				Int64("traffic_id", summary.ID).
				Msg("读取完整 traffic 失败，跳过")
			continue
		}

		// 转换为 proxy.TrafficSnapshot
		snapshot, err := tc.convertToSnapshot(&fullTraffic)
		if err != nil {
			tc.logger.Warn().
				Err(err).
				Int64("traffic_id", summary.ID).
				Msg("转换 snapshot 失败，跳过")
			continue
		}

		// 添加到 trace
		builder.AddTraffic(snapshot, fullTraffic.Tool, "")
	}

	trace := builder.Build()

	tc.logger.Info().
		Str("action_id", actionID).
		Int("traffic_count", trace.Meta.TotalCount).
		Dur("duration", trace.Meta.Duration).
		Msg("trace 构建完成（包含整个 task 的 traffic）")

	return trace, nil
}

// convertToSnapshot 将 traffic.AgentTraffic 转换为 proxy.TrafficSnapshot
func (tc *TraceCollector) convertToSnapshot(t *traffic.AgentTraffic) (proxy.TrafficSnapshot, error) {
	// 解析 request headers（从 json.RawMessage）
	var reqHeaders map[string]string
	if len(t.RequestHeaders) > 0 {
		if err := json.Unmarshal(t.RequestHeaders, &reqHeaders); err != nil {
			return proxy.TrafficSnapshot{}, fmt.Errorf("解析 request headers 失败: %w", err)
		}
	}

	// 解析 response headers（可能是 map[string]string 或 map[string][]string）
	var respHeaders map[string][]string
	if len(t.ResponseHeaders) > 0 {
		// 先尝试解析为 map[string][]string
		if err := json.Unmarshal(t.ResponseHeaders, &respHeaders); err != nil {
			// 如果失败，尝试解析为 map[string]string 然后转换
			var singleValueHeaders map[string]string
			if err2 := json.Unmarshal(t.ResponseHeaders, &singleValueHeaders); err2 != nil {
				return proxy.TrafficSnapshot{}, fmt.Errorf("解析 response headers 失败: %w", err)
			}
			// 转换为 map[string][]string
			respHeaders = make(map[string][]string, len(singleValueHeaders))
			for k, v := range singleValueHeaders {
				respHeaders[k] = []string{v}
			}
		}
	}

	return proxy.TrafficSnapshot{
		ID:              strconv.FormatInt(t.ID, 10),
		Source:          "internal",
		AgentRunID:      t.AgentRunID,
		Host:            t.Host,
		Method:          t.Method,
		URI:             t.URL,
		Path:            t.Path,
		Scheme:          extractScheme(t.URL),
		RequestHeaders:  reqHeaders,
		RequestBody:     t.RequestBody,
		StatusCode:      t.StatusCode,
		ResponseHeaders: respHeaders,
		ResponseBody:    t.ResponseBody,
		Timestamp:       t.CreatedAt,
	}, nil
}

// extractScheme 从 URL 中提取 scheme
func extractScheme(url string) string {
	if len(url) > 7 && url[:7] == "http://" {
		return "http"
	}
	if len(url) > 8 && url[:8] == "https://" {
		return "https"
	}
	return "http" // 默认
}
