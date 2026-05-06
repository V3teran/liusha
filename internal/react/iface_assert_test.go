package react

import (
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/lesson"
)

// 编译期断言：生产路径上各 hook 依赖必须被实现端隐式满足。
var (
	_ StateReader   = (*engagement.Store)(nil)
	_ LessonAdder   = (*lesson.Store)(nil)
	_ LessonToucher = (*lesson.Store)(nil)
)
