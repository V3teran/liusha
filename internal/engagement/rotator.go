package engagement

import (
	"context"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/config"
)

// RotateLimits 控制 proxy 模式 engagement 何时滚动新一份。
//
// 单一阈值 MaxAge：从 CreatedAt 起 elapsed 超过此值即触发轮转。
// 历史 MaxStateSize（notes jsonb 字节）和 MaxFindings（累计 finding 数）阈值已删——
// notes 走 Redis TTL 自治，finding 计数本身不应触发轮转（中型目标几百条很常见，
// 提前轮转无实际收益且引入"轮转时机与 TTL 不对齐"问题）。
type RotateLimits struct {
	MaxAge time.Duration
}

// fallbackRotateLimits 是 RotateLimits 字段缺省时的兜底值。
//   - 24h 一轮：覆盖一次工作日浏览量；与 notes TTL 严格对齐
//
// 正常路径由 cmd/scanner 通过 RotateLimitsFromConfig 从 yaml 注入；
// 这里仅作为 caller 失误时的最后防线。
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

// Rotator 包装 LookupOrCreate，给 proxy 模式按 MaxAge 自动滚动 engagement。
//
// 整站模式（mode != ModeProxy）直通；由上层逻辑自决何时 close。
//
// 轮转的"内容"：
//   - engagement 表：旧行 status=aborted（行保留作历史档案）+ 新行 status=active 新 UUID
//   - notes Redis key：新 UUID 自动是新 key（旧 key 留着等 24h TTL）
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

// EnsureActive 返回当前 host 应使用的 active engagement_id。
//
// 流程：
//  1. LookupOrCreate 拿到当前 active engagement
//  2. 若 mode != proxy 直通返回（整站模式不轮转）
//  3. 若 mode == proxy：检查 MaxAge；命中则 abort 旧 + create 新
func (r *Rotator) EnsureActive(ctx context.Context, host string, mode Mode) (string, error) {
	eng, err := r.engs.LookupOrCreate(ctx, host, mode)
	if err != nil {
		return "", err
	}
	if eng.Mode != ModeProxy {
		return eng.ID, nil
	}
	if !r.ageExceeded(eng) {
		return eng.ID, nil
	}
	return r.rotate(ctx, eng)
}

func (r *Rotator) ageExceeded(eng Engagement) bool {
	now := time.Now
	if r.now != nil {
		now = r.now
	}
	return now().Sub(eng.CreatedAt) >= r.limits.MaxAge
}

// rotate 关旧 engagement + 建新。返回新 engagement_id。
//
// 旧 engagement 行保留在表里（status=aborted）作历史档案——
// lesson/credential 按 host 跨 engagement 持久化，新 engagement 仍能查到。
// notes Redis key 不主动 DEL，等 24h TTL 自然过期。
//
// 失败处理：
//   - Abort 失败：返错（旧 engagement 没正确 close 会破坏 active 唯一约束）
//   - LookupOrCreate（建新）失败：返错
func (r *Rotator) rotate(ctx context.Context, oldEng Engagement) (string, error) {
	if err := r.engs.Abort(ctx, oldEng.ID, ""); err != nil {
		return "", fmt.Errorf("rotate: abort old engagement %s: %w", oldEng.ID, err)
	}
	newEng, err := r.engs.LookupOrCreate(ctx, oldEng.TargetHost, oldEng.Mode)
	if err != nil {
		return "", fmt.Errorf("rotate: create new engagement: %w", err)
	}
	return newEng.ID, nil
}
