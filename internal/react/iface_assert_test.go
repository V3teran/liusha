package react

import (
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/lesson"
)

// 编译期断言：生产路径上各 hook 的依赖必须被实现端隐式满足。
//
// v1.2 收尾后：StateAppender 已删（lesson_extract 不再写 engagement.memory_hints）；
// lesson_extract 改用 lesson.Store 满足 LessonAdder/LessonToucher。
var (
	_ StateReader   = (*engagement.Store)(nil)
	_ LessonAdder   = (*lesson.Store)(nil)
	_ LessonToucher = (*lesson.Store)(nil)
)
