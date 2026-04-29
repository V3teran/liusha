package done_validator

import (
	"context"
	"encoding/json"

	"github.com/V3teran/liusha/internal/agent/action"
)

// 编译期断言：BACValidator 必须实现 action.DoneValidator 接口。
var _ action.DoneValidator = (*BACValidator)(nil)

// FactReader 抽象 memory 三层 JSON 的只读访问，由 engagement.Store 自动满足。
//
// 这里只声明 ReadState 一个方法（小接口原则）：BACValidator 仅需读 facts 看
// 是否有 evidence/boundaries，不参与写入。
type FactReader interface {
	ReadState(ctx context.Context, engagementID string) ([]byte, error)
}

// FindingChecker 抽象 finding 表的 dedup_key 查询，由 finding.Store 自动满足。
// 用 1-method 的小接口隔离，便于在测试里注入 fake。
type FindingChecker interface {
	HasDedupKey(ctx context.Context, engagementID, dedupKey string) (bool, error)
}

// validReasons 列出 BAC done 允许的退出原因，对应 spec §6.1：
//   - finding_written：写完一条 finding 即可收手；
//   - all_similar：所有候选端点跨身份响应高度相似（无越权信号）；
//   - heuristic_skip：启发式判定无须深探（如静态资源）；
//   - no_pattern_match：完整 4 步走完仍未命中已知 BAC 模式。
var validReasons = map[string]struct{}{
	"finding_written":  {},
	"all_similar":      {},
	"heuristic_skip":   {},
	"no_pattern_match": {},
}

// BACValidator 是 BAC skill 的 done 系统层裁决器（黑客松借鉴共识 C）。
//
// 每个 ReAct task 实例化一份（持 engagementID），不共用全局单例——这样既能让
// fact/finding 查询绑到具体 engagement，也避免并发任务互相污染状态。
type BACValidator struct {
	state    FactReader
	findings FindingChecker
	eid      string
}

// NewBACValidator 装配一个 BACValidator 实例。state/findings 都不能为 nil（构造方负责）。
func NewBACValidator(state FactReader, findings FindingChecker, eid string) *BACValidator {
	return &BACValidator{state: state, findings: findings, eid: eid}
}

// CanDone 实现 action.DoneValidator interface。
//
// 判定流程（任意一项不满足都返回 missing 列表，让 LLM 知道还缺什么）：
//  1. args 必须能解析出 reason 字段；
//  2. reason 必须 ∈ validReasons；
//  3. memory_facts 必须至少含 1 条 evidence 或 1 条 boundary（说明 4 个工具至少跑过 1 个并写过 fact）；
//  4. 若 reason=finding_written：args 必须含 dedup_key，且 finding 表能查到该 key。
func (v *BACValidator) CanDone(ctx context.Context, args json.RawMessage) (bool, []string) {
	var missing []string

	var p struct {
		Reason   string `json:"reason"`
		DedupKey string `json:"dedup_key"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Reason == "" {
		// 解析失败或 reason 为空——按缺 reason 处理，不再继续后续检查（因为 reason 是入口字段）。
		return false, []string{"reason"}
	}

	if _, ok := validReasons[p.Reason]; !ok {
		missing = append(missing, "valid_reason")
	}

	// 读 state 看是否至少跑过一个写 fact 的工具。
	stateBytes, err := v.state.ReadState(ctx, v.eid)
	if err != nil {
		missing = append(missing, "state_read_error")
	} else if !hasEvidenceOrBoundary(stateBytes) {
		missing = append(missing, "evidence_or_boundary")
	}

	// finding_written 需要额外的 dedup_key + 表内存在性校验。
	if p.Reason == "finding_written" {
		if p.DedupKey == "" {
			missing = append(missing, "dedup_key")
		} else {
			exists, err := v.findings.HasDedupKey(ctx, v.eid, p.DedupKey)
			switch {
			case err != nil:
				missing = append(missing, "finding_check_error")
			case !exists:
				missing = append(missing, "finding")
			}
		}
	}

	return len(missing) == 0, missing
}

// hasEvidenceOrBoundary 判断 state.facts 下 evidence 或 boundaries 数组是否至少有 1 条。
//
// State JSON 结构（来自 engagement.Store.ReadState）：
//
//	{
//	  "facts": {"evidence": [...], "boundaries": [...]},
//	  "ideas": {...}, "hints": {...}
//	}
//
// 任一数组非空即视为"BAC 工具至少跑过一轮"。
func hasEvidenceOrBoundary(stateBytes []byte) bool {
	var s struct {
		Facts json.RawMessage `json:"facts"`
	}
	if err := json.Unmarshal(stateBytes, &s); err != nil {
		return false
	}
	var facts struct {
		Evidence   []json.RawMessage `json:"evidence"`
		Boundaries []json.RawMessage `json:"boundaries"`
	}
	if err := json.Unmarshal(s.Facts, &facts); err != nil {
		return false
	}
	return len(facts.Evidence) > 0 || len(facts.Boundaries) > 0
}
