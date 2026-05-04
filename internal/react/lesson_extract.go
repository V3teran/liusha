package react

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/vulnfinding"
	"github.com/V3teran/liusha/internal/lesson"
	"github.com/V3teran/liusha/internal/llm"
)

// LessonAdder 是 LessonExtractHook 写 host_lesson 的最小依赖。
// *lesson.Store 隐式满足该接口；nil 时 extractLesson 退化为仅 LLM 调用 + slog（无副作用）。
type LessonAdder interface {
	Add(ctx context.Context, l lesson.Lesson) (lesson.Lesson, error)
}

// LessonToucher 是 ReSavedTouchHook 给老 lesson hit_count++ 用。
// 由 *lesson.Store 自动满足。
//
// 用 (host, dedup_key) 而不是 finding.ID：因为 finding 是 append-only，重发现的
// 新行 ID 与 host_lesson.source_finding_id（指首发 ID）不匹配；按 (host, dedup_key)
// 反查首发 ID 才能正确命中老 lesson。
//
// 返回 rowsAffected：0 表示首发对应的 lesson 还没建好（extract 异步未完成），
// hook 凭此判断是否需要 backoff 重试。
type LessonToucher interface {
	TouchByDedup(ctx context.Context, host, dedupKey string) (int64, error)
}

// extractedLessonPriority 是 LessonExtract 写入 host_lesson 时的固定优先级（中-高）。
const extractedLessonPriority = 7

// extractSystemPrompt 让 light_provider 把单条 finding 浓缩成可复用的目标级经验。
//
// 输出 JSON 形式 {content: "...", payload: {...}}：
// - content 是给下次 AI 看的中文自由文本经验
// - payload 是结构化字段（程序化消费：聚类/统计/重放）
const extractSystemPrompt = `你把一条新发现（finding）浓缩成可复用的目标级经验，输出 JSON 对象（仅 JSON，无任何前后缀）：
{
  "content": "中文经验文本，≤ 500 字。包含 endpoint / 方法 / 触发条件 / 具体 payload / 绕过技巧 / 下次建议。可分多段，单段不超过 200 字。",
  "payload": {
    "method": "GET|POST|...",
    "url_template": "/api/bac/order/:id",
    "payload_string": "?id=2 或 body 关键字段示例",
    "headers": {"X-User":"test"},
    "notes": "可选，技巧/边界条件备注"
  }
}
要求：
- 必须输出合法 JSON，且 content 与 payload 同时给出；
- payload 字段是结构化复用要点；不知道某字段则缺省（不要瞎填）；
- content 是经验全文，必须包含具体 payload 和触发条件（让下次 AI 直接照做）；
- 严禁任何 JSON 包装外的前缀、后缀、Markdown 代码围栏。`

// NewLessonExtractHook 把 finding.Save 首次发现事件转成 host_lesson 经验。
//
// 设计要点：
//   - 同步执行；vulnfinding.Store.fireSavedHooks 已 go func() 起单独 goroutine，外层不会阻塞；
//   - LLM 失败 / 空内容 / lesson.Add 失败 → 仅 slog.Warn，不 panic（finding 已经持久化）；
//   - 仅在 finding 首次 INSERT 时被调用；
//   - 重发现 finding 时不提取（避免反复浪费 LLM）；hit_count++ 由 NewLessonTouchHook 处理。
func NewLessonExtractHook(g llm.Generator, lessons LessonAdder) vulnfinding.SavedHook {
	return func(ctx context.Context, eid string, f vulnfinding.VulnFinding) {
		extractLesson(ctx, g, lessons, eid, f)
	}
}

// NewLessonTouchHook 在 finding 重发现（UPDATE 路径）时，给对应 lessons 累加 hit_count。
//
// 每次重扫确认漏洞依然存在时，所有 source_finding_id 指向该 finding 的 lesson 行
// hit_count+1，体现"经验被多次验证"，便于按可信度排序。
//
// **时序竞争修复**：首发 LessonExtract 是异步 goroutine（~2s LLM），lesson 可能在重发现
// hook 触发时还没建好 → 0 update。这里加 backoff 重试：每 800ms × 4 次（最多 3.2s）
// 等 extract 完成。仍失败仅 slog.Warn（hit_count 丢一次不影响业务）。
const lessonTouchMaxRetries = 4
const lessonTouchBackoff = 800 * time.Millisecond

