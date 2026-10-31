package credential

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/logx"
)

// StartSweepLoop 周期清扫已终态任务的会话身份（启动即扫一次，此后每
// 10 分钟一轮）。判定依据是任务状态（active 保留、终态/已删除回收），
// 不依赖时间——凭证库无过期机制，生命周期由事实驱动。lookup 由装配方
// 提供（taskID → 当前状态），本包不反向依赖 task store。
func StartSweepLoop(ctx context.Context, creds *RedisProvider, lookup func(context.Context, string) (string, bool), logger zerolog.Logger) {
	sweep := func() {
		removed, err := SweepTerminalSessions(ctx, creds, lookup)
		if err != nil {
			logger.Warn().Err(err).Msg("会话身份清扫失败（下轮重试）")
			return
		}
		if removed > 0 {
			logger.Info().Int("removed", removed).Msg("清扫已终态任务的会话身份")
		}
	}
	logx.Go(logger, "session-sweep", func() {
		sweep() // 启动即扫：接住上次进程崩溃遗留的会话
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweep()
			}
		}
	})
}
