// Package lesson 是 lesson 表的 Go 模型与持久化层。
//
// 跨 engagement 长期知识库：LLM 主动 write_lesson 写本表，
// hunter 装配时按 host 加载 top-N 当背景知识；重复 content 时
// hit_count++ 体现可信度。
package lesson

import (
	"time"
)

// 包级常量：Lesson.Kind 的合法取值（v0022 加，与 lesson 表 kind 列 CHECK 约束一致）。
//   - KindLesson：distill 自动蒸馏的"目标级长期经验"（per host），与原行为一致
//   - KindHint  ：业务规则提醒（host 可为 HostGlobalHint="*" 表示对所有 host 通用）
const (
	KindLesson = "lesson"
	KindHint   = "hint"

	// HostGlobalHint 是 hint 的特殊 host 值——表示对所有 host 通用的业务规则。
	// 与具体 host 一起被 ListByHostWithGlobalHints 拉取。
	HostGlobalHint = "*"
)

// Lesson 是 lesson 表行的 Go 表示。
//
// Content：自由文本经验（中文，给下次 AI 看）。
// Kind（v0022 加）：lesson | hint。caller 必填（Add 路径校验非空）。
type Lesson struct {
	ID          string
	Host        string
	Kind        string // v0022：lesson | hint
	Content     string
	ContentHash string // SHA-256 hex（64 字符）
	Priority    int    // 1-10，越大越优先
	HitCount    int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
