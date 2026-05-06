package common

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/toolfx"
	"github.com/V3teran/liusha/internal/vulnfinding"
)

// FindingStore 是 WriteFinding 依赖的最小接口。
//
// 由 *vulnfinding.Store 自动满足。Save 是 append-only：每次都 INSERT 新行，
// 返回 (Finding, isFirstSeen, error)；异步触发 OnSaved（首次发现）或 OnReSaved（重发现）钩子。
type FindingStore interface {
	Save(ctx context.Context, f vulnfinding.VulnFinding) (vulnfinding.VulnFinding, bool, error)
}

// WriteFinding — 写或合并一条漏洞 finding。
//
// dedup_key 必填；v1.2 改为 (host, dedup_key) 全局唯一去重，跨 engagement 同 endpoint
// 合并 evidence。
//
// Host 由 builder 从 BuilderParams.Host 注入（不让 LLM 自填，避免拼错）；
// 用作 finding.Host 列填充（migration 0004 强约束 NOT NULL CHECK <>”）。
type WriteFinding struct {
	Store        FindingStore
	EngagementID string
	TaskID       string // 可空，空字符串表示无关联 task
	Host         string // builder 注入；空时 Save 报错
	FlowID       int64  // 触发本次 sub-task 的 http_flow.id；0 表示不关联（如主 ReAct 直发）
}

// Name 返回动作名 "write_finding"。
func (a *WriteFinding) Name() string { return "write_finding" }

// Description 提供给 LLM 的简介。
func (a *WriteFinding) Description() string {
	return "写一条漏洞 finding（append-only）。dedup_key 必填；evidence 承载证据 jsonb。"
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
    "confidence":{"type":"string","enum":["high","medium","low"],"description":"自评置信度：high=具名工具默认参数即坐实；medium=升级参数/自构 PoC 复测才坐实，或仅强 body 关键字；low=仅相似度差分/弱关键字"},
    "dedup_key":{"type":"string"}
  },
  "required":["kind","severity","title","confidence","dedup_key"]
}`)
}

// Execute 解析参数 → 构造 vulnfinding.VulnFinding → Store.Save → 返回 {id, dedup_key}。
func (a *WriteFinding) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		Kind       string          `json:"kind"`
		Severity   string          `json:"severity"`
		Title      string          `json:"title"`
		Target     json.RawMessage `json:"target"`
		Evidence   json.RawMessage `json:"evidence"`
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
	in.DedupKey = vulnfinding.NormalizeDedupKey(in.DedupKey)

	// 工具层强制 evidence schema：kind="bac.*" 必须满足 BACEvidence 必填字段；
	// 其他 kind 暂不约束（YAGNI，等加 SSRF/IDOR 时扩展）。校验失败拒绝写库，
	// 让 LLM 看到结构错误后重试，避免 evidence 字段散乱。
	if err := vulnfinding.ValidateEvidence(in.Kind, in.Evidence); err != nil {
		return toolfx.Result{}, fmt.Errorf("evidence schema 校验失败: %w", err)
	}

	// TaskID 可空：空字符串 → nil 指针，避免 FK 不存在的 task。
	var taskPtr *string
	if a.TaskID != "" {
		t := a.TaskID
		taskPtr = &t
	}
	// FlowID 可空：0 → nil 指针（主 ReAct 直发 finding 不绑定具体流量时）。
	var flowPtr *int64
	if a.FlowID != 0 {
		fid := a.FlowID
		flowPtr = &fid
	}

	saved, _, err := a.Store.Save(ctx, vulnfinding.VulnFinding{
		EngagementID: a.EngagementID,
		TaskID:       taskPtr,
		SourceFlowID: flowPtr,
		Host:         a.Host,
		Kind:         in.Kind,
		Severity:     vulnfinding.Severity(in.Severity),
		Title:        in.Title,
		Target:       in.Target,
		Evidence:     in.Evidence,
		Confidence:   vulnfinding.Confidence(in.Confidence),
		DedupKey:     in.DedupKey,
	})
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("保存 finding 失败: %w", err)
	}

	out, _ := json.Marshal(map[string]string{"id": saved.ID, "dedup_key": saved.DedupKey})
	return toolfx.Result{Output: out}, nil
}