func NewLessonTouchHook(toucher LessonToucher) vulnfinding.SavedHook {
	return func(ctx context.Context, _ string, f vulnfinding.VulnFinding) {
		if toucher == nil || f.Host == "" || f.DedupKey == "" {
			return
		}
		for attempt := 0; attempt < lessonTouchMaxRetries; attempt++ {
			n, err := toucher.TouchByDedup(ctx, f.Host, f.DedupKey)
			if err != nil {
				slog.Warn("lesson touch failed", "err", err, "host", f.Host, "dedup_key", f.DedupKey)
				return
			}
			if n > 0 {
				return
			}
			// 0 update：lesson 可能还在 extract；sleep 后重试
			select {
			case <-time.After(lessonTouchBackoff):
			case <-ctx.Done():
				return
			}
		}
		slog.Warn("lesson touch gave up: lesson never appeared", "host", f.Host, "dedup_key", f.DedupKey)
	}
}

// extractLesson 是单次经验提取的可测函数主体（LLM 把 finding 压缩成 lesson）。
//
// LLM 输出 JSON {content, payload}：content 落 host_lesson.content（自由文本），
// payload 落 host_lesson.payload（结构化）。LLM 输出非合法 JSON 时降级：
// 把整段当 content 用，payload 留空 '{}'。
func extractLesson(ctx context.Context, g llm.Generator, lessons LessonAdder, eid string, f vulnfinding.VulnFinding) {
	if lessons == nil {
		// 无 lesson 后端时 extract 完全没意义；直接跳过省 LLM 调用
		return
	}
	if f.Host == "" {
		slog.Warn("lesson_extract skip: finding.Host empty", "finding_id", f.ID)
		return
	}

	user := buildExtractPrompt(f)
	res, err := g.Generate(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: extractSystemPrompt},
		{Role: llm.RoleUser, Content: user},
	}, nil)
	if err != nil {
		slog.Warn("lesson_extract llm call failed", "err", err, "engagement_id", eid, "finding_id", f.ID)
		return
	}

	raw := strings.TrimSpace(res.Content)
	if raw == "" {
		slog.Warn("lesson_extract llm returned empty content", "engagement_id", eid, "finding_id", f.ID)
		return
	}

	content, payload := parseExtractOutput(raw)
	if content == "" {
		slog.Warn("lesson_extract parsed content empty", "engagement_id", eid, "finding_id", f.ID, "raw_head", truncate(raw, 100))
		return
	}

	eidCopy := eid
	fid := f.ID
	if _, err := lessons.Add(ctx, lesson.Lesson{
		Host:               f.Host,
		Content:            content,
		Payload:            payload,
		Priority:           extractedLessonPriority,
		SourceEngagementID: &eidCopy,
		SourceFindingID:    &fid,
	}); err != nil {
		slog.Warn("lesson_extract add host_lesson failed", "err", err, "host", f.Host, "engagement_id", eid, "finding_id", f.ID)
	}
}

// parseExtractOutput 解析 LLM 返回的 JSON {content, payload}。
//
// 降级路径：LLM 偶尔输出非 JSON（裸文本/Markdown 围栏/带前缀）时，
// 把整段当 content 用，payload 留空 '{}'。这样 lesson 仍可写入，只是缺结构化字段。
//
// 容错处理 Markdown 围栏 ```json ... ```：去掉首尾 fence 后再 unmarshal。
func parseExtractOutput(raw string) (content string, payload []byte) {
	stripped := stripCodeFence(raw)
	var parsed struct {
		Content string          `json:"content"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stripped), &parsed); err == nil && strings.TrimSpace(parsed.Content) != "" {
		p := []byte(parsed.Payload)
		if len(p) == 0 {
			p = []byte("{}")
		}
		return strings.TrimSpace(parsed.Content), p
	}
	// 降级：raw 当 content
	return raw, []byte("{}")
}

// stripCodeFence 去掉 ```json ... ``` / ``` ... ``` Markdown 围栏。
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// 去首行（可能是 ``` 或 ```json）
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	// 去末尾 ```
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// buildExtractPrompt 把 finding 的关键字段拼成单条 user 消息。
//
// 保留对"下次扫描决策 + payload 复用"有用的元信息：kind / dedup_key / title / target / evidence。
// 不 truncate evidence 太狠——extract 需要看到具体 evidence 才能写出可复用的 payload。
//
// 用 json.RawMessage 直接序列化以保留原始结构；超长时仍按字节截断防爆 token。
func buildExtractPrompt(f vulnfinding.VulnFinding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "新发现：\nkind=%s\ntitle=%s\ndedup_key=%s\nhost=%s\n",
		f.Kind, f.Title, f.DedupKey, f.Host)
	if len(f.Target) > 0 {
		fmt.Fprintf(&b, "target=%s\n", truncate(string(f.Target), 800))
	}
	if len(f.Evidence) > 0 {
		fmt.Fprintf(&b, "evidence=%s\n", truncate(string(f.Evidence), 1200))
	}
	b.WriteString("\n请按 system 约束输出 ≤ 500 字经验提示（带具体 payload / endpoint / 触发条件）。")
	return b.String()
}
