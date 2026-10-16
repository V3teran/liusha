package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// stallWindowTicks 是停滞判定所需的连续无变化 tick 数（15s/tick）。
// 单个 action 的长 ReAct 执行期间图不变但 LLM/工具在推进，图无变化
// 本身不构成停滞证据；叠加活动信号（下述 activityIdle）后延长窗口
// 只影响真正的死亡编排的检出延迟。
const stallWindowTicks = 40 // 40 tick × 15s = 10 分钟

// terminateWaitInterval/terminateGracePeriod 是验收通过后优雅终止的等待节奏
// 与上限：控制平面消费器的轮询周期是 5s，四 agent 退出 + 终态落库在其之上
// 再加余量。超时未落终态判 FAIL——验收包含停机路径，挂死的任务不许溜过。
const (
	terminateWaitInterval = 5 * time.Second
	terminateGracePeriod  = 3 * time.Minute
)

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

// terminateAndAwait 经控制平面下发 terminate 并等待任务落终态，返回最终状态。
// 下发走 task_control_event 表（runner 侧消费器 5s 轮询），等待走 task.status——
// 事件消费 → detector 中止 → 四 agent 退出 → 终态落库的整条停机链路都在验收内。
func terminateAndAwait(ctx context.Context, pool *pgxpool.Pool, taskID string, logger *zerolog.Logger) (string, error) {
	if _, err := pool.Exec(ctx,
		`INSERT INTO task_control_event (task_id, command, payload) VALUES ($1, 'terminate', '{}'::jsonb)`,
		taskID); err != nil {
		return "", fmt.Errorf("下发 terminate 失败: %w", err)
	}
	logger.Info().Str("task_id", taskID).Msg("已下发 terminate，等待优雅停机")

	deadline := time.Now().Add(terminateGracePeriod)
	for time.Now().Before(deadline) {
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM task WHERE id=$1`, taskID).Scan(&status); err != nil {
			return "", fmt.Errorf("查询任务状态失败: %w", err)
		}
		if status != "active" {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(terminateWaitInterval):
		}
	}
	return "active", fmt.Errorf("任务在 %v 内未落终态（停机链路卡死）", terminateGracePeriod)
}

// pollTaskWithGraphStats 轮询任务直到满足验收标准，随后经控制平面优雅终止
// 任务并等待终态（验收包含停机路径——"挖到漏洞"和"收得住"都是验收项）。
func pollTaskWithGraphStats(
	ctx context.Context,
	client *ExplorationGraphClient,
	pool *pgxpool.Pool,
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

				// 验收通过 ≠ 万事大吉：认知循环仍在烧 LLM。经控制平面下发
				// terminate（而不是直接改 task.status）——同时验证优雅停机
				// 路径：事件消费 → 四 agent 中止 → 终态落库。
				finalStatus, err := terminateAndAwait(ctx, pool, taskID, logger)
				if err != nil {
					return fmt.Errorf("优雅终止失败: %w", err)
				}
				if finalStatus != "completed" {
					return fmt.Errorf("任务终态为 %q，期望 completed", finalStatus)
				}
				logger.Info().Str("final_status", finalStatus).Msg("✓ 任务已优雅终止")
				return nil
			}
		}
	}
}
