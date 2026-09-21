package main

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
)

// pollTaskWithGraphStats 使用新的知识图谱 API 轮询任务完成
// 替代旧的 pollTaskCompletion（基于 finding 表）
func pollTaskWithGraphStats(
	ctx context.Context,
	client *KnowledgeGraphClient,
	taskID string,
	criteria AcceptanceCriteria,
	logger *zerolog.Logger,
) error {
	deadline := time.Now().Add(pollDeadline())
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	logger.Info().
		Str("task_id", taskID).
		Int("min_objectives", criteria.MinObjectives).
		Int("min_actions", criteria.MinActions).
		Int("min_results", criteria.MinResults).
		Msg("开始轮询任务（知识图谱模式）")

	var lastStats GraphStats
	noProgressCount := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("轮询超时: %s (最终统计: %+v)",
					criteria.DiagnosticMessage(lastStats), lastStats)
			}

			// 调用新 API 获取统计
			stats, err := client.GetTaskStats(ctx, taskID)
			if err != nil {
				logger.Warn().Err(err).Msg("获取任务统计失败，继续轮询")
				continue
			}

			// 检查是否卡住
			if IsStuck(stats) {
				return fmt.Errorf("任务卡住: %s (统计: %+v)",
					StuckReason(stats), stats)
			}

			// 检查是否有进展
			if stats == lastStats {
				noProgressCount++
				if noProgressCount >= 4 { // 1 分钟无进展
					logger.Warn().
						Interface("stats", stats).
						Msg("任务无进展超过 1 分钟")
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
