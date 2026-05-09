package common

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// FindingStore 是 WriteFinding 工具依赖的最小接口。
type FindingStore interface {
	Save(ctx context.Context, f finding.VulnFinding) (finding.VulnFinding, error)
}

// WriteFinding — 写一条漏洞 finding（append-only）。
//
// v0024 agentic-lean：summary 自由文本是漏洞主体，severity 自由文本，evidence 可选。
// 不再有 kind / confidence / dedup_key 字段——dedup 由 LLM 自决（写前调 findings() 看已有的）。
//
// Host 由调用方注入（hunter builder 从 BuilderParams.Host），不让 LLM 自填避免拼错。
type WriteFinding struct {
	Store        FindingStore
	EngagementID string
	TaskID       string // 可空
	Host         string // builder 注入；空时 Save 报错
	FlowID       int64  // 触发本次 hunter 的 http_flow.id；0 表示不关联
}

// Name 返回动作名 "finding"。
func (a *WriteFinding) Name() string { return "write_finding" }

// Description 提供给 LLM 的简介。
func (a *WriteFinding) Description() string {
	return "写一条漏洞 finding。**summary 是核心**：自由文本描述发现是什么、怎么验证、推理依据。" +
		"severity 自由文本（建议 critical/high/medium/low/info 保持配色一致；其他值 UI 退化为蓝色）。" +
		"evidence 选填（复杂证据走 jsonb，简单的写在 summary 里）。" +
		"**写之前先 findings() 查 host 已有的**——同一漏洞别重复写。"
}

// ParametersJSON 给出 finding 字段 schema（v0024 lean）。
func (a *WriteFinding) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties": {
    "summary":{"type":"string","description":"自由文本描述漏洞核心：是什么 / 怎么验证 / 推理依据。第一行（≤72 chars）会被 UI/Label 当短标题用（git commit convention）。"},
    "severity":{"type":"string","description":"自由文本（建议 critical/high/medium/low/info 保持前端配色一致）"},
    "target":{"type":"object","description":"目标元数据 jsonb（如 {host,method,path}），UI 显示用"},
    "evidence":{"type":"object","description":"可选：复杂结构化证据 jsonb；简单证据写在 summary 即可"}
  },
  "required":["summary"]
}`)
}

// Execute 解析参数 → 构造 finding.VulnFinding → Store.Save → 返回 {id}。
func (a *WriteFinding) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		Summary  string          `json:"summary"`
		Severity string          `json:"severity"`
		Target   json.RawMessage `json:"target"`
		Evidence json.RawMessage `json:"evidence"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 finding 参数失败: %w", err)
	}
	if in.Summary == "" {
		return toolfx.Result{}, fmt.Errorf("summary 必填（自由文本描述漏洞核心）")
	}

	var taskPtr *string
	if a.TaskID != "" {
		t := a.TaskID
		taskPtr = &t
	}
	var flowPtr *int64
	if a.FlowID != 0 {
		fid := a.FlowID
		flowPtr = &fid
	}

	saved, err := a.Store.Save(ctx, finding.VulnFinding{
		EngagementID: a.EngagementID,
		TaskID:       taskPtr,
		SourceFlowID: flowPtr,
		Host:         a.Host,
		Severity:     in.Severity,
		Summary:      in.Summary,
		Target:       in.Target,
		Evidence:     in.Evidence,
	})
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("保存 finding 失败: %w", err)
	}

	out, _ := json.Marshal(map[string]string{"id": saved.ID})
	return toolfx.Result{Output: out}, nil
}
