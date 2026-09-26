// Package main 是 liusha 端到端验收触发器（active 模式）。
//
// CLI 用法：
//
//	go run ./cmd/e2e active:full   # 综合开放性扫描（压测 LLM 自主 recon + swarm）
//	go run ./cmd/e2e active:xss    # XSS 专项扫描
//
// Active 流程（按选中顺序串行跑）：
//  1. POST /chat body={"brief":"<自然语言任务简报>"} → 拿 (conversation_id, task_id)
//  2. 按 task_id 轮询知识图谱 → 满足 AcceptanceCriteria（objective/action/result 节点数）为 PASS
//
// 想加新漏洞类型：activeProfiles map 加一行（带 brief 自然语言描述）。
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/envx"
	"github.com/V3teran/liusha/internal/logx"
)

const (
	pollInterval        = 15 * time.Second
	defaultPollDeadline = 40 * time.Minute // 与 runner.solo_agent_run_timeout_seconds 对齐；让 main_task 在 e2e 超时前自然结束
	dialTimeout         = 10 * time.Second
	rawIOTimeout        = 100 * time.Second
)

// pollDeadline 从 ENV LIUSHA_E2E_POLL_DEADLINE_SECONDS 读取（开发期可调），缺省 40 分钟。
func pollDeadline() time.Duration {
	if v := os.Getenv("LIUSHA_E2E_POLL_DEADLINE_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return defaultPollDeadline
}

func main() {
	logger := logx.New("e2e")
	ctx := context.Background()

	apiBase := envx.OrDefault("LIUSHA_API_BASE", "http://localhost:8090")
	apiKey := envx.OrDefault("LIUSHA_API_KEY", "changeme-dev-key")
	pgDSN := envx.OrDefault("LIUSHA_POSTGRES_DSN", "postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable")
	// Phase 2: 暂时不需要这些变量（passive 模式专用）
	// proxyURL := envx.OrDefault("LIUSHA_PROXY_ADDR", "http://localhost:8888")
	// vulnBase := envx.OrDefault("LIUSHA_VULNAPP_BASE", "http://111.229.193.40:38001")

	activeSel, err := selectProfiles(os.Args[1:])
	if err != nil {
		logger.Fatal().Err(err).Msg("select profiles")
	}
	if len(activeSel) == 0 {
		logger.Fatal().Msg("无 profile 可跑（用法：active:<name>）")
	}

	// 共享 PG pool（passive / active 均用）
	pool, err := db.NewPgPool(ctx, pgDSN, 5, 1, 0, 0)
	if err != nil {
		logger.Fatal().Err(err).Msg("pg")
	}
	defer pool.Close()

	// ---- Active 流水线 ----
	if len(activeSel) > 0 {
		if err := runActiveProfiles(ctx, activeSel, apiBase, apiKey, pool, logger); err != nil {
			logger.Error().Err(err).Msg("e2e active FAIL")
			fmt.Printf("✗ e2e active FAIL: %v\n", err)
			os.Exit(1)
		}
		names := make([]string, len(activeSel))
		for i, p := range activeSel {
			names[i] = p.name
		}
		fmt.Printf("✓ e2e active PASS profile=[%s]\n", strings.Join(names, ","))
	}
}
