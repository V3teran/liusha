package llminvocation

import (
	"context"
	"time"

	"github.com/rs/zerolog"
)

// StartRetentionSweep 启动 llm_invocation 留存清理循环（每日一次）。
// 记录含完整 prompt/response 敏感载荷，retentionDays（llm.invocation_retention_days）> 0 时
// 每日删除到期行（0 = 永久留存，审计全量）。runner 单实例部署，天然承担单例清理职责。
// 删除前先 Flush，避免 buffer 内的新记录与删除窗口竞争。
func StartRetentionSweep(ctx context.Context, store *Store, retentionDays int, logger zerolog.Logger) {
	if retentionDays <= 0 {
		logger.Info().Msg("LLM 调用记录永久留存（llm.invocation_retention_days = 0）")
		return
	}

	go func() {
		interval := 24 * time.Hour
		logger.Info().
			Int("retention_days", retentionDays).
			Dur("interval", interval).
			Msg("llm_invocation 留存清理已启动")

		sweep := func() {
			if err := store.Flush(ctx); err != nil {
				logger.Warn().Err(err).Msg("留存清理前 flush 失败（仍继续删除到期行）")
			}
			cutoff := time.Now().AddDate(0, 0, -retentionDays)
			deleted, err := store.DeleteOlderThan(ctx, cutoff)
			if err != nil {
				logger.Warn().Err(err).Msg("llm_invocation 留存清理失败")
				return
			}
			if deleted > 0 {
				logger.Info().Int64("deleted", deleted).Msg("llm_invocation 到期记录已清理")
			}
		}

		sweep()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweep()
			}
		}
	}()
}
