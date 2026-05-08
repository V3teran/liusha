package react

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/lesson"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/logx"
)

// lessonExtractLog 包级 zerolog logger（与其他埋点统一走 logs/scanner.log；之前用 stdlib slog 默认输出到 stderr）。
var lessonExtractLog zerolog.Logger = logx.New("react.lesson_extract")

// effectiveLesson 把 zero 字段 fallback 到对应 fallback 常量。
func effectiveLesson(c config.LessonConfig) (priority, maxRetries int, initBackoff, maxBackoff time.Duration) {
	priority = c.ExtractedPriority
	if priority <= 0 {
		priority = fallbackExtractedLessonPriority
	}
	maxRetries = c.TouchMaxRetries
	if maxRetries <= 0 {
		maxRetries = fallbackLessonTouchMaxRetries
	}
	initBackoff = time.Duration(c.TouchInitialBackoffMs) * time.Millisecond
	if initBackoff <= 0 {
		initBackoff = fallbackLessonTouchInitialBackoff
	}
	maxBackoff = time.Duration(c.TouchMaxBackoffMs) * time.Millisecond
	if maxBackoff <= 0 {
		maxBackoff = fallbackLessonTouchMaxBackoff
	}
	return
}

// LessonAdder 是 LessonExtractHook 写 lesson 的最小依赖。
// *lesson.Store 隐式满足该接口；nil 时 extractLesson 直接 return（无副作用）。
type LessonAdder interface {
	Add(ctx context.Context, l lesson.Lesson) (lesson.Lesson, error)
}

// LessonToucher 是 ReSavedTouchHook 给老 lesson hit_count++ 用。
// 由 *lesson.Store 自动满足。
//
// 用 (host, dedup_key) 而不是 finding.ID：因为 finding 是 append-only，重发现的
// 新行 ID 与 lesson.source_finding_id（指首发 ID）不匹配；按 (host, dedup_key)
// 反查首发 ID 才能正确命中老 lesson。
//
// 返回 rowsAffected：0 表示首发对应的 lesson 还没建好（extract 异步未完成），
// hook 凭此判断是否需要 backoff 重试。
type LessonToucher interface {
	TouchByDedup(ctx context.Context, host, dedupKey string) (int64, error)
}

// fallbackExtractedLessonPriority 在 cfg.Lesson.ExtractedPriority ≤ 0 时使用。
const fallbackExtractedLessonPriority = 7

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

// NewLessonExtractHook 把 finding.Save 首次发现事件转成 lesson 经验。
//
// 设计要点：
//   - 同步执行；finding.Store.fireSavedHooks 已 go func() 起单独 goroutine，外层不会阻塞；
//   - LLM 失败 / 空内容 / lesson.Add 失败 → 仅 log Warn，不 panic（finding 已经持久化）；
//   - 仅在 finding 首次 INSERT 时被调用；
//   - 重发现 finding 时不提取（避免反复浪费 LLM）；hit_count++ 由 NewLessonTouchHook 处理。
func NewLessonExtractHook(g llm.Generator, lessons LessonAdder, cfg config.LessonConfig) finding.SavedHook {
	priority, _, _, _ := effectiveLesson(cfg)
	timeoutSec := cfg.ExtractTimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = fallbackLessonExtractTimeoutSec
	}
	return func(ctx context.Context, eid string, f finding.VulnFinding) {
		// hook 由 finding.Store.fireSavedHooks 用 context.Background() 触发，无业务 deadline；
		// 在此加业务级超时防 LLM 永久挂起把 goroutine 漏掉。
		ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()
		extractLesson(ctx, g, lessons, eid, f, priority)
	}
}

// NewLessonTouchHook 在 finding 重发现（UPDATE 路径）时，给对应 lessons 累加 hit_count。
//
// 每次重扫确认漏洞依然存在时，所有 source_finding_id 指向该 finding 的 lesson 行
// hit_count+1，体现"经验被多次验证"，便于按可信度排序。
//
// **时序竞争修复**：首发 LessonExtract 是异步 goroutine（~2-10s LLM，含 prompt cache miss
// 与 retry），lesson 可能在重发现 hook 触发时还没建好 → 0 update。
// 用几何 backoff（500ms 起，每次 × 2，cap 在 4s）×7 次重试，总等待约 19.5s——
// 前期快速重试避免无谓等待，后期长等待覆盖慢 LLM。仍失败仅 log Warn。
//
// 旧策略 4 × 800ms = 3.2s 在 e2e 实测下偶发 give up（LLM 调用 > 3s 时），新策略覆盖 99% 场景。
const (
	fallbackLessonTouchMaxRetries     = 7
	fallbackLessonTouchInitialBackoff = 500 * time.Millisecond
	fallbackLessonTouchMaxBackoff     = 4 * time.Second
	// fallbackLessonExtractTimeoutSec：cfg.LessonConfig.ExtractTimeoutSeconds 为 0 时兜底
	// （NewLessonExtractHook 把它包成 ctx WithTimeout，防 LLM 调用永久挂起）。
	fallbackLessonExtractTimeoutSec = 60
)

