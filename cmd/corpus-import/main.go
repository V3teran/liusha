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
// 环境变量（复用 scanner 同源）：
//   - LIUSHA_POSTGRES_DSN：PG 连接串
//   - LIUSHA_CONFIG：      config.yaml 路径（默认 ./config/config.yaml，取 light provider + LLM keys）
//   - JINA_API_KEY：       embedding；缺失则只落行不 embed（仍可 sparse 检索）
package main

import (
	"context"
	"os"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/einollm"
	"github.com/V3teran/liusha/internal/embedding"
	"github.com/V3teran/liusha/internal/envx"
	"github.com/V3teran/liusha/internal/logx"
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

	store := corpus.NewStore(pool)
	tagger := einollm.New(cfg) // light provider 打标
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
