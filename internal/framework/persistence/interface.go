package persistence

import (
	"context"

	"github.com/V3teran/liusha/internal/framework/core"
)

// Store 是统一的持久化接口。
// 封装 StateManager、Checkpointer、EventStore。
type Store interface {
	// StateManager 返回状态管理器
	StateManager() core.StateManager[any]

	// Checkpointer 返回检查点管理器
	Checkpointer() core.Checkpointer

	// EventStore 返回事件存储
	EventStore() core.EventStore

	// Close 关闭存储连接
	Close() error

	// Health 健康检查
	Health(ctx context.Context) error
}

// StoreConfig 是存储配置。
type StoreConfig struct {
	// 存储类型：memory, postgres, redis
	Type string

	// 连接字符串（postgres: DSN, redis: URL）
	ConnectionString string

	// 最大连接数
	MaxConnections int

	// 连接超时（毫秒）
	ConnectTimeoutMs int64

	// 是否启用缓存
	EnableCache bool

	// 缓存 TTL（秒）
	CacheTTL int64
}

// Transaction 是事务接口。
type Transaction interface {
	// Commit 提交事务
	Commit(ctx context.Context) error

	// Rollback 回滚事务
	Rollback(ctx context.Context) error

	// StateManager 返回事务内的状态管理器
	StateManager() core.StateManager[any]
}

// TransactionalStore 是支持事务的存储。
type TransactionalStore interface {
	Store

	// Begin 开始事务
	Begin(ctx context.Context) (Transaction, error)
}

// CachedStore 是带缓存的存储。
type CachedStore interface {
	Store

	// Invalidate 失效缓存
	Invalidate(ctx context.Context, keys []string) error

	// ClearCache 清空所有缓存
	ClearCache(ctx context.Context) error
}

// MigrationRunner 是数据库迁移接口。
type MigrationRunner interface {
	// Migrate 执行迁移（升级到最新版本）
	Migrate(ctx context.Context) error

	// Rollback 回滚迁移（回退到指定版本）
	Rollback(ctx context.Context, version int) error

	// Version 获取当前版本
	Version(ctx context.Context) (int, error)
}

// BackupManager 是备份管理器。
type BackupManager interface {
	// Backup 备份数据
	Backup(ctx context.Context, path string) error

	// Restore 恢复数据
	Restore(ctx context.Context, path string) error

	// ListBackups 列出所有备份
	ListBackups(ctx context.Context) ([]BackupInfo, error)
}

// BackupInfo 是备份信息。
type BackupInfo struct {
	// 备份文件路径
	Path string `json:"path"`

	// 备份时间（Unix 毫秒）
	Timestamp int64 `json:"timestamp"`

	// 备份大小（字节）
	SizeBytes int64 `json:"size_bytes"`

	// 数据版本
	Version int `json:"version"`
}

// StoreFactory 创建存储实例。
type StoreFactory func(config StoreConfig) (Store, error)

// Registry 是存储类型注册中心。
type Registry interface {
	// Register 注册存储类型
	Register(storeType string, factory StoreFactory)

	// Create 创建存储实例
	Create(config StoreConfig) (Store, error)

	// List 列出已注册的存储类型
	List() []string
}
