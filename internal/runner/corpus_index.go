package runner

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/corpus"
)

// CorpusIndexInterval 是知识库向量补齐的巡检周期。
const CorpusIndexInterval = 10 * time.Minute

// StartCorpusIndexLoop 为尚无向量的知识条目补齐向量：启动跑一次，此后周期巡检（随 ctx 取消退出）。
// 写入时向量化失败的条目、在配置密钥之前写入的条目，都由这里收账；不补齐它们就永远进不了
// 语义召回。检索模型密钥是运行期可变的（模型配置页热改），所以循环恒开——未配置时
// IndexPending 自动空转，配置后下一轮（或 api 保存时的即时补齐）收账。
func StartCorpusIndexLoop(ctx context.Context, kb *corpus.Service, logger zerolog.Logger) {
	run := func() {
		n, err := kb.IndexPending(ctx)
		if err != nil && ctx.Err() == nil {
			logger.Warn().Err(err).Int("indexed", n).Msg("知识库向量补齐未完成（下轮巡检重试）")
			return
		}
		if n > 0 {
			logger.Info().Int("indexed", n).Msg("知识库向量已补齐")
		}
	}
	go func() {
		run()
		ticker := time.NewTicker(CorpusIndexInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}
