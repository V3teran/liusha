package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/lesson"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// lessonsLister 是 ReadLessons 工具依赖的最小读接口，由 *lesson.Store 自动满足。
type lessonsLister interface {
	ListByHost(ctx context.Context, host string, limit int) ([]lesson.Lesson, error)
}

// ReadLessons — 列出本 task 目标 host 的全部历史经验（lesson 表，跨 owner 持久化）。
//
// 启动时已注入一次到 user prompt（包含跨 host 业务规则 hint host='*'）；agent 想再回看时调本工具。
// 本工具只拉 host-scoped lesson（kind=lesson 的 distill 经验 + host-scoped hint）。
type ReadLessons struct {
	Store  lessonsLister
	Host   string // builder 注入；空时 Execute 报错
}

// Name 返回工具名 "read_lessons"。
func (a *ReadLessons) Name() string { return "read_lessons" }

func (a *ReadLessons) Description() string {
	return "列出本 task 目标 host 的全部历史经验（lesson 表，跨 owner 累积）。" +
		"host 来源：passive 是真实 HTTP host；active 是 brief 抽取的 URL host（抽不到时回退 eid，lesson 跨 task 复用失效）。" +
		"\n**何时用**：挖到一半想回看类似经验、想确认某种 payload 是否之前用过、" +
		"或拼新 PoC 前查 host 已知细节。返回按 priority desc + updated_at desc 排序的列表。"
}

// ParametersJSON 无入参（host 由 builder 注入）。
func (a *ReadLessons) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

// lessonItem 是给 LLM 看的瘦摘要项。
type lessonItem struct {
	ID        string    `json:"id"`
	Priority  int       `json:"priority"`
	HitCount  int       `json:"hit_count"`
	Kind      string    `json:"kind"` // lesson | hint
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Execute 列出 host 全部 lesson 摘要。
func (a *ReadLessons) Execute(ctx context.Context, _ json.RawMessage) (toolfx.Result, error) {
	if a.Store == nil {
		return toolfx.Result{}, errors.New("read_lessons: Store nil")
	}
	if a.Host == "" {
		return toolfx.Result{}, errors.New("read_lessons: Host 必填（builder 注入失败）")
	}

	ls, err := a.Store.ListByHost(ctx, a.Host, 50)
	if err != nil {
		return toolfx.Result{}, err
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
	output, err := json.Marshal(map[string]any{
		"count":   len(items),
		"lessons": items,
	})
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("marshal lessons: %w", err)
	}
	return toolfx.Result{Output: output}, nil
}
