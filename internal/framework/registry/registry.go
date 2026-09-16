package registry

import (
	"fmt"
	"sync"

	"github.com/V3teran/liusha/internal/framework/core"
)

// AgentBuilder 是 Agent 构建器函数类型
// 接收配置，返回 Agent 实例
type AgentBuilder func(config core.AgentConfig) (core.Agent, error)

// Registry 是 Agent 注册表
// 业务层通过它注册 Agent 实现，Framework 通过它创建 Agent
type Registry struct {
	mu       sync.RWMutex
	builders map[string]AgentBuilder
}

// NewRegistry 创建 Agent 注册表
func NewRegistry() *Registry {
	return &Registry{
		builders: make(map[string]AgentBuilder),
	}
}

// Register 注册 Agent 构建器
// agentType: Agent 类型（planner/executor/monitor/evaluator）
// builder: Agent 构建器函数
func (r *Registry) Register(agentType string, builder AgentBuilder) error {
	if agentType == "" {
		return fmt.Errorf("agent type cannot be empty")
	}

	if builder == nil {
		return fmt.Errorf("agent builder cannot be nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.builders[agentType]; exists {
		return fmt.Errorf("agent type %q already registered", agentType)
	}

	r.builders[agentType] = builder
	return nil
}

// MustRegister 注册 Agent 构建器（失败时 panic）
func (r *Registry) MustRegister(agentType string, builder AgentBuilder) {
	if err := r.Register(agentType, builder); err != nil {
		panic(err)
	}
}

// CreateAgent 根据配置创建 Agent
func (r *Registry) CreateAgent(config core.AgentConfig) (core.Agent, error) {
	r.mu.RLock()
	builder, exists := r.builders[config.Type]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("agent type %q not registered", config.Type)
	}

	return builder(config)
}

// IsRegistered 检查 Agent 类型是否已注册
func (r *Registry) IsRegistered(agentType string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.builders[agentType]
	return exists
}

// RegisteredTypes 返回所有已注册的 Agent 类型
func (r *Registry) RegisteredTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make([]string, 0, len(r.builders))
	for agentType := range r.builders {
		types = append(types, agentType)
	}

	return types
}

// Unregister 注销 Agent 类型（用于测试）
func (r *Registry) Unregister(agentType string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.builders, agentType)
}

// Clear 清空所有注册（用于测试）
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.builders = make(map[string]AgentBuilder)
}

// globalRegistry 是全局注册表
var globalRegistry = NewRegistry()

// Register 向全局注册表注册 Agent
func Register(agentType string, builder AgentBuilder) error {
	return globalRegistry.Register(agentType, builder)
}

// MustRegister 向全局注册表注册 Agent（失败时 panic）
func MustRegister(agentType string, builder AgentBuilder) {
	globalRegistry.MustRegister(agentType, builder)
}

// CreateAgent 从全局注册表创建 Agent
func CreateAgent(config core.AgentConfig) (core.Agent, error) {
	return globalRegistry.CreateAgent(config)
}

// IsRegistered 检查 Agent 类型是否已在全局注册表注册
func IsRegistered(agentType string) bool {
	return globalRegistry.IsRegistered(agentType)
}

// RegisteredTypes 返回全局注册表中所有已注册的 Agent 类型
func RegisteredTypes() []string {
	return globalRegistry.RegisteredTypes()
}

// Global 返回全局注册表
func Global() *Registry {
	return globalRegistry
}
