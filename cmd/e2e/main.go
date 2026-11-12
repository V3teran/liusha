// Package main 是 liusha 端到端验收触发器：以人工下发（POST /chat）跑验收剧本。
//
// CLI 用法：
//
//	go run ./cmd/e2e            # 全部剧本（字典序）
//	go run ./cmd/e2e full       # 综合开放性扫描（压测 LLM 自主 recon + 多攻击面扩展）
//	go run ./cmd/e2e xss        # XSS 专项扫描
//
// 流程（按选中顺序串行跑）：
//  1. POST /chat body={"brief":"<自然语言任务简报>"} → 拿 (conversation_id, task_id)
//  2. 按 task_id 轮询探索图 → 满足 AcceptanceCriteria（objective/action/result 节点数）为 PASS
//
// 想加新漏洞类型：profiles map 加一行（带 brief 自然语言描述）。
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/logx"
)

// pollDeadline 从 ENV LIUSHA_E2E_POLL_DEADLINE_SECONDS 读取，缺省无超时（0 表示不限制）。
func pollDeadline() time.Duration {
	if v := os.Getenv("LIUSHA_E2E_POLL_DEADLINE_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 0 // 默认无超时限制
}

// envOr 读环境变量 key；未设或为空时返回 fallback。
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	logger := logx.New("e2e")
	ctx := context.Background()

	apiBase := envOr("LIUSHA_API_BASE", "http://localhost:8580")
	pgDSN := envOr("LIUSHA_POSTGRES_DSN", "postgres://liusha:liusha@localhost:55432/liusha?sslmode=disable")

	selected, err := selectProfiles(os.Args[1:])
	if err != nil {
		logger.Fatal().Err(err).Msg("select profiles")
	}

	// 登录引导：拿会话 Cookie（全新库自动走首启建号），后续所有请求复用。
	session, err := bootstrapSession(apiBase)
	if err != nil {
		logger.Fatal().Err(err).Msg("登录引导失败（api 不可达 / 凭据失效且库已初始化）")
	}

	// 共享 PG pool（探索图轮询直读）
	pool, err := db.NewPgPool(ctx, config.PostgresConfig{DSN: pgDSN, MaxConns: 5, MinConns: 1}, zerolog.Nop())
	if err != nil {
		logger.Fatal().Err(err).Msg("pg")
	}

	if err := runProfiles(ctx, selected, apiBase, session, pool, logger); err != nil {
		logger.Error().Err(err).Msg("e2e FAIL")
		fmt.Printf("✗ e2e FAIL: %v\n", err)
		os.Exit(1)
	}
	names := make([]string, len(selected))
	for i, p := range selected {
		names[i] = p.name
	}
	fmt.Printf("✓ e2e PASS profile=[%s]\n", strings.Join(names, ","))
}
