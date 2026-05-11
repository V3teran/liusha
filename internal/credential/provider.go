package credential

import "context"

// Provider 是活凭证存储抽象，业务层依赖此接口而非具体后端，
// 便于用 fake / mock 在单元测试中替换 Redis。
type Provider interface {
	// BatchSave 批量写入按 host 分组的身份；ttlSeconds=0 表示永不过期。
	// caller 误传 anonymous 会被防御性跳过，不污染存储。
	BatchSave(ctx context.Context, byHost map[string][]Identity, ttlSeconds int) error

	// GetIdentitiesByHost 返回指定 host 的所有预录入真实身份。
	// 不注入 anonymous：anonymous 是 LLM 临时构造的测试概念，不是持久化对象。
	GetIdentitiesByHost(ctx context.Context, host string) ([]Identity, error)

	// List 形态等价于 GetIdentitiesByHost，但以 map 形式返回，方便调用方传给批量 API。
	List(ctx context.Context, host string) (map[string][]Identity, error)

	// Delete 清空指定 host 下所有持久化的身份。
	Delete(ctx context.Context, host string) error
}
