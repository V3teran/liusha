package credential

import "context"

// Provider 是活凭证存储抽象，业务层依赖此接口而非具体后端，
// 便于用 fake / mock 在单元测试中替换 Redis。
//
// 全部操作以单个 host 为作用域（存储键 = 一个 hash）：跨 host 的批量编排
// 属于调用方（HTTP 层循环），存储接口不承载多键事务。
type Provider interface {
	// SaveIdentities 批量 upsert 单个 host 下的登录身份（按 Username 覆盖写）。
	// CreatedAt 为空时由存储侧打点；非空时保留（更新不刷新录入时间）。
	// caller 误传 anonymous 会被防御性跳过，不污染存储。
	SaveIdentities(ctx context.Context, host string, ids []Identity) error

	// GetIdentitiesByHost 返回指定 host 的全部登录身份（录入 + 会话），
	// 按存储序返回，不注入 anonymous。
	GetIdentitiesByHost(ctx context.Context, host string) ([]Identity, error)

	// DeleteIdentity 删除单个身份（field 级）。
	DeleteIdentity(ctx context.Context, host, name string) error

	// DeleteSessions 删除 host 下全部会话用户名，返回删除数（录入身份不动）。
	DeleteSessions(ctx context.Context, host string) (int, error)

	// DeleteHost 清空 host 下的全部身份。
	DeleteHost(ctx context.Context, host string) error

	// ListHosts 枚举有凭证的 host 及其用户名计数（只读 field 名，不读凭证值）。
	ListHosts(ctx context.Context) ([]HostSummary, error)
}

// TaskStatusLookup 是清扫器对任务终态的判定依赖：返回任务当前状态与
// 任务是否存在。实现方（runner）闭包 task store；测试用 fake。
type TaskStatusLookup func(ctx context.Context, taskID string) (status string, exists bool)

// 任务状态字面值（与 task 表 CHECK 约束一致；清扫只关心是否终态）。
const (
	statusActive    = "active"
	statusCompleted = "completed"
	statusAborted   = "aborted"
)

// SweepTerminalSessions 清扫已终态任务的会话用户名：遍历全部 host 的会话
// 用户名，归属任务已终态（completed/aborted）或任务行已不存在（级联删除）
// 即删除；任务仍 active（含 Reopen 复活中）不动。
//
// 这是任务终态清理的崩溃兜底：正常路径任务结束时 handler 已删过自己的
// 会话，runner 进程崩溃没跑过清理的任务由这里按事实回收——判定依据是
// 任务状态这个事实，不是任何时间猜测。
func SweepTerminalSessions(ctx context.Context, p Provider, lookup TaskStatusLookup) (removed int, err error) {
	hosts, err := p.ListHosts(ctx)
	if err != nil {
		return 0, err
	}
	for _, h := range hosts {
		if h.Sessions == 0 {
			continue
		}
		ids, err := p.GetIdentitiesByHost(ctx, h.Host)
		if err != nil {
			continue // 单 host 失败不拖累整轮清扫，下轮重试
		}
		for _, id := range ids {
			if !IsSessionIdentity(id.Username) {
				continue
			}
			taskID, ok := SessionTaskID(id.Username)
			if !ok || taskID == "" {
				continue // 全局会话无归属任务，交由人工管理
			}
			status, exists := lookup(ctx, taskID)
			if exists && status == statusActive {
				continue
			}
			if dErr := p.DeleteIdentity(ctx, h.Host, id.Username); dErr == nil {
				removed++
			}
		}
	}
	return removed, nil
}
