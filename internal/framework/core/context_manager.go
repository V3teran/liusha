package core

import (
	"context"
	"fmt"
	"sync"
)

// ContextManager 是上下文管理器。
type ContextManager struct {
	mu      sync.RWMutex
	parent  *ContextManager
	values  map[string]any
	inherit map[string]bool // 可继承的 key
}

// NewContextManager 创建上下文管理器。
func NewContextManager(parent *ContextManager) *ContextManager {
	return &ContextManager{
		parent:  parent,
		values:  make(map[string]any),
		inherit: make(map[string]bool),
	}
}

// SetValue 设置上下文值。
func (c *ContextManager) SetValue(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[key] = value
}

// GetValue 获取上下文值（支持从父上下文查找）。
func (c *ContextManager) GetValue(key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 先查找本地
	if value, exists := c.values[key]; exists {
		return value, true
	}

	// 查找父上下文（如果允许继承）
	if c.parent != nil && c.inherit[key] {
		return c.parent.GetValue(key)
	}

	return nil, false
}

// Inherit 标记可继承的 key。
func (c *ContextManager) Inherit(keys ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, key := range keys {
		c.inherit[key] = true
	}
}

// Isolate 标记不可继承的 key。
func (c *ContextManager) Isolate(keys ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, key := range keys {
		delete(c.inherit, key)
	}
}

// InheritAll 继承父上下文的所有值。
func (c *ContextManager) InheritAll() error {
	if c.parent == nil {
		return fmt.Errorf("no parent context to inherit from")
	}

	c.parent.mu.RLock()
	defer c.parent.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	for key, value := range c.parent.values {
		c.values[key] = value
		c.inherit[key] = true
	}

	return nil
}

// Clone 克隆上下文。
func (c *ContextManager) Clone() *ContextManager {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cloned := NewContextManager(c.parent)

	for k, v := range c.values {
		cloned.values[k] = v
	}

	for k, v := range c.inherit {
		cloned.inherit[k] = v
	}

	return cloned
}

// Merge 合并另一个上下文。
func (c *ContextManager) Merge(other *ContextManager) {
	if other == nil {
		return
	}

	other.mu.RLock()
	defer other.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	for k, v := range other.values {
		c.values[k] = v
	}
}

// Export 导出上下文为 map。
func (c *ContextManager) Export() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()

	exported := make(map[string]any, len(c.values))
	for k, v := range c.values {
		exported[k] = v
	}

	return exported
}

// Import 导入 map 为上下文。
func (c *ContextManager) Import(data map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for k, v := range data {
		c.values[k] = v
	}
}

// Clear 清空上下文。
func (c *ContextManager) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.values = make(map[string]any)
	c.inherit = make(map[string]bool)
}

// Keys 返回所有 key。
func (c *ContextManager) Keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	keys := make([]string, 0, len(c.values))
	for k := range c.values {
		keys = append(keys, k)
	}

	return keys
}

// ============================================
// 任务上下文
// ============================================

// TaskContext 是任务执行上下文（扩展 Go 的 context.Context）。
type TaskContext struct {
	context.Context
	manager *ContextManager
}

// NewTaskContext 创建任务上下文。
func NewTaskContext(ctx context.Context, manager *ContextManager) *TaskContext {
	return &TaskContext{
		Context: ctx,
		manager: manager,
	}
}

// Set 设置任务上下文值。
func (t *TaskContext) Set(key string, value any) {
	t.manager.SetValue(key, value)
}

// Get 获取任务上下文值。
func (t *TaskContext) Get(key string) (any, bool) {
	return t.manager.GetValue(key)
}

// Manager 返回上下文管理器。
func (t *TaskContext) Manager() *ContextManager {
	return t.manager
}

// CreateChild 创建子上下文。
func (t *TaskContext) CreateChild(ctx context.Context) *TaskContext {
	childManager := NewContextManager(t.manager)
	return NewTaskContext(ctx, childManager)
}

// ============================================
// 上下文传播
// ============================================

// ContextPropagator 上下文传播器。
type ContextPropagator struct {
	propagateKeys []string // 需要传播的 key
	transformers  map[string]func(any) any // key -> 转换函数
}

// NewContextPropagator 创建上下文传播器。
func NewContextPropagator() *ContextPropagator {
	return &ContextPropagator{
		propagateKeys: make([]string, 0),
		transformers:  make(map[string]func(any) any),
	}
}

// AddKey 添加需要传播的 key。
func (p *ContextPropagator) AddKey(key string) {
	p.propagateKeys = append(p.propagateKeys, key)
}

// AddTransformer 添加值转换器。
func (p *ContextPropagator) AddTransformer(key string, transformer func(any) any) {
	p.transformers[key] = transformer
}

// Propagate 传播上下文。
func (p *ContextPropagator) Propagate(from, to *ContextManager) error {
	for _, key := range p.propagateKeys {
		value, exists := from.GetValue(key)
		if !exists {
			continue
		}

		// 应用转换器
		if transformer, ok := p.transformers[key]; ok {
			value = transformer(value)
		}

		to.SetValue(key, value)
		to.Inherit(key)
	}

	return nil
}
