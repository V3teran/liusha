// Package einotools 把 liusha 的域工具实现为**原生** eino tool。
//
// 设计（eino 全面迁移 P2，见 docs/superpowers/specs/2026-06-07-eino-full-migration.md）：
//   - 用 utils.InferTool 从入参 struct 自动推 JSON schema（省掉手写 ParametersJSON）
//   - owner/host/hunter 等注入值用**闭包捕获**——不进 LLM 可见参数，防 LLM 串库（同旧工具语义）
//   - 工具只组装领域对象 + 调 store；校验/dedup 仍在 store 层（finding.Store.Save）
//
// 这是 P2 的样板：read_findings（无参读）+ write_finding（带参写），其余工具照此机械迁移。
package einotools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/V3teran/liusha/internal/finding"
)

// FindingReader / FindingWriter 是窄接口，*finding.Store 自动满足（同旧工具的最小接口约定）。
type FindingReader interface {
	ListByOwnerAndHost(ctx context.Context, ownerType, ownerID, host string, limit int) ([]finding.VulnFinding, error)
}

type FindingWriter interface {
	Save(ctx context.Context, f finding.VulnFinding) (finding.VulnFinding, error)
}

// FindingUpdater 是 update_finding 依赖的最小接口（*finding.Store 自动满足）。
type FindingUpdater interface {
	Update(ctx context.Context, id, summary, severity string, target, evidence json.RawMessage) error
}

// noArgs 是无入参工具的占位入参类型（InferTool 需要一个入参类型）。
type noArgs struct{}

// BuildReadFindings 造原生 eino read_findings 工具。owner/host 闭包捕获，不进 LLM 参数。
func BuildReadFindings(store FindingReader, ownerType, ownerID, host string) (tool.BaseTool, error) {
	return utils.InferTool(
		"read_findings",
		"列出本次扫描(owner+host)已有 finding（写 finding 前必查，防重复）。返回 [{id,severity,summary,created_at}]。",
		func(ctx context.Context, _ noArgs) (map[string]any, error) {
			if ownerType == "" || ownerID == "" || host == "" {
				return nil, errors.New("read_findings: owner/host 注入缺失")
			}
			fs, err := store.ListByOwnerAndHost(ctx, ownerType, ownerID, host, 0)
			if err != nil {
				return nil, err
			}
			items := make([]map[string]any, 0, len(fs))
			for _, f := range fs {
				items = append(items, map[string]any{
					"id":             f.ID,
					"severity":       f.Severity,
					"summary":        f.Summary,
					"source_flow_id": f.SourceFlowID,
					"created_at":     f.CreatedAt,
				})
			}
			return map[string]any{"count": len(items), "findings": items}, nil
		})
}

// writeFindingArgs 是 write_finding 入参；jsonschema tag → InferTool 自动生成 schema。
// target/evidence 用 map[string]any（原生 object）——天然避开旧工具的 "LLM string-encode jsonb" 坑，
// 不再需要 normalizeJSONObject。
type writeFindingArgs struct {
	Summary       string         `json:"summary"        jsonschema:"required,description=一行短标题（≤500 字符，无换行）；详情/复现进 evidence"`
	Severity      string         `json:"severity"       jsonschema:"description=critical/high/medium/low/info"`
	CWEID         string         `json:"cwe_id"         jsonschema:"description=CWE 编号（如 CWE-89），同类漏洞必须一致"`
	OWASPCategory string         `json:"owasp_category" jsonschema:"description=可选 OWASP 类别（如 A03:2021）"`
	Remediation   string         `json:"remediation"    jsonschema:"description=可选修复建议"`
	Target        map[string]any `json:"target"         jsonschema:"description=漏洞定位 object，含 method/path"`
	Evidence      map[string]any `json:"evidence"       jsonschema:"description=证据 object，必须含可复现 repro_cmd"`
	DependsOn     []string       `json:"depends_on"     jsonschema:"description=组合漏洞前置 finding id 数组；基础漏洞省略"`
}

