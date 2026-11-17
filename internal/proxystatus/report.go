package proxystatus

import (
	"context"
	"time"

	"github.com/rs/zerolog"
)

// Snapshot 取代理此刻的状态；上报方补上心跳与退出时间。
type Snapshot func() Record

// stopWriteTimeout 是退出时写入停止时间的时限：ctx 已取消，单独给一个短时限。
const stopWriteTimeout = 3 * time.Second

// Report 立即上报一次，之后每个 HeartbeatInterval 上报一次；ctx 取消时写入停止时间后返回。
// 上报失败只记日志：状态是给控制台看的，不能拖垮代理本身。
func Report(ctx context.Context, store *Store, snapshot Snapshot, logger zerolog.Logger) {
	put := func(ctx context.Context, r Record) {
		if err := store.Put(ctx, r); err != nil {
			logger.Warn().Err(err).Msg("上报代理状态失败，控制台可能显示代理未运行")
		}
	}
	beat := func() Record {
		r := snapshot()
		r.HeartbeatAt = time.Now()
		return r
	}

	put(ctx, beat())
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			put(ctx, beat())
		case <-ctx.Done():
			r := beat()
			stopped := r.HeartbeatAt
			r.StoppedAt = &stopped
			wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopWriteTimeout)
			put(wctx, r)
			cancel()
			return
		}
	}
}
