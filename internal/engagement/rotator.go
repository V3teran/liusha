package engagement

import (
	"context"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/config"
)

// FindingCounter 是 Rotator 阈值检查需要的最小 finding 表只读访问。
//
// 由 *finding.Store 自动满足（v1.2 新增 CountByEngagement 方法）。
// 接口形式声明便于单元测试注入 fake。
type FindingCounter interface {
	CountByEngagement(ctx context.Context, engagementID string) (int, error)
}

// RotateLimits 控制 proxy 模式 engagement 何时滚动新一份。
//
// 任一阈值命中即触发轮转：
//   - MaxAge：从 CreatedAt 起 elapsed 超过此值
//   - MaxStateSize：notes jsonb 字节数超过此值
//   - MaxFindings：当前 engagement 累计 finding 数超过此值
//
// 注：原有 HintCarryTopN 字段已删除（v1.2 P2 复审）——lesson 表已是更优的
// 跨 engagement 长期知识层（永久 + content_hash 去重 + hit_count）；轮转时再 carry
// 一次会让新 engagement 的 user prompt 同时含 carry hint + lesson 重复内容。
type RotateLimits struct {
	MaxAge       time.Duration
	MaxStateSize int
	MaxFindings  int
}

// fallbackRotateLimits 是 RotateLimits 字段缺省时的兜底值。
//   - 24h 一轮：覆盖一次工作日浏览量
//   - 1 MiB jsonb：read_notes 输出再大就把 LLM context 压垮
//   - 300 findings：中型目标一轮可能积累几百条，过早轮转会切断 lesson 沉淀链
//
// 正常路径由 cmd/scanner 通过 RotateLimitsFromConfig 从 yaml 注入；
// 这里仅作为 caller 失误时的最后防线。
var fallbackRotateLimits = RotateLimits{
	MaxAge:       24 * time.Hour,
	MaxStateSize: 1 << 20,
	MaxFindings:  300,
}

// RotateLimitsFromConfig 从 yaml 配置构造阈值集。
// 任一字段为 0 时由 ApplyDefaults 兜底（caller 应在 config.Load 内已应用）。
func RotateLimitsFromConfig(c config.EngagementConfig) RotateLimits {
	return RotateLimits{
		MaxAge:       time.Duration(c.MaxAgeHours) * time.Hour,
		MaxStateSize: c.MaxStateSizeBytes,
		MaxFindings:  c.MaxFindings,
	}
}

// Rotator 包装 LookupOrCreate，给 proxy 模式按阈值自动滚动 engagement。
//
// 整站模式（mode != ModeProxy）直通；由上层逻辑自决何时 close。
type Rotator struct {
	engs   *Store
	finds  FindingCounter
	limits RotateLimits
	now    func() time.Time // 测试可注入；nil 用 time.Now
}

// NewRotator 构造 Rotator；engs 必填；finds 可空（空时 MaxFindings 阈值跳过检查）；
// limits 任一字段 ≤0 时用 fallbackRotateLimits 对应值。
func NewRotator(engs *Store, finds FindingCounter, limits RotateLimits) *Rotator {
	if limits.MaxAge <= 0 {
		limits.MaxAge = fallbackRotateLimits.MaxAge
	}
	if limits.MaxStateSize <= 0 {
		limits.MaxStateSize = fallbackRotateLimits.MaxStateSize
	}
	if limits.MaxFindings <= 0 {
		limits.MaxFindings = fallbackRotateLimits.MaxFindings
	}
	return &Rotator{engs: engs, finds: finds, limits: limits}
}

// EnsureActive 返回当前 (tenant, host) 应使用的 active engagement_id。
//
// 流程：
//  1. LookupOrCreate 拿到当前 active engagement
//  2. 若 mode != proxy 直通返回（整站模式不轮转）
//  3. 若 mode == proxy：检查阈值；命中则 abort 旧 + create 新 engagement（hint 不 carry，
//     由 lesson 表跨 engagement 持久化覆盖该角色）
func (r *Rotator) EnsureActive(ctx context.Context, host string, mode Mode) (string, error) {
	eng, err := r.engs.LookupOrCreate(ctx, host, mode)
	if err != nil {
		return "", err
	}
	if eng.Mode != ModeProxy {
		return eng.ID, nil
	}
	if !r.shouldRotate(ctx, eng) {
		return eng.ID, nil
	}
	return r.rotate(ctx, eng)
}

// shouldRotate 评估 eng 是否触达任一阈值。任一命中即返 true。
//
// 单项检查失败（如 finds.CountByEngagement 报错）按"未触达"处理——保守策略，
// 不让监控/计数错误把 engagement 转飞；下次 EnsureActive 还有机会评估。
func (r *Rotator) shouldRotate(ctx context.Context, eng Engagement) bool {
	if r.ageExceeded(eng) {
		return true
	}
	if r.stateSizeExceeded(eng) {
		return true
	}
	if r.findingCountExceeded(ctx, eng) {
		return true
	}
	return false
}

func (r *Rotator) ageExceeded(eng Engagement) bool {
	now := time.Now
	if r.now != nil {
		now = r.now
	}
	return now().Sub(eng.CreatedAt) >= r.limits.MaxAge
}

func (r *Rotator) stateSizeExceeded(eng Engagement) bool {
	return len(eng.Notes) >= r.limits.MaxStateSize
}

func (r *Rotator) findingCountExceeded(ctx context.Context, eng Engagement) bool {
	if r.finds == nil {
		return false
	}
	cnt, err := r.finds.CountByEngagement(ctx, eng.ID)
	if err != nil {
		return false
	}
	return cnt >= r.limits.MaxFindings
}

// rotate 关旧 engagement + 建新。返回新 engagement_id。
//
// 不再 carry hint：lesson 表本身已是按 host 持久化的长期知识层，新 engagement
// 通过 loadLessonsForPrompt 自动读取；这里再 carry 会重复。
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
