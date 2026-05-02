package common

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/toolfx"
	"github.com/V3teran/liusha/internal/finding"
)

// FindingStore 是 WriteFinding 依赖的最小接口。
//
// 由 *finding.Store 自动满足（internal/finding/store.go:45 Save）。Save 内部已经在
// 成功路径异步触发 OnSaved hook（distill 订阅由 main 装配阶段挂载，T30）。
type FindingStore interface {
	Save(ctx context.Context, f finding.Finding) (finding.Finding, error)
}

// WriteFinding — 写或合并一条漏洞 finding。
//
// dedup_key 必填；同一 (engagement_id, dedup_key) 多次调用会触发 finding.Store 的
// 合并语义（evidence jsonb || EXCLUDED.evidence + 推进 updated_at）。
type WriteFinding struct {
	Store        FindingStore
	EngagementID string
	TaskID       string // 可空，空字符串表示无关联 task
}

// Name 返回动作名 "write_finding"。
func (a *WriteFinding) Name() string { return "write_finding" }

// Description 提供给 LLM 的简介。
func (a *WriteFinding) Description() string {
	return "写或合并一条漏洞 finding。dedup_key 必填；evidence/payload 用增量 jsonb 合并。"
}

// ParametersJSON 给出 finding 完整字段 schema。
func (a *WriteFinding) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties": {
    "kind":{"type":"string"},
    "severity":{"type":"string","enum":["info","low","medium","high","critical"]},
    "title":{"type":"string"},
    "target":{"type":"object"},
    "evidence":{"type":"object"},
    "payload":{"type":"object"},
    "tool":{"type":"string"},
    "confidence":{"type":"string","enum":["unverified","verified","rejected"]},
    "dedup_key":{"type":"string"}
  },
  "required":["kind","severity","title","dedup_key"]
}`)
}

// Execute 解析参数 → 构造 finding.Finding → Store.Save → 返回 {id, dedup_key}。
func (a *WriteFinding) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		Kind       string          `json:"kind"`
		Severity   string          `json:"severity"`
		Title      string          `json:"title"`
		Target     json.RawMessage `json:"target"`
		Evidence   json.RawMessage `json:"evidence"`
		Payload    json.RawMessage `json:"payload"`
		Tool       string          `json:"tool"`
		Confidence string          `json:"confidence"`
		DedupKey   string          `json:"dedup_key"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 write_finding 参数失败: %w", err)
	}
	if in.DedupKey == "" {
		return toolfx.Result{}, fmt.Errorf("dedup_key 必填")
	}
	if in.Kind == "" || in.Title == "" {
		return toolfx.Result{}, fmt.Errorf("kind 与 title 都不能为空")
	}

	// 工具层强制重写 dedup_key 中的 path（数字 / UUID / 长 hex → :id / :uuid / :hex），
	// 避免 LLM 拼错 path 模板导致同 endpoint 不同实例重复入库。
	in.DedupKey = finding.NormalizeDedupKey(in.DedupKey)

	// TaskID 可空：空字符串 → nil 指针，避免 FK 不存在的 task。
	var taskPtr *string
	if a.TaskID != "" {
		t := a.TaskID
		taskPtr = &t
	}

	saved, err := a.Store.Save(ctx, finding.Finding{
		EngagementID: a.EngagementID,
		TaskID:       taskPtr,
		Kind:         in.Kind,
		Severity:     finding.Severity(in.Severity),
		Title:        in.Title,
		Target:       in.Target,
		Evidence:     in.Evidence,
		Payload:      in.Payload,
		Tool:         in.Tool,
		Confidence:   finding.Confidence(in.Confidence),
		DedupKey:     in.DedupKey,
	})
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("保存 finding 失败: %w", err)
	}

	out, _ := json.Marshal(map[string]string{"id": saved.ID, "dedup_key": saved.DedupKey})
	return toolfx.Result{Output: out}, nil
}
