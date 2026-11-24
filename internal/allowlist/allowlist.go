// Package allowlist 判定一个名字在不在名单内。function_tools、cli_tools、skills 三类 agent
// 名单共用这一处判定。
//
// 它不引用任何业务包：名单的使用方有规划者、技能视图、工具清单、HTTP 接口……它们不必为了
// 判定一个名字而引用工具注册表，再由注册表带上模型框架与出厂自定义引擎（沙盒服务端曾因此
// 在自己的依赖里背上这两样）。
package allowlist

import "slices"

// Wildcard 是集合字段的通配元素：全部，含以后新增。
const Wildcard = "*"

// Allows 报告 name 是否在名单内：
//   - nil = 默认拒绝；
//   - 空切片 = 空集（明确不装配任何项）；
//   - 含通配 "*" = 全部（含以后新增）；
//   - 其余 = 严格名单。
func Allows(names []string, name string) bool {
	if slices.Contains(names, Wildcard) {
		return true
	}
	return slices.Contains(names, name)
}
