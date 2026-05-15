package engagement

import (
	"context"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/config"
)

// RotateLimits 控制 proxy 模式 engagement 何时滚动新一份。
//
// 单一阈值 MaxAge：作为新建 proxy session 的 TTL（expires_at = now + MaxAge）；
// 判断轮转则直接看 DB 里的 expires_at 字段，不再依赖 CreatedAt + MaxAge 推算。
type RotateLimits struct {
	MaxAge time.Duration
}

// fallbackRotateLimits 是 RotateLimits 字段缺省时的兜底值。
//   - 24h 一轮：覆盖一次工作日浏览量；与 notes TTL 严格对齐
var fallbackRotateLimits = RotateLimits{
	MaxAge: 24 * time.Hour,
}

// RotateLimitsFromConfig 从 yaml 配置构造阈值集。
// 任一字段为 0 时由 ApplyDefaults 兜底（caller 应在 config.Load 内已应用）。
func RotateLimitsFromConfig(c config.EngagementConfig) RotateLimits {
	return RotateLimits{
		MaxAge: time.Duration(c.MaxAgeHours) * time.Hour,
	}
}

// Rotator 给 proxy 模式按 expires_at 自动滚动 engagement。
//
// 整站（browser）模式不经过 Rotator：每次主动 CreateBrowserScan，按需 Abort。
//
// 轮转的「内容」：
//   - engagement 表：旧行 status=aborted（行保留作历史档案）+ 新行 status=active 新 UUID
//   - notes Redis key：新 UUID 自动是新 key（旧 key 留着等 TTL）
//   - finding/flow/agent_run 计数：按新 engagement_id 自然从 0 重计
//   - lesson/credential：跨 engagement 持久化，不轮转
type Rotator struct {
	engs   *Store
	limits RotateLimits
	now    func() time.Time // 测试可注入；nil 用 time.Now
}

// NewRotator 构造 Rotator。engs 必填；limits.MaxAge ≤0 时回退 fallback 24h。
func NewRotator(engs *Store, limits RotateLimits) *Rotator {
	if limits.MaxAge <= 0 {
		limits.MaxAge = fallbackRotateLimits.MaxAge
	}
	return &Rotator{engs: engs, limits: limits}
}

// EnsureProxySession 返回当前 active proxy session 的 engagement_id。
//
// 流程：
//  1. LookupActiveProxy 看是否已有
//  2. 没有：CreateProxySession(MaxAge) 建新
//  3. 已有 & expires_at 未过期：直接返回
//  4. 已有 & 已过期：Abort 旧 + CreateProxySession 建新
//
// v0033：删除 host 参数——proxy session 接受任意 host 流量，按时间窗轮转。
func (r *Rotator) EnsureProxySession(ctx context.Context) (string, error) {
	eng, ok, err := r.engs.LookupActiveProxy(ctx)
	if err != nil {
		return "", fmt.Errorf("lookup active proxy: %w", err)
	}
	if !ok {
		newEng, err := r.engs.CreateProxySession(ctx, r.limits.MaxAge)
		if err != nil {
			return "", fmt.Errorf("create proxy session: %w", err)
		}
		return newEng.ID, nil
	}
	if !r.expired(eng) {
		return eng.ID, nil
	}
	return r.rotate(ctx, eng)
}

// expired 判断 engagement 是否已超时间窗。
// ExpiresAt 为 nil 时（理论上 proxy 模式不应出现）视作永不过期。
func (r *Rotator) expired(eng Engagement) bool {
	if eng.ExpiresAt == nil {
		return false
	}
	now := time.Now
	if r.now != nil {
		now = r.now
	}
	return now().After(*eng.ExpiresAt)
}

// rotate 关旧 proxy session + 建新。返回新 engagement_id。
//
// 旧 engagement 行保留在表里（status=aborted）作历史档案。
// notes Redis key 不主动 DEL，等 TTL 自然过期。
func (r *Rotator) rotate(ctx context.Context, oldEng Engagement) (string, error) {
	if err := r.engs.Abort(ctx, oldEng.ID, ""); err != nil {
		return "", fmt.Errorf("rotate: abort old engagement %s: %w", oldEng.ID, err)
	}
	newEng, err := r.engs.CreateProxySession(ctx, r.limits.MaxAge)
	if err != nil {
		return "", fmt.Errorf("rotate: create new proxy session: %w", err)
	}
	return newEng.ID, nil
}
