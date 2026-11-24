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

// terminateWaitInterval/terminateGracePeriod 是验收通过后优雅终止的等待节奏
// 与上限：认知循环每 5s 读一次任务行，四 agent 退出 + 终态落库在其之上
// 再加余量。超时未落终态判 FAIL——验收包含停机路径，挂死的任务不许溜过。
const (
	terminateWaitInterval = 5 * time.Second
	terminateGracePeriod  = 3 * time.Minute
)

// heartbeatIdleWindow 是判定「存活 run 的心跳已停」的时长。心跳是 runner 恢复监控判定一轮 run
// 是否还在被推进的依据，这里与它看同一个信号：图停滞 + 心跳停摆才算真死。窗口取得比恢复监控
// 的判定阈值（缺省 120 秒）长，让监控先处置——它重投之后新的 run 续上心跳，这里自然判为活跃。
// 不按「累计调用次数是否增长」判断：一个有超时的长命令在途时调用次数不增长，会被误判停滞。
// 心跳时刻取自库端时钟，本工具与开发栈跑在同一主机上。
const heartbeatIdleWindow = 5 * time.Minute

// runIdle 报告任务当前这轮 run 的存活心跳是否已超过 heartbeatIdleWindow 没有续约。没有存活的
// run 时不在这里判（交给任务状态与恢复监控）；读取失败按「有活动」处理（宁可多等，不误判停滞）。
func runIdle(ctx context.Context, api taskAPI, taskID string, logger *zerolog.Logger) bool {
	run, live, err := api.LiveRun(ctx, taskID)
	if err != nil {
		logger.Warn().Err(err).Msg("读取任务当前 run 失败，按有活动处理")
		return false
	}
	return live && time.Since(run.HeartbeatAt) >= heartbeatIdleWindow
}

// terminateAndAwait 走公开 API 下发 terminate 并等待任务落终态，返回最终状态。
// 下发与前端同一控制端点（验收路径 = 用户路径），等待走 task.status——
// 控制下发 → 停机传播 → 四 agent 退出 → 终态落库的整条链路都在验收内。
func terminateAndAwait(ctx context.Context, api taskAPI, taskID string, logger *zerolog.Logger) (string, error) {
	if err := controlTask(api.base, api.session, taskID, "terminate"); err != nil {
		return "", fmt.Errorf("下发 terminate 失败: %w", err)
	}
	logger.Info().Str("task_id", taskID).Msg("已下发 terminate，等待优雅停机")

	deadline := time.Now().Add(terminateGracePeriod)
	for time.Now().Before(deadline) {
		status, err := api.Status(ctx, taskID)
		if err != nil {
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

// pollTaskWithGraphStats 轮询任务直到满足验收标准，随后经控制 API 优雅终止
// 任务并等待终态（验收包含停机路径——"挖到漏洞"和"收得住"都是验收项）。
func pollTaskWithGraphStats(
	ctx context.Context,
	api taskAPI,
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
			stats, err := api.GraphStats(ctx, taskID)
			if err != nil {
				logger.Warn().Err(err).Msg("获取任务统计失败，继续轮询")
				continue
			}

			// 停滞判定 = 图无变化持续 stallWindowTicks 且当前 run 的存活心跳停摆 ≥5 分钟
			// （与 runner 恢复监控同一活性口径；宁可多等不误杀健康任务）。
			if stats == lastStats {
				noProgressCount++
				if noProgressCount == 8 {
					logger.Warn().
						Interface("stats", stats).
						Msg("任务无图变化超过 2 分钟")
				}
				if noProgressCount >= stallWindowTicks && runIdle(ctx, api, taskID, logger) {
					return fmt.Errorf("任务卡住: 图统计 %v 已无变化超过 %v，且当前 run 的存活心跳已停",
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

				// 验收通过 ≠ 万事大吉：认知循环仍在烧 LLM。经控制 API 下发
				// terminate（而不是直接改 task.status）——同时验证优雅停机
				// 路径：任务行终态 → 循环观察到 → 四 agent 中止 → run 收尾落库。
				finalStatus, err := terminateAndAwait(ctx, api, taskID, logger)
				if err != nil {
					return fmt.Errorf("优雅终止失败: %w", err)
				}
				// 验收达标即主动停（不再烧 token 等自然收敛），主动停的终态
				// 就是 aborted——"收得住"验收的是能停，不是停成什么样。
				if finalStatus != "aborted" {
					return fmt.Errorf("任务终态为 %q，期望 aborted", finalStatus)
				}
				logger.Info().Str("final_status", finalStatus).Msg("✓ 任务已优雅终止")
				return nil
			}
		}
	}
}
