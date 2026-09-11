package core

import (
	"fmt"
	"sync"
)

// RegistryImpl 是 Registry 的完整实现。
type RegistryImpl struct {
	mu sync.RWMutex

	// Agent 工厂
	agents map[string]AgentFactory

	// Tool 实例
	tools map[string]Tool

	// Skill 实例
	skills map[string]Skill

	// 命名空间隔离
	namespaces map[string]*RegistryImpl
}

// NewRegistryImpl 创建注册中心实例。
func NewRegistryImpl() *RegistryImpl {
	return &RegistryImpl{
		agents:     make(map[string]AgentFactory),
		tools:      make(map[string]Tool),
		skills:     make(map[string]Skill),
		namespaces: make(map[string]*RegistryImpl),
	}
}

// ============================================
// Agent 注册
// ============================================

// RegisterAgent 注册 Agent 工厂。
func (r *RegistryImpl) RegisterAgent(agentType string, factory AgentFactory) error {
	if agentType == "" {
		return fmt.Errorf("agent type cannot be empty")
	}
	if factory == nil {
		return fmt.Errorf("agent factory cannot be nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// 检查是否已存在
	if _, exists := r.agents[agentType]; exists {
		return fmt.Errorf("agent type already registered: %s", agentType)
	}

	r.agents[agentType] = factory
	return nil
}

// CreateAgent 根据类型创建 Agent。
func (r *RegistryImpl) CreateAgent(agentType string, config AgentConfig) (Agent, error) {
	r.mu.RLock()
	factory, exists := r.agents[agentType]
	r.mu.RUnlock()

	if !exists {
		return nil, ErrAgentTypeNotFound{Type: agentType}
	}

	return factory(config)
}

// ListAgentTypes 列出已注册的 Agent 类型。
func (r *RegistryImpl) ListAgentTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make([]string, 0, len(r.agents))
	for t := range r.agents {
		types = append(types, t)
	}
	return types
}

// HasAgentType 检查 Agent 类型是否已注册。
func (r *RegistryImpl) HasAgentType(agentType string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.agents[agentType]
	return exists
}

// UnregisterAgent 注销 Agent 类型。
func (r *RegistryImpl) UnregisterAgent(agentType string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.agents[agentType]; !exists {
		return ErrAgentTypeNotFound{Type: agentType}
	}

	delete(r.agents, agentType)
	return nil
}

// ============================================
// Tool 注册
// ============================================

// RegisterTool 注册工具。
func (r *RegistryImpl) RegisterTool(tool Tool) error {
	if tool == nil {
		return fmt.Errorf("tool cannot be nil")
	}

	name := tool.Name()
	if name == "" {
		return fmt.Errorf("tool name cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// 检查是否已存在
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool already registered: %s", name)
	}

	r.tools[name] = tool
	return nil
}

// GetTool 获取工具。
func (r *RegistryImpl) GetTool(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tool, exists := r.tools[name]
	if !exists {
		return nil, fmt.Errorf("tool not found: %s", name)
	}

	return tool, nil
}

// ListTools 列出所有工具。
func (r *RegistryImpl) ListTools() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, tool)
	}
	return tools
}

// UnregisterTool 注销工具。
func (r *RegistryImpl) UnregisterTool(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[name]; !exists {
		return fmt.Errorf("tool not found: %s", name)
	}

	delete(r.tools, name)
	return nil
}

// HasTool 检查工具是否存在。
func (r *RegistryImpl) HasTool(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.tools[name]
	return exists
}

// ============================================
// Skill 注册
// ============================================

// RegisterSkill 注册技能。
func (r *RegistryImpl) RegisterSkill(skill Skill) error {
	if skill == nil {
		return fmt.Errorf("skill cannot be nil")
	}

	name := skill.Name()
	if name == "" {
		return fmt.Errorf("skill name cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// 检查是否已存在
	if _, exists := r.skills[name]; exists {
		return fmt.Errorf("skill already registered: %s", name)
	}

	r.skills[name] = skill
	return nil
}

// GetSkill 获取技能。
func (r *RegistryImpl) GetSkill(name string) (Skill, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	skill, exists := r.skills[name]
	if !exists {
		return nil, fmt.Errorf("skill not found: %s", name)
	}

	return skill, nil
}

// ListSkills 列出所有技能。
func (r *RegistryImpl) ListSkills() []Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	skills := make([]Skill, 0, len(r.skills))
	for _, skill := range r.skills {
		skills = append(skills, skill)
	}
	return skills
}

// UnregisterSkill 注销技能。
func (r *RegistryImpl) UnregisterSkill(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.skills[name]; !exists {
		return fmt.Errorf("skill not found: %s", name)
	}

	delete(r.skills, name)
	return nil
}

// HasSkill 检查技能是否存在。
func (r *RegistryImpl) HasSkill(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.skills[name]
	return exists
}

// ============================================
// 命名空间管理
// ============================================

// Namespace 获取或创建命名空间。
func (r *RegistryImpl) Namespace(name string) *RegistryImpl {
	r.mu.Lock()
	defer r.mu.Unlock()

	if ns, exists := r.namespaces[name]; exists {
		return ns
	}

	ns := NewRegistryImpl()
	r.namespaces[name] = ns
	return ns
}

// ListNamespaces 列出所有命名空间。
func (r *RegistryImpl) ListNamespaces() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.namespaces))
	for name := range r.namespaces {
		names = append(names, name)
	}
	return names
}

// DeleteNamespace 删除命名空间。
func (r *RegistryImpl) DeleteNamespace(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.namespaces[name]; !exists {
		return fmt.Errorf("namespace not found: %s", name)
	}

	delete(r.namespaces, name)
	return nil
}

// ============================================
// 批量操作
// ============================================

// Clear 清空所有注册。
func (r *RegistryImpl) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.agents = make(map[string]AgentFactory)
	r.tools = make(map[string]Tool)
	r.skills = make(map[string]Skill)
	r.namespaces = make(map[string]*RegistryImpl)
}

// Count 统计注册数量。
func (r *RegistryImpl) Count() (agents, tools, skills int) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.agents), len(r.tools), len(r.skills)
}

// ============================================
// 全局注册中心（单例）
// ============================================

var (
	globalRegistry     *RegistryImpl
	globalRegistryOnce sync.Once
)

// GlobalRegistry 获取全局注册中心。
func GlobalRegistry() *RegistryImpl {
	globalRegistryOnce.Do(func() {
		globalRegistry = NewRegistryImpl()
	})
	return globalRegistry
}
