package react

import (
	"github.com/V3teran/liusha/internal/engagement"
)

// 编译期断言：生产路径上各 hook 依赖必须被实现端隐式满足。
// v0024 final agentic：删除 LessonToucher / LessonAdder（distill hook 被 write_lesson 工具替代）。
var (
	_ NotesReader = (*engagement.Store)(nil)
)
