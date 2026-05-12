package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// findingsLister 是 ReadFindings 工具依赖的最小读接口，由 *finding.Store 自动满足。
// limit ≤ 0 = 不限制；> 0 = SQL LIMIT 限上限。
type findingsLister interface {
	ListByHost(ctx context.Context, host string, limit int) ([]finding.VulnFinding, error)
}

// ReadFindings — 列出本 task 目标 host 的全部已有 finding（dedup 参考）。
//
// v0024 agentic：与 write_finding 配对的读工具，命名风格统一动词前缀。
type ReadFindings struct {
	Store findingsLister
	Host  string // builder 注入；空时 Execute 报错
}

// Name 返回工具名 "read_findings"。
func (a *ReadFindings) Name() string { return "read_findings" }

// Description 提供给 LLM 的简介。
func (a *ReadFindings) Description() string {
	return "列出本 task 目标 host 已有的全部 finding（id/severity/summary/created_at）。" +
		"**写 finding 前必查**——同 host 同一漏洞别重复写。" +
		"返回按 created_at desc 排序的列表。"
}

// ParametersJSON 无入参（host 由 builder 注入）。
func (a *ReadFindings) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

// findingItem 是给 LLM 看的瘦摘要项。
//
// SourceFlowID 让 LLM 区分"同流量内已有 finding"——decide write_finding 新增 /
// update_finding 改进 / done 跳过 三分支。
type findingItem struct {
	ID           string    `json:"id"`
	Severity     string    `json:"severity"`
	Summary      string    `json:"summary"`
	SourceFlowID *int64    `json:"source_flow_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// Execute 列出 host 全部 finding 摘要。
func (a *ReadFindings) Execute(ctx context.Context, _ json.RawMessage) (toolfx.Result, error) {
	if a.Host == "" {
		return toolfx.Result{}, errors.New("findings: Host 必填（builder 注入失败）")
	}
	if a.Store == nil {
		return toolfx.Result{}, errors.New("findings: Store nil")
	}

	fs, err := a.Store.ListByHost(ctx, a.Host, 0)
	if err != nil {
		return toolfx.Result{}, err
	}

	items := make([]findingItem, 0, len(fs))
	for _, f := range fs {
		items = append(items, findingItem{
			ID:           f.ID,
			Severity:     f.Severity,
			Summary:      f.Summary,
			SourceFlowID: f.SourceFlowID,
			CreatedAt:    f.CreatedAt,
		})
	}
	output, err := json.Marshal(map[string]any{
		"count":    len(items),
		"findings": items,
	})
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("marshal findings: %w", err)
	}
	return toolfx.Result{Output: output}, nil
}
