package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/lesson"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// LessonAdder 是 WriteLesson 工具依赖的最小接口。
// *lesson.Store 自动满足。
type LessonAdder interface {
	Add(ctx context.Context, l lesson.Lesson) (lesson.Lesson, error)
}

// WriteLesson — 写一条长期经验到 lesson 表（kind=lesson，跨 owner 持久化）。
//
// hunter agent 写 finding 后自决何时调本工具沉淀经验（"这条值得下次复用吗"）。
// host 由 builder 注入；LLM 只填 content（必填）+ kind/priority/payload（可选）。
// (host, content_hash) 唯一键 ON CONFLICT 幂等：同 content 重复写只 hit_count++。
type WriteLesson struct {
	Store  LessonAdder
	Host   string // builder 注入（per-task host）
}

// Name 返回工具名 "write_lesson"。
func (a *WriteLesson) Name() string { return "write_lesson" }

func (a *WriteLesson) Description() string {
	return "写一条「跨 owner 长期经验」到 lesson 库" +
		"（按 host 永久累积，下次扫同一 host 自动注入 user prompt；同 content_hash 自动 dedup）。" +
		"\n\nhost 维度：passive 模式是真实 HTTP host（如 target.com:8080），跨 task 复用度高；" +
		"active 模式是 brief 里抽取的 URL host，抽不到时回退 owner_id 兜底（此情况 lesson 跨 task 复用失效，建议优先用 note）。" +
		"\n\n【必写】下次扫描同 host / 同类目标能复用的知识：" +
		"\n- 目标默认/常用凭据（如『此 host 默认 admin:password』）" +
		"\n- 工具调用 pattern（如『DVWA login.php 必须先 GET 拿 user_token 再 POST』）" +
		"\n- 系统级稳定怪癖的通用解（不变的目标特性，如『/api/x 用 id 参数注入；UNION 列数=2；DBMS=MariaDB』）" +
		"\n- kind=hint：跨 host 业务规则（如『价格篡改 ≥10% 才算 finding』）。⚠️ hint 影响所有未来 agent，只在强证据时写。" +
		"\n\n【禁写】请改用对应工具：" +
		"\n- 本次具体漏洞细节（漏洞 PoC）→ write_finding" +
		"\n- 一次性事实（本次 session、临时 cookie、当前状态）→ write_note" +
		"\n- 通用 OWASP 理论 / LLM 已知知识（浪费长期存储）" +
		"\ncontent 必填（≤500 字）；priority 1-10 默认 5。"
}

// ParametersJSON 给出 content 必填 + kind/priority 可选 schema。
func (a *WriteLesson) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "content":{"type":"string","description":"自由文本经验（≤500 字，给下次 AI 看）"},
    "kind":{"type":"string","enum":["lesson","hint"],"default":"lesson","description":"lesson=本 host 特定经验（默认）；hint=跨 host 业务规则（影响所有未来 agent，慎用）"},
    "priority":{"type":"integer","minimum":1,"maximum":10,"description":"优先级 1-10（默认 5；越大越优先注入下次 prompt）"}
  },
  "required":["content"]
}`)
}

// Execute 解析参数 → 调 Store.Add → 返回 lesson_id。
func (a *WriteLesson) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	if a.Store == nil {
		return toolfx.Result{}, errors.New("write_lesson: Store nil")
	}
	if a.Host == "" {
		return toolfx.Result{}, errors.New("write_lesson: Host 必填（builder 注入失败）")
	}

	var in struct {
		Content  string `json:"content"`
		Kind     string `json:"kind"`
		Priority int    `json:"priority"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 write_lesson 参数失败: %w", err)
	}
	if in.Content == "" {
		return toolfx.Result{}, errors.New("content 必填")
	}

	// kind 决定写入语义：
	//   - "lesson"（缺省）→ 本 host 经验，host 保留 builder 注入值。
	//   - "hint"          → 跨 host 业务规则，host 强制覆盖为 HostGlobalHint("*")。
	kind := lesson.KindLesson
	host := a.Host
	switch in.Kind {
	case "", lesson.KindLesson:
		// 默认分支：lesson。
	case lesson.KindHint:
		kind = lesson.KindHint
		host = lesson.HostGlobalHint
	default:
		return toolfx.Result{}, fmt.Errorf("kind 取值非法 %q（仅支持 lesson | hint）", in.Kind)
	}

	saved, err := a.Store.Add(ctx, lesson.Lesson{
		Host:     host,
		Kind:     kind,
		Content:  in.Content,
		Priority: in.Priority,
	})
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("保存 lesson 失败: %w", err)
	}

	out, _ := json.Marshal(map[string]string{"id": saved.ID})
	return toolfx.Result{Output: out}, nil
}
