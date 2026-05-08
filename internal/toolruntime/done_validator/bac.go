package done_validator

import (
	"context"
	"encoding/json"

	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// 编译期断言：BACValidator 必须实现 toolfx.DoneValidator 接口。
var _ toolfx.DoneValidator = (*BACValidator)(nil)

// FactReader 抽象 memory 三层 JSON 的只读访问，由 engagement.Store 自动满足。
//
// v1.2 关键修复（task scope 隔离）：用 ReadStateScoped 而非 ReadState——
// 否则跨 task 在同一 engagement 内运行时，validator 会看到其他 task 写的 evidence
// 而误判本 task 已"完成"，让什么都没干的 task 也能 done 通过。
type FactReader interface {
	ReadStateScoped(ctx context.Context, engagementID string, opts engagement.ReadOpts) ([]byte, error)
}

// FindingChecker 抽象 finding 表的 dedup_key 查询，由 finding.Store 自动满足。
// 用 1-method 的小接口隔离，便于在测试里注入 fake。
type FindingChecker interface {
	HasDedupKey(ctx context.Context, engagementID, dedupKey string) (bool, error)
}

// validReasons 列出 done 允许的退出原因（agentic 路线简化：从 4 类缩到 2 类）。
//
//   - finding_written：写完一条 finding 即可收手；必校验 dedup_key 真存在
//   - no_pattern_match：未命中漏洞（含原 all_differ / heuristic_skip 等"无漏洞"情况；
//     具体细节由 LLM 在 take_note / finding.evidence.reasoning 自由表达，不再用 enum 区分）
//
// 简化理由：原 4 类把 telemetry 标签塞进 reason enum 限制了 LLM 表达。agentic 路线下
// LLM 用自然语言描述细节，工具层只校验"是否真完成"。
var validReasons = map[string]struct{}{
	"finding_written":  {},
	"no_pattern_match": {},
}

// BACValidator 是 BAC skill 的 done 系统层裁决器（黑客松借鉴共识 C）。
//
// 每个 ReAct task 实例化一份（持 engagementID + taskID），不共用全局单例——
// 既能让 fact/finding 查询绑到具体 engagement，也通过 taskID 隔离同 engagement
// 并发 task 的 facts 串扰（v1.2 关键修复：原 ReadState 全量读会让 B 看到 A 的 evidence
// 误判已完成）。
type BACValidator struct {
	state    FactReader
	findings FindingChecker
	eid      string
	taskID   string
}

// NewBACValidator 装配一个 BACValidator 实例。state/findings 都不能为 nil（构造方负责）。
//
// taskID 来自 BuilderParams.TaskID（delegate 生成 UUID）；空字符串退化为 engagement
// 全量读（仅向后兼容；正常装配路径不应空）。
func NewBACValidator(state FactReader, findings FindingChecker, eid, taskID string) *BACValidator {
	return &BACValidator{state: state, findings: findings, eid: eid, taskID: taskID}
}

// CanDone 实现 toolfx.DoneValidator interface。
//
// 判定流程（任意一项不满足都返回 missing 列表，让 LLM 知道还缺什么）：
//  1. args 必须能解析出 reason 字段；
//  2. reason 必须 ∈ validReasons；
//  3. engagement.memory_notes 中本 task 写过的 note 必须至少含 1 条 kind=observation 或 boundary（说明 4 个工具至少跑过 1 个并 take_note）；
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

	// 读 state 看是否至少跑过一个写 fact 的工具——按 task 视图，避免跨 task 串扰。
	stateBytes, err := v.state.ReadStateScoped(ctx, v.eid, engagement.ReadOpts{TaskID: v.taskID})
	if err != nil {
		missing = append(missing, "state_read_error")
	} else if !hasEvidenceOrBoundary(stateBytes, v.taskID) {
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

// hasEvidenceOrBoundary 判断本 task 是否在 engagement.memory_notes 写过至少 1 条
// kind ∈ {observation, boundary} 的 note。
//
// State JSON 结构（v1.2 收尾后，来自 engagement.Store.ReadStateScoped）：
//
//	{
//	  "notes": {"notes": [{"kind":"observation|hypothesis|boundary","content":"...","agent_run_id":"..."}]}
//	}
//
// 严格按 task_id 匹配——避免跨 task 串扰（A 写过 B 没干活也能 done 通过）。
// taskID 为空时退化为"engagement 内任意 observation/boundary 即可"（向后兼容，但生产路径不应空）。
func hasEvidenceOrBoundary(stateBytes []byte, taskID string) bool {
	var s struct {
		Notes json.RawMessage `json:"notes"`
	}
	if err := json.Unmarshal(stateBytes, &s); err != nil {
		return false
	}
	var box struct {
		Notes []struct {
			Kind   string `json:"kind"`
			TaskID string `json:"agent_run_id"`
		} `json:"notes"`
	}
	if err := json.Unmarshal(s.Notes, &box); err != nil {
		return false
	}
	for _, n := range box.Notes {
		if n.Kind != "observation" && n.Kind != "boundary" {
			continue
		}
		if taskID == "" || n.TaskID == taskID {
			return true
		}
	}
	return false
}
