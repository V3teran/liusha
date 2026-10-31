package task

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/config/settingstore"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/convstream"
)

const (
	// reaperInterval 是 reaper 巡逻周期。两条 (status, heartbeat_at) 索引 UPDATE，常态命中 0 行，
	// 30s 既灵敏又不扰库。
	reaperInterval = 30 * time.Second
	// reaperStaleBuffer 是判死阈值在「最长合法工具+LLM 间隔」之上再加的安全缓冲（时钟偏移 / 调度抖动）。
	reaperStaleBuffer = 2 * time.Minute
)

// StartReaper 启动 task 心跳探活循环：task.heartbeat_at 由 agent 每次工具调用驱动续命
// （工具记录拦截器心跳）+ handler 入口重置一次。runner 进程崩溃或扫描卡死后心跳停摆，
// reaper 据此把超时孤儿判为 aborted——否则前端永远显示「进行中」。
// 会话任务同步落 aborted 时间线节点（判死对用户可见，而非静默翻转）。
//
// staleAfter 必须 > 单 run 内两次工具调用之间的最长合法间隔，否则冤杀正在干活的扫描：
// 最长间隔 ≈ 一次长工具执行(step_tool_timeout) + 决定下一步的 LLM 生成(step_llm_timeout) +
// 可能的上下文压缩 LLM(step_llm_timeout) + 缓冲。据此动态推导，不写死；
// stepLLMTimeoutSeconds 是启动 yaml 值，仅作热配置读失败时的回退基底。
func StartReaper(
	ctx context.Context,
	store *Store,
	settings *settingstore.Store,
	stepLLMTimeoutSeconds int,
	convs *conversation.Store,
	pub *convstream.Publisher,
	logger zerolog.Logger,
) {
	// 启动默认值作回退（1800/300 与 settingstore.normalized 的内置默认一致）：
	// runtime 配置读失败时 staleAfter 仍有合理上界。
	fallbackStaleAfter := time.Duration(1800+2*stepLLMTimeoutSeconds)*time.Second + reaperStaleBuffer
	go func() {
		logger.Info().Dur("stale_after", fallbackStaleAfter).Dur("interval", reaperInterval).Msg("task reaper started")
		ticker := time.NewTicker(reaperInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// staleAfter 每 tick 现读 runtime 热配置（工具超时 + 2×LLM 看门狗 + 缓冲）——
				// 配置改大后正在跑的长任务不被新口径冤杀。
				staleAfter := fallbackStaleAfter
				if rt, err := settings.Runtime(ctx); err == nil {
					staleAfter = time.Duration(rt.StepToolTimeoutSeconds+2*rt.StepLLMTimeoutSeconds)*time.Second + reaperStaleBuffer
				}
				ids, err := store.ReapStale(ctx, staleAfter)
				if err != nil {
					logger.Warn().Err(err).Msg("task reap stale failed")
					continue
				}
				for _, id := range ids {
					logger.Warn().Str("task_id", id).Dur("stale_after", staleAfter).
						Msg("task 心跳超时回收（进程崩溃或扫描卡死）")
					if convs == nil || pub == nil {
						continue
					}
					conversationID, cerr := convs.ResolveConversationByTask(ctx, id)
					if cerr != nil || conversationID == "" {
						continue // 无会话（纯后台扫描）或解析失败——静默，回收本体已完成
					}
					msg, aerr := convs.AppendMessage(ctx, conversationID,
						conversation.RoleAssistant, conversation.KindEvent,
						"扫描已停止", conversation.TaskStatusMeta(conversation.TaskStatusAborted,
							map[string]any{"reason": "心跳超时（runner 崩溃或任务卡死）"}))
					if aerr != nil {
						logger.Warn().Err(aerr).Str("task_id", id).Msg("回收通知写会话失败")
						continue
					}
					_ = pub.PublishMessage(ctx, conversationID, msg)
				}
			}
		}
	}()
}
