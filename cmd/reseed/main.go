// Package main：种子强制重导工具。
//
// 把 agents/*.md 与 skills/**/*.md 以覆盖语义写入 DB——术语/提示词升级后
// 同步已初始化的库。常规启动路径是 insert-only（不覆盖 DB 事实源），
// 本工具是唯一的显式覆盖通道：
//   - agents：整行强制覆盖（prompt/工具/档位/skills 声明）
//   - skills：仅覆盖内置行（is_builtin=true）；用户自建不动
//
// 写完经 redis 失效总线广播（LIUSHA_REDIS_ADDR 可达时），正在运行的
// api/runner 进程被动清 L1，下次读即新值——无需重启进程。
// redis 不可达时仅告警，靠各进程 L2 的 10min TTL 兜底收敛。
//
// 用法：LIUSHA_POSTGRES_DSN=... [LIUSHA_REDIS_ADDR=...] go run ./cmd/reseed [-dir .]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/agent"
	cfgcache "github.com/V3teran/liusha/internal/cache"
	"github.com/V3teran/liusha/internal/cachestore"
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

	skillRes, err := seed.ImportSkillsForce(ctx, *dir, skills)
	if err != nil {
		logger.Fatal().Err(err).Msg("强制重导 skills 失败")
	}
	logger.Info().
		Strs("upserted", skillRes.Upserted).
		Strs("pruned", skillRes.Pruned).
		Msg("内置 skills 已按种子覆盖写入（用户自建不动）")

	// 失效广播：写 DB 后让所有活进程（api/runner）的 L1/L2 立刻作废。
	invalidateCache(ctx, logger, pool, codes, skillRes)

	fmt.Println("reseed 完成")
}

// invalidateCache 经 redis 总线广播失效。redis 未配置/不可达仅告警——
// 运行中的进程最多 10min（L2 TTL）后收敛，或重启立即生效。
func invalidateCache(ctx context.Context, logger zerolog.Logger, pool *pgxpool.Pool, agentCodes []string, skillRes seed.SkillsSeed) {
	redisAddr := os.Getenv("LIUSHA_REDIS_ADDR")
	if redisAddr == "" {
		logger.Warn().Msg("LIUSHA_REDIS_ADDR 未配置——跳过缓存失效广播；运行中的 api/runner 最多 10min（L2 TTL）后收敛，或重启进程立即生效")
		return
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	defer func() { _ = rdb.Close() }()

	cfgStore := cfgcache.New(pool, cachestore.New(rdb, 0))
	if err := cfgStore.InvalidateAgents(ctx, agentCodes...); err != nil {
		logger.Warn().Err(err).Msg("agent 缓存失效广播失败（L2 TTL 兜底）")
	}
	if err := cfgStore.InvalidateSkills(ctx, skillRes.Touched()...); err != nil {
		logger.Warn().Err(err).Msg("skill 缓存失效广播失败（L2 TTL 兜底）")
	}
	logger.Info().Msg("缓存失效已广播（运行中的 api/runner 下次读即新值）")
}
