package einotools

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/V3teran/liusha/internal/lesson"
)

// LessonLister / LessonAdder 是窄接口，*lesson.Store 自动满足。
type LessonLister interface {
	ListByHost(ctx context.Context, host string, limit int) ([]lesson.Lesson, error)
}

type LessonAdder interface {
	Add(ctx context.Context, l lesson.Lesson) (lesson.Lesson, error)
}

const readLessonsLimit = 50

// lessonItem 是给 LLM 看的瘦摘要项。
type lessonItem struct {
	ID        string    `json:"id"`
	Priority  int       `json:"priority"`
	HitCount  int       `json:"hit_count"`
	Kind      string    `json:"kind"`
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BuildReadLessons 造原生 eino read_lessons 工具。host 闭包捕获。
func BuildReadLessons(store LessonLister, host string) (tool.BaseTool, error) {
	return utils.InferTool(
		"read_lessons",
		"列出本 task 目标 host 的全部历史经验（lesson 表，跨 owner 累积）。"+
			"host 来源：passive 是真实 HTTP host；active 是 brief 抽取的 URL host（抽不到时回退 eid，lesson 跨 task 复用失效）。"+
			"\n**何时用**：挖到一半想回看类似经验、想确认某种 payload 是否之前用过、"+
			"或拼新 PoC 前查 host 已知细节。返回按 priority desc + updated_at desc 排序的列表。",
		func(ctx context.Context, _ noArgs) (map[string]any, error) {
			if host == "" {
				return nil, errors.New("read_lessons: Host 注入缺失")
			}
			ls, err := store.ListByHost(ctx, host, readLessonsLimit)
			if err != nil {
				return nil, err
			}
			items := make([]lessonItem, 0, len(ls))
			for _, l := range ls {
				items = append(items, lessonItem{
					ID:        l.ID,
					Priority:  l.Priority,
					HitCount:  l.HitCount,
					Kind:      l.Kind,
					Content:   l.Content,
					UpdatedAt: l.UpdatedAt,
				})
			}
			return map[string]any{"count": len(items), "lessons": items}, nil
		})
}

// writeLessonArgs 是 write_lesson 入参；content 必填，kind/priority 可选。
// 可选字段带 ,omitempty 避免被误标 required（见 findings.go 详注）。
type writeLessonArgs struct {
	Content  string `json:"content"            jsonschema:"required,description=自由文本经验（≤500 字，给下次 AI 看）"`
	Kind     string `json:"kind,omitempty"     jsonschema:"enum=lesson,enum=hint,description=lesson=本 host 特定经验（默认）；hint=跨 host 业务规则（影响所有未来 agent，慎用）"`
	Priority int    `json:"priority,omitempty" jsonschema:"description=优先级 1-10（默认 5；越大越优先注入下次 prompt）"`
}

// BuildWriteLesson 造原生 eino write_lesson 工具。host 闭包捕获；kind=hint 时 host 强制为全局。
func BuildWriteLesson(store LessonAdder, host string) (tool.BaseTool, error) {
	return utils.InferTool(
		"write_lesson",
		"写一条「跨 task 长期经验」到 lesson 库"+
			"（按 host 永久累积，下次扫同一 host 自动注入 user prompt；同 content_hash 自动 dedup）。"+
			"\n\nhost 维度：passive 模式是真实 HTTP host（如 target.com:8080），跨 task 复用度高；"+
			"active 模式是 brief 里抽取的 URL host，抽不到时回退 task_id 兜底（此情况 lesson 跨 task 复用失效）。"+
			"\n\n【必写】下次扫描同 host / 同类目标能复用的知识："+
			"\n- 目标默认/常用凭据（如『此 host 默认 admin:password』）"+
			"\n- 工具调用 pattern（如『DVWA login.php 必须先 GET 拿 user_token 再 POST』）"+
			"\n- 系统级稳定怪癖的通用解（不变的目标特性）"+
			"\n- kind=hint：跨 host 业务规则（如『价格篡改 ≥10% 才算 finding』）。⚠️ hint 影响所有未来 agent，只在强证据时写。"+
			"\n\n【禁写】请改用对应工具："+
			"\n- 本次具体漏洞细节（漏洞 PoC）→ write_finding"+
			"\n- 一次性事实（本次 session、临时 cookie、当前状态）→ 直接在对话里说出（reasoning），不进长期库"+
			"\n- 通用 OWASP 理论 / LLM 已知知识（浪费长期存储）",
		func(ctx context.Context, in writeLessonArgs) (map[string]any, error) {
			if host == "" {
				return nil, errors.New("write_lesson: Host 注入缺失")
			}
			if in.Content == "" {
				return nil, errors.New("content 必填")
			}
			// kind 决定写入语义：lesson→保留注入 host；hint→强制全局 host。
			kind := lesson.KindLesson
			lhost := host
			switch in.Kind {
			case "", lesson.KindLesson:
			case lesson.KindHint:
				kind = lesson.KindHint
				lhost = lesson.HostGlobalHint
			default:
				return nil, fmt.Errorf("kind 取值非法 %q（仅支持 lesson | hint）", in.Kind)
			}
			saved, err := store.Add(ctx, lesson.Lesson{
				Host:     lhost,
				Kind:     kind,
				Content:  in.Content,
				Priority: in.Priority,
			})
			if err != nil {
				return nil, fmt.Errorf("保存 lesson 失败: %w", err)
			}
			return map[string]any{"id": saved.ID}, nil
		})
}
