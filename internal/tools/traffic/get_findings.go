package traffic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/toolruntime"
	"github.com/V3teran/liusha/internal/finding"
)

// findingsLister 是 GetFindings 依赖的最小读接口，由 *finding.Store 自动满足。
// 局部定义在 consumer 侧（Go idiom: accept interfaces, return structs）+ 让单测可注入 fake。
type findingsLister interface {
	ListByEngagement(ctx context.Context, engagementID string) ([]finding.VulnFinding, error)
}

// GetFindings 主 ReAct 用：查 PG 当前 engagement 已有 findings 摘要。
//
// 主 LLM 在 spawn_skill 多次后用此工具汇总当前已发现的漏洞条目，
// 据此决定还要继续测哪些 / done 收尾。
type GetFindings struct {
	Store        findingsLister
	EngagementID string
}

// Name 返回工具名 "get_findings"。
func (a *GetFindings) Name() string { return "get_findings" }

// Description 给 LLM 的工具描述。
func (a *GetFindings) Description() string {
	return "查当前 engagement 已有的 finding 列表（id/kind/severity/title）。"
}

// ParametersJSON 工具入参 JSON Schema（无入参）。
func (a *GetFindings) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

// findingItem 是 LLM tool message 里的 finding 摘要项；字段尽量瘦，避免吃 token。
type findingItem struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Severity   string `json:"severity"`
	Confidence string `json:"confidence"`
	Title      string `json:"title"`
}

// Execute 列出 engagement 下所有 finding 摘要。
//
// Output 是给 LLM 的结构化 JSON（runtime 直接当 tool message content 喂回），
// Summary 是给 Observer 滑窗 / 日志的人类可读串。两者必须都设——只设 Summary
// 时 LLM 看到 tool message content 缺失，会误判"未发现漏洞"。
func (a *GetFindings) Execute(ctx context.Context, _ json.RawMessage) (toolfx.Result, error) {
	if a.Store == nil || a.EngagementID == "" {
		return toolfx.Result{}, errors.New("GetFindings: store/eid 必填")
	}
	fs, err := a.Store.ListByEngagement(ctx, a.EngagementID)
	if err != nil {
		return toolfx.Result{}, err
	}
	items := make([]findingItem, 0, len(fs))
	for _, f := range fs {
		items = append(items, findingItem{
			ID:         f.ID,
			Kind:       f.Kind,
			Severity:   string(f.Severity),
			Confidence: string(f.Confidence),
			Title:      f.Title,
		})
	}
	output, err := json.Marshal(map[string]any{
		"count":    len(items),
		"findings": items,
	})
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("marshal findings: %w", err)
	}
	summary := fmt.Sprintf("findings=%d:", len(fs))
	for _, f := range fs {
		summary += fmt.Sprintf("\n- id=%s kind=%s severity=%s title=%s",
			f.ID, f.Kind, f.Severity, f.Title)
	}
	return toolfx.Result{Output: output, Summary: summary}, nil
}
