package executor

import (
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// Config 是 Executor/Coordinator 的配置
type Config struct {
	TaskID string
	Host   string // 虚拟主机地址

	// 数据存储
	World        *knowledgegraph.Store
	TrafficStore *traffic.AgentStore
	ProxyStore   *traffic.ProxyStore
	FindingStore *finding.Store

	// 服务
	ControlPlane *controlplane.Store
	Router       *provider.Router
	Registry     *registry.Registry

	// AgentFunc 用于创建 Coordinator
	AgentFunc AgentFunc

	Logger zerolog.Logger
}

// NewCoordinatorFromConfig 从配置创建 Coordinator
func NewCoordinatorFromConfig(cfg Config) *Coordinator {
	return NewCoordinator(
		cfg.TaskID,
		cfg.Host,
		cfg.FindingStore,
		cfg.AgentFunc,
		cfg.Logger,
	)
}
