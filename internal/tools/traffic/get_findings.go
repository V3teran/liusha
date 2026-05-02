package traffic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/toolfx"
)

// GetFindings 主 ReAct 用：查 PG 当前 engagement 已有 findings 摘要。
//
// 主 LLM 在 spawn_skill 多次后用此工具汇总当前已发现的漏洞条目，
// 据此决定还要继续测哪些 / done 收尾。
type GetFindings struct {
	Store        *finding.Store
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

// Execute 列出 engagement 下所有 finding 摘要。
func (a *GetFindings) Execute(ctx context.Context, _ json.RawMessage) (toolfx.Result, error) {
	if a.Store == nil || a.EngagementID == "" {
		return toolfx.Result{}, errors.New("GetFindings: store/eid 必填")
	}
	fs, err := a.Store.ListByEngagement(ctx, a.EngagementID)
	if err != nil {
		return toolfx.Result{}, err
	}
	summary := fmt.Sprintf("findings=%d:", len(fs))
	for _, f := range fs {
		summary += fmt.Sprintf("\n- id=%s kind=%s severity=%s title=%s",
			f.ID, f.Kind, f.Severity, f.Title)
	}
	return toolfx.Result{Summary: summary}, nil
}
