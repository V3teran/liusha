package main

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
)

// stallWindowTicks 是停滞判定所需的连续无变化 tick 数（15s/tick）。
// 单个 action 的长 ReAct 执行期间图不变但 LLM/工具在推进，图无变化
// 本身不构成停滞证据；叠加活动信号（下述 activityIdle）后延长窗口
// 只影响真正的死亡编排的检出延迟。
const stallWindowTicks = 40 // 40 tick × 15s = 10 分钟

// activityIdle 查询该 task 最近 5 分钟是否有 LLM/工具活动。
// 活性口径与 runner 的恢复监控一致：图停滞 + 活动停摆才算真死。
// 查询失败按"有活动"处理（宁可多等，不误杀）。
func activityIdle(ctx context.Context, taskID string) bool {
	// 复用 api 的探索图统计通道不可行（那是图数据）；
	// 直接查 api 暴露的会话用量接口过于间接——e2e 触发器与 DB 同机（dev 契约），
	// 用 LIUSHA_E2E_PG_DSN 直查是最低耦合做法；DSN 未配时保守返回 false（不判死）。
	dsn := envOrDefault("LIUSHA_E2E_PG_DSN", "postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable")
	pool, err := newPgxPoolForE2E(ctx, dsn)
	if err != nil {
		return false
	}
	defer pool.Close()

	var n int
	err = pool.QueryRow(ctx, `
		SELECT (
			SELECT count(*) FROM llm_invocation
			WHERE task_id = $1 AND created_at > now() - interval '5 minutes'
		) + (
			SELECT count(*) FROM tool_invocation
			WHERE task_id = $1 AND created_at > now() - interval '5 minutes'
		)`, taskID).Scan(&n)
	if err != nil {
		return false
	}
	return n == 0
}

// envOrDefault 读环境变量（缺省回退）。
func envOrDefault(key, fallback string) string {
	if v := lookupEnv(key); v != "" {
		return v
	}
	return fallback
}

// pollTaskWithGraphStats 轮询任务直到满足验收标准或任务停滞。
func pollTaskWithGraphStats(
	ctx context.Context,
	client *ExplorationGraphClient,
	taskID string,
	criteria AcceptanceCriteria,
	logger *zerolog.Logger,
) error {
	deadline := pollDeadline()
	var absoluteDeadline time.Time
	if deadline > 0 {
		absoluteDeadline = time.Now().Add(deadline)
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	logger.Info().
		Str("task_id", taskID).
		Int("min_objectives", criteria.MinObjectives).
		Int("min_actions", criteria.MinActions).
		Int("min_results", criteria.MinResults).
		Msg("开始轮询任务（探索图模式）")

	var lastStats GraphStats
	noProgressCount := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if deadline > 0 && time.Now().After(absoluteDeadline) {
				return fmt.Errorf("轮询超时: 未在 %v 内完成 (最后统计: %+v)",
					deadline, lastStats)
			}

			// 查询探索图统计
			stats, err := client.GetTaskStats(ctx, taskID)
			if err != nil {
				logger.Warn().Err(err).Msg("获取任务统计失败，继续轮询")
				continue
			}

			// 停滞判定 = 图无变化持续 stallWindowTicks 且 LLM/工具活动停摆 ≥5 分钟
			// （与 runner 恢复监控同一活性口径；宁可多等不误杀健康任务）。
			if stats == lastStats {
				noProgressCount++
				if noProgressCount == 8 {
					logger.Warn().
						Interface("stats", stats).
						Msg("任务无图变化超过 2 分钟")
				}
				if noProgressCount >= stallWindowTicks && activityIdle(ctx, taskID) {
					return fmt.Errorf("任务卡住: 图统计 %v 已无变化超过 %v 且无 LLM/工具活动",
						stats, stallWindowTicks*15*time.Second)
				}
			} else {
				noProgressCount = 0
				logger.Info().
					Interface("stats", stats).
					Str("diagnostic", criteria.DiagnosticMessage(stats)).
					Msg("任务进展")
			}

			lastStats = stats

			// 检查是否满足验收标准
			if criteria.IsMetBy(stats) {
				logger.Info().
					Interface("stats", stats).
					Msg("✓ 任务完成，满足验收标准")
				return nil
			}
		}
	}
}
