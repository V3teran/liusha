package main

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
)

// runProfiles 顺序跑被选的剧本：调 POST /chat → 轮询探索图。
func runProfiles(ctx context.Context, profs []profile, api taskAPI, logger zerolog.Logger) error {
	for _, p := range profs {
		// 走会话入口（POST /chat）
		conversationID, taskID, err := createChatTask(api.base, api.session, p.brief)
		if err != nil {
			return fmt.Errorf("profile %s: createChatTask: %w", p.name, err)
		}
		logger.Info().
			Str("profile", p.name).
			Str("task_id", taskID).
			Str("conversation_id", conversationID).
			Int("min_objectives", p.acceptance.MinObjectives).
			Int("min_actions", p.acceptance.MinActions).
			Int("min_results", p.acceptance.MinResults).
			Msg("chat scan dispatched")

		// 经公开 API 轮询探索图
		if err := pollTaskWithGraphStats(ctx, api, taskID, p.acceptance, &logger); err != nil {
			return fmt.Errorf("profile %s: %w", p.name, err)
		}

		logger.Info().Str("profile", p.name).Msg("✓ profile PASS")
		fmt.Printf("✓ profile=%s PASS\n", p.name)
	}
	return nil
}
