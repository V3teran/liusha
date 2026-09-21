package main

import (
	"context"
	"fmt"
	// Phase 2: 暂时不需要 time（passive 模式的 pollDeadline 已移到 main.go）
	// "time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	// Phase 2: 暂时注释掉 passive 相关 imports（以后恢复）
	// "github.com/V3teran/liusha/internal/task"
	// "github.com/V3teran/liusha/internal/agentrun"
	// "github.com/V3teran/liusha/internal/finding"
)

// profilePlan 是单个 profile 的运行计划：解析后的 host + 加载好的样本。
// Phase 2: 暂时注释掉（passive 模式专用）
// type profilePlan struct {
// 	prof      profile
// 	host      string
// 	samples   []string
// 	samplePth string
// }

// runActiveProfiles 顺序跑被选的 active profile：调 POST /chat → 轮询知识图谱。
//
// Phase 2: 只使用知识图谱 API 轮询（验证完整认知循环）
func runActiveProfiles(ctx context.Context, profs []activeProfile, apiBase, apiKey string, pool *pgxpool.Pool, logger zerolog.Logger) error {
	kgClient := NewKnowledgeGraphClient(pool)

	for _, ap := range profs {
		// 走会话入口（POST /chat）
		convID, taskID, err := createChatScan(apiBase, apiKey, ap.brief)
		if err != nil {
			return fmt.Errorf("active profile %s: createChatScan: %w", ap.name, err)
		}
		logger.Info().
			Str("profile", ap.name).
			Str("task_id", taskID).
			Str("conversation_id", convID).
			Int("min_objectives", ap.acceptance.MinObjectives).
			Int("min_actions", ap.acceptance.MinActions).
			Int("min_results", ap.acceptance.MinResults).
			Msg("active chat scan dispatched")

		// 使用知识图谱轮询（直接查询数据库）
		if err := pollTaskWithGraphStats(ctx, kgClient, taskID, ap.acceptance, &logger); err != nil {
			return fmt.Errorf("active profile %s: %w", ap.name, err)
		}

		logger.Info().Str("profile", ap.name).Msg("✓ active profile PASS")
		fmt.Printf("✓ active profile=%s PASS\n", ap.name)
	}
	return nil
}

// ========================================
// Phase 2: 以下所有 passive 相关函数暂时注释掉
// 等实现 passive 模式的知识图谱轮询后再恢复
// ========================================

/*
func discoverPassiveTasks(ctx context.Context, ts *task.Store, hosts map[string]struct{}, baseline time.Time) ([]string, error) {
	tasks, err := ts.List(ctx, 512)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, t := range tasks {
		if !t.CreatedAt.After(baseline) {
			continue
		}
		if _, ok := hosts[t.TargetHost]; ok {
			ids = append(ids, t.ID)
		}
	}
	return ids, nil
}

func buildPlans(selected []profile, vulnBase string) ([]profilePlan, error) {
	// ... 实现略
	return nil, nil
}

func enrollAllCreds(apiBase, apiKey, vulnBase string) error {
	// ... 实现略
	return nil
}

func runAllUnified(ctx context.Context, plans []profilePlan, proxyHostPort, apiBase, apiKey string, pool *pgxpool.Pool, logger zerolog.Logger) error {
	// ... 实现略（很长的函数）
	return nil
}

func resolveSampleHost(vulnBase string, samples []string) (string, error) {
	// ... 实现略
	return "", nil
}
*/
