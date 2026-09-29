package executor

import (
	"github.com/V3teran/liusha/internal/registry"
)

// Registry 是 executor 包对 registry.Registry 的薄封装
// 用于在 Engine 中统一管理工具注册
type Registry struct {
	inner *registry.Registry
}

// NewRegistry 创建工具注册表
func NewRegistry(inner *registry.Registry) *Registry {
	return &Registry{inner: inner}
}

// List 返回所有已注册的工具
func (r *Registry) List() []registry.Tool {
	schemas := r.inner.Schemas()
	tools := make([]registry.Tool, 0, len(schemas))
	for _, schema := range schemas {
		if tool, ok := r.inner.Get(schema.Name); ok {
			tools = append(tools, tool)
		}
	}
	return tools
}

// Register 注册工具
func (r *Registry) Register(tool registry.Tool) {
	r.inner.Register(tool)
}
