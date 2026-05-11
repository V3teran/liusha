package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/toolruntime"
)

// FindingUpdater 是 UpdateFinding 工具依赖的最小接口。
// *finding.Store 自动满足。
type FindingUpdater interface {
	Update(ctx context.Context, id, summary, severity string, target, evidence json.RawMessage) error
}

// UpdateFinding — 部分更新一条已有 finding（覆盖 summary/severity/evidence/target）。
//
// v0024 agentic：当 read_findings 看到等价但你**有更有价值的新内容**
// （更详细的 PoC / 更精准的描述 / 更高严重度），调本工具覆盖原 finding；
// 完全等价 → done() 跳过；新漏洞 → write_finding 新建。
//
// created_at 保留首次发现时间不变；只覆盖你传的字段（空字段不动）。
type UpdateFinding struct {
	Store FindingUpdater
}

// Name 返回工具名 "update_finding"。
func (a *UpdateFinding) Name() string { return "update_finding" }

// Description 提供给 LLM 的简介。
func (a *UpdateFinding) Description() string {
	return "更新一条已有 finding（保留 created_at 首次发现时间，只覆盖你传的字段）。" +
		"**何时用**：read_findings 看到等价 finding，**但你的新发现更有价值**——" +
		"更详细的 PoC、更精准的 payload、更高的 severity，覆盖之前的版本让记录最优。" +
		"**何时不用**：完全等价 → done() 跳过；新漏洞 → write_finding 新建。" +
		"id 必填；summary/severity/target/evidence 至少传一个（空字段不动，原值保留）。"
}

// ParametersJSON 给出 id 必填 + 4 个可选更新字段 schema。
func (a *UpdateFinding) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "id":{"type":"string","description":"要更新的 finding id（read_findings 拿）"},
    "summary":{"type":"string","description":"覆盖 summary（自由文本；不传则保留原值）"},
    "severity":{"type":"string","description":"覆盖 severity（自由文本；不传则保留原值）"},
    "target":{"type":"object","description":"覆盖 target jsonb（不传则保留原值）"},
    "evidence":{"type":"object","description":"覆盖 evidence jsonb（不传则保留原值）"}
  },
  "required":["id"]
}`)
}

// Execute 解析 → 调 Store.Update。
func (a *UpdateFinding) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	if a.Store == nil {
		return toolfx.Result{}, errors.New("update_finding: Store nil")
	}

	var in struct {
		ID       string          `json:"id"`
		Summary  string          `json:"summary"`
		Severity string          `json:"severity"`
		Target   json.RawMessage `json:"target"`
		Evidence json.RawMessage `json:"evidence"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 update_finding 参数失败: %w", err)
	}
	if in.ID == "" {
		return toolfx.Result{}, errors.New("id 必填")
	}
	// 同 write_finding：unwrap LLM 误传的 JSON-encoded string，保证 jsonb 列存 object 形态。
	in.Target = normalizeJSONObject(in.Target)
	in.Evidence = normalizeJSONObject(in.Evidence)

	if err := a.Store.Update(ctx, in.ID, in.Summary, in.Severity, in.Target, in.Evidence); err != nil {
		return toolfx.Result{}, fmt.Errorf("更新 finding 失败: %w", err)
	}

	return toolfx.Result{Output: json.RawMessage(`{"ok":true}`)}, nil
}
