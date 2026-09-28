// Package main：种子强制重导工具。
//
// 把 agents/*.md（prompt/工具/档位）以覆盖语义写入 DB——术语/提示词升级后
// 同步已初始化的库。常规启动路径是 insert-only（不覆盖 DB 事实源），
// 本工具是唯一的显式覆盖通道。
//
// 用法：LIUSHA_POSTGRES_DSN=... go run ./cmd/reseed [-dir .]
// 同时顺带补齐缺失的 skills（insert-only，不覆盖已存在的 skill）。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/config/seed"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/envx"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/skillstore"
)

func main() {
	logger := logx.New("reseed")
	ctx := context.Background()

	dir := flag.String("dir", envx.OrDefault("LIUSHA_SEED_DIR", "."), "种子根目录（含 agents/ 与 skills/）")
	flag.Parse()

	pool, err := db.NewPgPool(ctx, os.Getenv("LIUSHA_POSTGRES_DSN"), 2, 1, 5, 0)
	if err != nil {
		logger.Fatal().Err(err).Msg("pg")
	}
	defer pool.Close()

	agents := agent.NewStore(pool)
	skills := skillstore.NewStore(pool)

	codes, err := seed.ImportAgentsForce(ctx, *dir, agents)
	if err != nil {
		logger.Fatal().Err(err).Msg("强制重导 agents 失败")
	}
	logger.Info().Strs("agents", codes).Msg("agents 已按种子覆盖写入")

	if err := seed.ImportSkills(ctx, *dir, skills); err != nil {
		logger.Fatal().Err(err).Msg("补齐 skills 失败")
	}
	fmt.Println("reseed 完成")
}
