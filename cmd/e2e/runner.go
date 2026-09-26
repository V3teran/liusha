package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// runActiveProfiles 顺序跑被选的 active profile：调 POST /chat → 轮询知识图谱。
func runActiveProfiles(ctx context.Context, profs []activeProfile, apiBase, apiKey string, pool *pgxpool.Pool, logger zerolog.Logger) error {
	kgClient := NewExplorationGraphClient(pool)

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
