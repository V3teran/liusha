// Package lesson 是 lesson 表的 Go 模型与持久化层。
//
// 跨 engagement 长期知识库：lesson_extract 蒸馏 finding 时单写本表，
// 子 ReAct 装配时按 (tenant, host) 加载 top-N 当背景知识；finding 重发现时
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
)

// Lesson 是 lesson 表行的 Go 表示。
//
// SourceEngagementID / SourceFindingID 用 *string：FK ON DELETE SET NULL；
// 旧 engagement 被删后 lesson 仍保留（知识不应随 engagement 销毁）。
//
// Content：自由文本经验（中文，给下次 AI 看）。
// Payload：结构化字段 jsonb（method/url_template/payload_string/headers/notes），
// 便于程序化消费（聚类/统计/重放）。空 jsonb '{}' 时 lesson 仍可用。
//
// Kind（v0022 加）：lesson | hint。caller 必填（Add 路径校验非空）。
type Lesson struct {
	ID                 string
	TenantID           string
	Host               string
	Kind               string // v0022：lesson | hint
	Content            string
	ContentHash        string // SHA-256 hex（64 字符）
	Priority           int    // 1-10，越大越优先
	SourceEngagementID *string
	SourceFindingID    *string
	HitCount           int
	Payload            []byte // jsonb raw；调用方 json.Marshal 后传入
	CreatedAt          time.Time
	UpdatedAt          time.Time
}
