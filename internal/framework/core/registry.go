package core

import (
	"sync"
)

// Registry 是组件注册中心（Agent、Tool、Skill 的统一注册机制）。
type Registry struct {
	agents map[string]AgentFactory
	tools  map[string]any // 值类型待定，兼容不同工具接口
	skills map[string]any // 值类型待定，兼容不同技能接口
	mu     sync.RWMutex
}

// NewRegistry 创建注册中心。
func NewRegistry() *Registry {
	return &Registry{
		agents: make(map[string]AgentFactory),
		tools:  make(map[string]any),
		skills: make(map[string]any),
	}
}

// RegisterAgent 注册 Agent 工厂。
func (r *Registry) RegisterAgent(agentType string, factory AgentFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[agentType] = factory
}

// CreateAgent 根据类型创建 Agent。
func (r *Registry) CreateAgent(agentType string, config AgentConfig) (Agent, error) {
	r.mu.RLock()
	factory, exists := r.agents[agentType]
	r.mu.RUnlock()

	if !exists {
		return nil, ErrAgentTypeNotFound{Type: agentType}
	}

	return factory(config)
}

// ListAgentTypes 列出已注册的 Agent 类型。
func (r *Registry) ListAgentTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make([]string, 0, len(r.agents))
	for t := range r.agents {
		types = append(types, t)
	}
	return types
}

// HasAgentType 检查 Agent 类型是否已注册。
func (r *Registry) HasAgentType(agentType string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.agents[agentType]
	return exists
}

// RegisterTool 注册工具。
func (r *Registry) RegisterTool(name string, tool any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[name] = tool
}

// GetTool 获取工具。
func (r *Registry) GetTool(name string) (any, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, exists := r.tools[name]
	return tool, exists
}

// ListTools 列出已注册的工具。
func (r *Registry) ListTools() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// RegisterSkill 注册技能。
func (r *Registry) RegisterSkill(name string, skill any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills[name] = skill
}

// GetSkill 获取技能。
func (r *Registry) GetSkill(name string) (any, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	skill, exists := r.skills[name]
	return skill, exists
}

// ListSkills 列出已注册的技能。
func (r *Registry) ListSkills() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.skills))
	for name := range r.skills {
		names = append(names, name)
	}
	return names
}

// Clear 清空所有注册。
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents = make(map[string]AgentFactory)
	r.tools = make(map[string]any)
	r.skills = make(map[string]any)
}
