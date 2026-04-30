// Package done_validator 收纳各 Skill 的 Done 校验器实现（黑客松借鉴共识 C：Done 系统层裁决）。
//
// 与 internal/agent/tool.DoneValidator interface 的关系：
//   - 这里只放具体 validator（如 BACValidator）+ 一个 key 集合 registry。
//   - registry 仅作"key 合法性"快速检查：skill loader 在加载 SKILL.md 时调
//     IsRegistered(key) 验证 frontmatter 的 done_validator 字段。
//   - 实际的 validator 实例由 cmd/scanner 在装配 ReAct task 时按需构造
//     （因为 BACValidator 依赖 per-task 的 engagementID）。
//
// 这样拆分的原因：
//   - 注册时拿不到 engagementID（engagementID 是 per-task 的运行期值）；
//   - 让 init() 只承担"key 合法性宣告"，不承担"绑实例"，避免全局耦合。
package done_validator

import "sync"

var (
	registryLock sync.RWMutex
	registry     = make(map[string]struct{})
)

// Register 把一个 done_validator key 注册到全局 set。重复调用幂等。
func Register(key string) {
	registryLock.Lock()
	defer registryLock.Unlock()
	registry[key] = struct{}{}
}

// IsRegistered 报告该 key 是否已注册（用于 skill loader 校验）。
func IsRegistered(key string) bool {
	registryLock.RLock()
	defer registryLock.RUnlock()
	_, ok := registry[key]
	return ok
}

// init 声明本包提供的 validator key。
func init() {
	Register("bac_v1")
}