func NewLessonTouchHook(toucher LessonToucher, cfg config.LessonConfig) finding.SavedHook {
	_, maxRetries, initBackoff, maxBackoff := effectiveLesson(cfg)
	return func(ctx context.Context, _ string, f finding.VulnFinding) {
		if toucher == nil || f.Host == "" || f.DedupKey == "" {
			return
		}
		backoff := initBackoff
		for attempt := 0; attempt < maxRetries; attempt++ {
			n, err := toucher.TouchByDedup(ctx, f.Host, f.DedupKey)
			if err != nil {
				lessonExtractLog.Warn().Err(err).Str("host", f.Host).Str("dedup_key", f.DedupKey).Msg("lesson touch failed")
				return
			}
			if n > 0 {
				return
			}
			// 0 update：lesson 可能还在 extract；sleep 后重试
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
		lessonExtractLog.Warn().Str("host", f.Host).Str("dedup_key", f.DedupKey).Msg("lesson touch gave up: lesson never appeared")
	}
}

// extractLesson 是单次经验提取的可测函数主体（LLM 把 finding 压缩成 lesson）。
//
// LLM 输出 JSON {content, payload}：content 落 lesson.content（自由文本），
// payload 落 lesson.payload（结构化）。LLM 输出非合法 JSON 时降级：
// 把整段当 content 用，payload 留空 '{}'。
func extractLesson(ctx context.Context, g llm.Generator, lessons LessonAdder, eid string, f finding.VulnFinding, priority int) {
	if lessons == nil {
		// 无 lesson 后端时 extract 完全没意义；直接跳过省 LLM 调用
		return
	}
	if f.Host == "" {
		lessonExtractLog.Warn().Str("finding_id", f.ID).Msg("lesson_extract skip: finding.Host empty")
		return
	}

	start := time.Now()
	lessonExtractLog.Info().
		Str("finding_id", f.ID).
		Str("kind", f.Kind).
		Str("host", f.Host).
		Str("engagement_id", eid).
		Msg("lesson_extract ▶ enter")

	user := buildExtractPrompt(f)
	res, err := g.Generate(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: extractSystemPrompt},
		{Role: llm.RoleUser, Content: user},
	}, nil)
	if err != nil {
		lessonExtractLog.Warn().Err(err).
			Str("engagement_id", eid).Str("finding_id", f.ID).
			Dur("duration", time.Since(start)).
			Msg("lesson_extract llm call failed")
		return
	}

	raw := strings.TrimSpace(res.Content)
	if raw == "" {
		lessonExtractLog.Warn().
			Str("engagement_id", eid).Str("finding_id", f.ID).
			Dur("duration", time.Since(start)).
			Msg("lesson_extract llm returned empty content")
		return
	}

	content, payload := parseExtractOutput(raw)
	if content == "" {
		lessonExtractLog.Warn().
			Str("engagement_id", eid).Str("finding_id", f.ID).
			Str("raw_head", truncate(raw, 100)).
			Dur("duration", time.Since(start)).
			Msg("lesson_extract parsed content empty")
		return
	}

	eidCopy := eid
	fid := f.ID
	added, err := lessons.Add(ctx, lesson.Lesson{
		Host:               f.Host,
		Content:            content,
		Payload:            payload,
		Priority:           priority,
		SourceEngagementID: &eidCopy,
		SourceFindingID:    &fid,
	})
	if err != nil {
		lessonExtractLog.Warn().Err(err).
			Str("host", f.Host).Str("engagement_id", eid).Str("finding_id", f.ID).
			Dur("duration", time.Since(start)).
			Msg("lesson_extract add lesson failed")
		return
	}
	lessonExtractLog.Info().
		Str("finding_id", f.ID).
		Str("lesson_id", added.ID).
		Str("host", f.Host).
		Int("priority", priority).
		Int("content_bytes", len(content)).
		Dur("duration", time.Since(start)).
		Msg("lesson_extract ◀ exit")
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
func buildExtractPrompt(f finding.VulnFinding) string {
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
