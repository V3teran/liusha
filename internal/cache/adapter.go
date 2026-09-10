package configstore

import (
	"context"
	"fmt"

	cfgagent "github.com/V3teran/liusha/internal/config/agent"
)

// GetPlanner 获取全局唯一的Planner配置
func (s *Store) GetPlanner(ctx context.Context) (cfgagent.Agent, error) {
	return s.Planner(ctx)
}

// GetExecutor 获取全局唯一的Executor配置
func (s *Store) GetExecutor(ctx context.Context) (cfgagent.Agent, error) {
	executors, err := s.EnabledDomainExecutors(ctx)
	if err != nil {
		return cfgagent.Agent{}, err
	}
	if len(executors) == 0 {
		return cfgagent.Agent{}, fmt.Errorf("no enabled executor found")
	}
	return executors[0], nil
}
