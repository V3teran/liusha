// Package corpusimport 是知识库专家知识离线导入（liusha corpus-import）。
//
// 把专家经验 / 历史报告 markdown 批量导入知识库（source=expert）。agent 运行期对知识库只有
// search_corpus 检索能力，知识沉淀由本命令离线完成（来源与信任级受控）。
// 流程：读 markdown → 按 ## 段切条 → LLM 自动打标 title/tags → embed → 落库。
//
// 打标模型取「LLM 配置」页的 simple 档；JINA_API_KEY 缺失时只落行不 embed（仍可 sparse 检索）。
package corpusimport

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/cryptx"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/embedding"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/llmstore"
	"github.com/V3teran/liusha/internal/logx"
)

// Run 导入 files 中的 markdown。单个文件失败记日志并跳过；全部失败时返回错误。
func Run(ctx context.Context, cfg config.AppConfig, files []string) error {
	if len(files) == 0 {
		return errors.New("缺少要导入的文件：liusha corpus-import <file.md> [more.md ...]")
	}
	logger := logx.New("corpus-import")

	pool, err := db.NewPgPool(ctx, cfg.Postgres, logger)
	if err != nil {
		return fmt.Errorf("连接 PostgreSQL: %w", err)
	}
	defer pool.Close()

	// LLM 配置的多级缓存需要 Redis 承载 L2；打标离不开 LLM 路由，连不上即失败。
	rdb, err := db.NewRedis(ctx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("连接 Redis: %w", err)
	}
	defer func() { _ = rdb.Close() }()

	keyCipher, err := cryptx.NewFromEnv("LIUSHA_LLM_KEY_SECRET")
	if err != nil {
		return fmt.Errorf("LIUSHA_LLM_KEY_SECRET 未配置或不合法（provider 密钥解密需要它）: %w", err)
	}
	llmStore := llmstore.New(pool, cachestore.New(rdb, 0))
	tagger := llm.NewRouterWithOpts(llmStore.AsRouterStore(keyCipher), cfg.LLM.Retry.Options())

	var embedder *embedding.Client
	if ec, err := embedding.NewClient(os.Getenv("JINA_API_KEY")); err != nil {
		logger.Warn().Err(err).Msg("JINA_API_KEY 未配置：只落行不 embed（仍可 sparse 检索）")
	} else {
		embedder = ec
	}

	store := corpus.NewStore(pool)
	total, failed := 0, 0
	for _, f := range files {
		n, err := importFile(ctx, logger, store, tagger, embedder, f)
		if err != nil {
			logger.Error().Err(err).Str("file", f).Msg("导入失败（跳过该文件）")
			failed++
			continue
		}
		total += n
	}
	logger.Info().Int("imported", total).Int("files", len(files)).Int("failed", failed).Msg("知识库专家知识导入完成")
	if failed == len(files) {
		return fmt.Errorf("全部 %d 个文件导入失败", failed)
	}
	return nil
}
