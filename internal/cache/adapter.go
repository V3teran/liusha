package cache

import (
	"context"
	"fmt"

	agent "github.com/V3teran/liusha/internal/agent"
)

// GetPlanner 获取全局唯一的Planner配置
func (s *Store) GetPlanner(ctx context.Context) (agent.Agent, error) {
	return s.Planner(ctx)
}

// GetExecutor 获取全局唯一的Executor配置
func (s *Store) GetExecutor(ctx context.Context) (agent.Agent, error) {
	executors, err := s.EnabledDomainExecutors(ctx)
	if err != nil {
		return agent.Agent{}, err
	}
	if len(executors) == 0 {
		return agent.Agent{}, fmt.Errorf("no enabled executor found")
	}
	return executors[0], nil
}
