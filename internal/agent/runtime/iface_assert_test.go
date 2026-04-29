package runtime

import (
	"github.com/V3teran/liusha/internal/engagement"
)

// 编译期断言：engagement.Store 必须隐式满足 StateReader / StateAppender 两个接口，
// 这是 LLMObserver / DistillHook 在生产路径正常工作的前提。
var (
	_ StateReader   = (*engagement.Store)(nil)
	_ StateAppender = (*engagement.Store)(nil)
)
