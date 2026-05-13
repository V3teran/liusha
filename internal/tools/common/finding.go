package common

import (
	"bytes"
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
// summary 是漏洞主体（自由文本），severity 自由文本，evidence 可选；
// dedup 由 LLM 自决（写前调 read_findings 看已有的）。
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
//
// 设计分工：
//   - description（本函数）：格式约束（summary 单行 ≤500 / evidence jsonb / severity 取值）+ 一行质量红线提示
//   - hunter system_prompt"## 写 finding 必须满足"：4 条详细 behavioral rules（真实命中 / 工具未失败 / 可复现 / 不重复）
// LLM 选工具时看 description 的格式细节，调用前已被 system_prompt 全局规则约束，互不重复。
func (a *WriteFinding) Description() string {
	return "写一条**新**漏洞 finding。" +
		"**格式**：summary 一行短标题（≤500 chars 无换行）；详情/复现/payload 全进 evidence jsonb；" +
		"severity 建议 critical/high/medium/low/info（其他值 UI 退化为蓝色）。" +
		"**质量**：evidence 必须含可复现的 repro_cmd；工具失败/超时不许伪造——详见 system prompt 的 4 条红线。"
}

// ParametersJSON 给出 finding 字段 schema。
func (a *WriteFinding) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties": {
    "summary":{"type":"string","maxLength":500,"description":"**一行**短标题（≤500 chars，无换行；git commit subject 风格）：'<漏洞类型> in <path> — <核心机理>'。详情/复现/payload 全进 evidence，**禁止**复制到 summary。DB 有 check 约束（单行 + ≤500），违反会拒收。"},
    "severity":{"type":"string","description":"自由文本（建议 critical/high/medium/low/info 保持前端配色一致）"},
    "target":{"type":"object","description":"目标元数据 jsonb（如 {host,method,path,parameter}），UI 显示用。**直接传 JSON object，不要再 string-encode 一层**（错例：\"{\\\"host\\\":\\\"...\\\"}\"；正确：{\"host\":\"...\"}）。"},
    "evidence":{"type":"object","description":"结构化证据 jsonb：放完整工具输出片段（sqlmap Parameter:/Type:/Payload:、nuclei matcher、curl 响应等）+ repro_cmd + dump 数据。**summary 之外的所有内容都进这里。** **直接传 JSON object，不要再 string-encode 一层**（错例：\"{\\\"vulnerability_type\\\":\\\"...\\\"}\"；正确：{\"vulnerability_type\":\"...\"}）。"}
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
	// LLM 偶尔会把 target/evidence object 误 string-encode 一层（schema 说 object 但传成 "{...}"），
	// 导致 jsonb 列存成 string 而非 object。统一 unwrap 回 object 形态。
	in.Target = normalizeJSONObject(in.Target)
	in.Evidence = normalizeJSONObject(in.Evidence)

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

// normalizeJSONObject 把 LLM 误传的"JSON-encoded string"形态 unwrap 回 object。
//
// 触发场景：schema 标 type=object，但 LLM 偶尔会传 "{\"foo\":1}" 这种"字符串包
// JSON"——`json.RawMessage` 原样存到 jsonb 列，导致 typeof = string 而非 object，
// UI 渲染要双重解析、查询 path 失效。
//
// 处理规则：
//   - 空输入原样返回（保留 jsonb null 语义）
//   - 不是 JSON string 形态（首字符非 "）→ 原样返回（已经是 object/array/scalar）
//   - 是 JSON string 但 unwrap 后不是 object/array → 原样返回（保留原始字符串语义）
//   - 是 JSON string 且 unwrap 后是合法 JSON object/array → 返回 unwrap 后的字节
func normalizeJSONObject(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return raw
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return raw
	}
	inner := bytes.TrimSpace([]byte(s))
	if len(inner) == 0 {
		return raw
	}
	if inner[0] != '{' && inner[0] != '[' {
		return raw
	}
	if !json.Valid(inner) {
		return raw
	}
	return json.RawMessage(s)
}
