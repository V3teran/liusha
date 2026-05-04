// Package lesson 是 host_lesson 表的 Go 模型与持久化层。
//
// 跨 engagement 长期目标知识库（v1.2）：lesson_extract 蒸馏 finding 时单写本表，
// 子 ReAct 装配时按 (tenant, host) 加载 top-N 当背景知识；finding 重发现时
// hit_count++ 体现可信度。
//
// v1.2 收尾：原 engagement.memory_hints 层已删——lesson_extract 不再双写，唯一长期
// 经验层就是 host_lesson；列保留在 engagement 表 schema 但不读不写。
package lesson

import (
	"time"
)

// Lesson 是 host_lesson 表行的 Go 表示。
//
// SourceEngagementID / SourceFindingID 用 *string：FK ON DELETE SET NULL；
// 旧 engagement 被删后 lesson 仍保留（知识不应随 engagement 销毁）。
//
// Content：自由文本经验（中文，给下次 AI 看）。
// Payload：结构化字段 jsonb（method/url_template/payload_string/headers/notes），
// 便于程序化消费（聚类/统计/重放）。空 jsonb '{}' 时 lesson 仍可用。
type Lesson struct {
	ID                 string
	TenantID           string
	Host               string
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
