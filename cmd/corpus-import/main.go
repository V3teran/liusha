// Package main 是 corpus 专家知识离线导入 CLI（见 lesson→corpus 设计 §5.3）。
//
// 把专家经验 / 历史报告 markdown 批量导入 corpus（source=expert），与 agent 的 write_corpus
// 工具分开（不同来源、不同信任级）。流程：读 markdown → 按 ## 段切条 → LLM 自动打标 title/tags
// → Jina embed → 落库。
//
// 用法：
//
//	go run ./cmd/corpus-import path/to/knowledge.md [more.md ...]
//
// 环境变量（复用 runner 同源）：
//   - LIUSHA_POSTGRES_DSN：PG 连接串
//   - LIUSHA_CONFIG：      config.yaml 路径（默认 ./config/config.yaml，取 light provider + LLM keys）
//   - JINA_API_KEY：       embedding；缺失则只落行不 embed（仍可 sparse 检索）
package main

import (
	"context"
	"os"

	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/cryptx"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/embedding"
	"github.com/V3teran/liusha/internal/envx"
	"github.com/V3teran/liusha/internal/llmstore"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/provider"
)

func main() {
	logger := logx.New("corpus-import")
	ctx := context.Background()

	files := os.Args[1:]
	if len(files) == 0 {
		logger.Fatal().Msg("用法：corpus-import <file.md> [more.md ...]")
	}

	cfgPath := envx.OrDefault("LIUSHA_CONFIG", "./config/config.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		logger.Fatal().Err(err).Str("path", cfgPath).Msg("加载 config 失败")
	}
	pgDSN := envx.OrDefault("LIUSHA_POSTGRES_DSN", "postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable")
	pool, err := db.NewPgPool(ctx, pgDSN, 5, 1, 0, 0)
	if err != nil {
		logger.Fatal().Err(err).Msg("连接 PG 失败")
	}
	defer pool.Close()

	// LLM 配置事实源：tagger 打标解析 light 别名对应 provider 部署。CLI 一次性运行，
	// 无跨进程失效需求，但 cachestore 需 redis 承载 L2——连不上则致命（打标离不开 LLM 路由）。
	redisAddr := os.Getenv("LIUSHA_REDIS_ADDR")
	rdb, err := db.NewRedis(ctx, redisAddr, cfg.Redis)
	if err != nil {
		logger.Fatal().Err(err).Msg("连接 Redis 失败")
	}
	defer func() { _ = rdb.Close() }()
	llmStore := llmstore.New(pool, cachestore.New(rdb, 0))

	// LLM provider API Key 加密密钥（migration 0103）：同 cmd/api/cmd/runner 的 fail-fast 校验——
	// tagger 打标要真正解密出明文才能打 LLM 请求。
	llmKeyCipher, err := cryptx.NewFromEnv("LIUSHA_LLM_KEY_SECRET")
	if err != nil {
		logger.Fatal().Err(err).Msg("LIUSHA_LLM_KEY_SECRET 未配置或不合法——provider 密钥解密需要它（fail-fast）")
	}

	store := corpus.NewStore(pool)
	tagger := provider.NewRouter(llmStore.AsRouterStore(), llmKeyCipher)
	var embedder *embedding.Client
	if ec, err := embedding.NewClient(os.Getenv("JINA_API_KEY")); err != nil {
		logger.Warn().Err(err).Msg("JINA_API_KEY 未配置：只落行不 embed（仍可 sparse 检索）")
	} else {
		embedder = ec
	}

	total := 0
	for _, f := range files {
		n, err := importFile(ctx, logger, store, tagger, embedder, f)
		if err != nil {
			logger.Error().Err(err).Str("file", f).Msg("导入失败（跳过该文件）")
			continue
		}
		total += n
	}
	logger.Info().Int("imported", total).Int("files", len(files)).Msg("corpus 专家知识导入完成")
}
