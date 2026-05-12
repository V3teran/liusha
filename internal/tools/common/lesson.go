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

// WriteLesson — 写一条长期经验到 lesson 表（kind=lesson，跨 engagement 持久化）。
//
// v0024 agentic-lean：替代旧 distill hook 的"系统自动二次 LLM 调用"模式——
// hunter agent 写 finding 后顺手调 write_lesson 把经验沉淀（自决何时值得沉淀），
// 省一次 LLM 调用 + 让 agent 自己判断"这是新颖经验吗"。
//
// host / tenant 由 builder 注入；LLM 只填 content（必填）+ priority（可选 1-10）+ payload（可选 jsonb）。
// (tenant, host, content_hash) 唯一键 ON CONFLICT 幂等：同 content 重复写只 hit_count++。
type WriteLesson struct {
	Store  LessonAdder
	Tenant string // builder 注入（cfg.Engagement.DefaultTenant）
	Host   string // builder 注入（per-task host）
}

// Name 返回工具名 "write_lesson"。
func (a *WriteLesson) Name() string { return "write_lesson" }

// Description 提供给 LLM 的简介。
func (a *WriteLesson) Description() string {
	return "写一条长期经验到 lesson 表（跨 engagement 持久化，下次扫自动注入 user prompt）。" +
		"**kind=lesson（默认）**：本 host 特定经验——下次扫同一 host 时注入。" +
		"  何时用：值得下次复用的 payload / endpoint / 业务模式（如『此 host 的 /api/x 用 id 参数注入；UNION 列数=2；DBMS=MariaDB』）。" +
		"**kind=hint**：跨 host 业务规则——对**所有** host 通用，每条扫描都注入到『跨 host 业务规则提醒（必须遵守）』段。" +
		"  何时用：客户/业务约束（如『价格篡改要 ≥10% 才算 finding』『/admin/* 是已知未授权设计不写 finding』），**不是** OWASP 通用知识。" +
		"  ⚠️ hint 影响所有未来 agent 的判定，误写代价高——只在**强证据**（如 read_lessons 显示历史多次确认）时才写。" +
		"**不要写**：通用 OWASP 理论、本 task 临时状态（用 write_memory）。" +
		"content 必填（≤500 字）；priority 1-10 默认 5。"
}

// ParametersJSON 给出 content 必填 + kind/priority/payload 可选 schema。
func (a *WriteLesson) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "content":{"type":"string","description":"自由文本经验（≤500 字，给下次 AI 看）"},
    "kind":{"type":"string","enum":["lesson","hint"],"default":"lesson","description":"lesson=本 host 特定经验（默认）；hint=跨 host 业务规则（影响所有未来 agent，慎用）"},
    "priority":{"type":"integer","minimum":1,"maximum":10,"description":"优先级 1-10（默认 5；越大越优先注入下次 prompt）"},
    "payload":{"type":"object","description":"可选：结构化字段 jsonb（如 {method, url_template, payload_string, headers}），便于程序化复用"}
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
	if a.Tenant == "" {
		return toolfx.Result{}, errors.New("write_lesson: Tenant 必填（builder 注入失败）")
	}

	var in struct {
		Content  string          `json:"content"`
		Kind     string          `json:"kind"`
		Priority int             `json:"priority"`
		Payload  json.RawMessage `json:"payload"`
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
		TenantID: a.Tenant,
		Host:     host,
		Kind:     kind,
		Content:  in.Content,
		Priority: in.Priority,
		Payload:  in.Payload,
	})
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("保存 lesson 失败: %w", err)
	}

	out, _ := json.Marshal(map[string]string{"id": saved.ID})
	return toolfx.Result{Output: out}, nil
}
