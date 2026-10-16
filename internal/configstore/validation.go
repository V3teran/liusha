package cache

import (
	"context"
	"fmt"
	"strings"
)

// ValidateAgentConfigs 验证所有必需的 agent 配置是否完整。
// 在进程启动时调用，确保关键配置已加载（避免运行时才发现 charter 为空）。
func (s *Store) ValidateAgentConfigs(ctx context.Context) error {
	requiredAgents := []string{"executor", "planner", "evaluator", "monitor"}

	var errors []string
	for _, code := range requiredAgents {
		agent, err := s.AgentByCode(ctx, code)
		if err != nil {
			errors = append(errors, fmt.Sprintf("  - %s: 未找到配置 (%v)", code, err))
			continue
		}

		// 验证 system_prompt 非空
		if strings.TrimSpace(agent.SystemPrompt) == "" {
			errors = append(errors, fmt.Sprintf("  - %s: system_prompt 为空", code))
		}

		// 验证 code 匹配
		if agent.Code != code {
			errors = append(errors, fmt.Sprintf("  - %s: code 不匹配（期望 %s，实际 %s）", code, code, agent.Code))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("agent 配置验证失败，请运行 make reseed:\n%s", strings.Join(errors, "\n"))
	}

	return nil
}

// ValidateAgentSystemPrompt 验证单个 agent 的 system_prompt 是否非空。
// 用于动态验证（如前端更新配置后）。