// BuildWriteFinding 造原生 eino write_finding 工具。owner/hunter/host/flow 闭包捕获。
func BuildWriteFinding(store FindingWriter, ownerType, ownerID, hunterID, host string, flowID int64) (tool.BaseTool, error) {
	return utils.InferTool(
		"write_finding",
		"写一条新漏洞 finding。summary 一行短标题；详情/复现/payload 全进 evidence；severity 建议 critical/high/medium/low/info。质量红线见 system prompt。",
		func(ctx context.Context, in writeFindingArgs) (map[string]any, error) {
			if in.Summary == "" {
				return nil, errors.New("summary 必填")
			}
			var targetJSON, evidenceJSON json.RawMessage
			if in.Target != nil {
				targetJSON, _ = json.Marshal(in.Target)
			}
			if in.Evidence != nil {
				evidenceJSON, _ = json.Marshal(in.Evidence)
			}
			var hunterPtr *string
			if hunterID != "" {
				h := hunterID
				hunterPtr = &h
			}
			var flowPtr *int64
			if flowID != 0 {
				fid := flowID
				flowPtr = &fid
			}
			saved, err := store.Save(ctx, finding.VulnFinding{
				OwnerType:     ownerType,
				OwnerID:       ownerID,
				HunterID:      hunterPtr,
				SourceFlowID:  flowPtr,
				Host:          host,
				Severity:      in.Severity,
				Summary:       in.Summary,
				Target:        targetJSON,
				Evidence:      evidenceJSON,
				CWEID:         in.CWEID,
				OWASPCategory: in.OWASPCategory,
				Remediation:   in.Remediation,
				DependsOn:     in.DependsOn,
			})
			if err != nil {
				return nil, fmt.Errorf("保存 finding 失败: %w", err)
			}
			return map[string]any{"id": saved.ID}, nil
		})
}

// updateFindingArgs 是 update_finding 入参；id 必填，其余字段空则不动（保留原值）。
type updateFindingArgs struct {
	ID       string         `json:"id"       jsonschema:"required,description=要更新的 finding id（read_findings 拿）"`
	Summary  string         `json:"summary"  jsonschema:"description=覆盖 summary（不传则保留原值）"`
	Severity string         `json:"severity" jsonschema:"description=覆盖 severity（不传则保留原值）"`
	Target   map[string]any `json:"target"   jsonschema:"description=覆盖 target object（不传则保留原值）"`
	Evidence map[string]any `json:"evidence" jsonschema:"description=覆盖 evidence object（不传则保留原值）"`
}

// BuildUpdateFinding 造原生 eino update_finding 工具。部分覆盖一条已有 finding（保留 created_at）。
func BuildUpdateFinding(store FindingUpdater) (tool.BaseTool, error) {
	return utils.InferTool(
		"update_finding",
		"更新一条已有 finding（保留 created_at 首次发现时间，只覆盖你传的字段）。"+
			"**何时用**：read_findings 看到等价 finding，**但你的新发现更有价值**——"+
			"更详细的 PoC、更精准的 payload、更高的 severity，覆盖之前的版本让记录最优。"+
			"**何时不用**：完全等价 → 跳过；新漏洞 → write_finding 新建。"+
			"id 必填；summary/severity/target/evidence 至少传一个（空字段不动，原值保留）。",
		func(ctx context.Context, in updateFindingArgs) (map[string]any, error) {
			if in.ID == "" {
				return nil, errors.New("id 必填")
			}
			var targetJSON, evidenceJSON json.RawMessage
			if in.Target != nil {
				targetJSON, _ = json.Marshal(in.Target)
			}
			if in.Evidence != nil {
				evidenceJSON, _ = json.Marshal(in.Evidence)
			}
			if err := store.Update(ctx, in.ID, in.Summary, in.Severity, targetJSON, evidenceJSON); err != nil {
				return nil, fmt.Errorf("更新 finding 失败: %w", err)
			}
			return map[string]any{"ok": true}, nil
		})
}
