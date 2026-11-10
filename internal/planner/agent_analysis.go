package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// analyzeAndProcessResults 分析增量 Results，并把结论连同分析水位作为一次规划
// 修订原子落图：达成、新方向及其吸收、延续动作互不排斥，分析给出几样就落几样，
// 要么全落、要么全不落。失败返回错误——水位随修订一起未落，下一轮整体重做；
// 因为没有部分生效的写入，重做不会重复落图。
func (a *Agent) analyzeAndProcessResults(ctx context.Context, results []explorationgraph.Node) error {
	a.logger.Info().Int("result_count", len(results)).Msg("开始分析 Results")

	if len(results) == 0 {
		return nil
	}

	analysis, err := a.planner.AnalyzeResults(ctx, a.graph, a.taskID, results)
	if err != nil {
		return fmt.Errorf("analyze results: %w", err)
	}

	a.logger.Info().
		Int("completed_objectives", len(analysis.CompletedObjectives)).
		Int("new_objectives", len(analysis.NewObjectives)).
		Int("continuation_actions", len(analysis.ContinuationActions)).
		Str("reasoning", analysis.Reasoning).
		Msg("Result 分析完成")

	revision, err := a.planRevision(analysis, a.knownProposals(ctx))
	if err != nil {
		return err
	}
	for _, r := range results {
		revision.Analyzed = append(revision.Analyzed, r.ID)
	}

	report, err := a.graph.RevisePlan(ctx, a.taskID, revision)
	for _, r := range report.Rejected {
		a.logger.Warn().
			Str("field", string(r.Field)).
			Str("ref", r.ID).
			Str("reason", r.Reason).
			Msg("规划修订剔除无效引用")
	}
	if err != nil {
		return fmt.Errorf("规划修订: %w", err)
	}

	a.logger.Info().
		Strs("added_objectives", report.AddedObjectives).
		Strs("added_actions", report.AddedActions).
		Strs("completed", report.CompletedObjectives).
		Strs("superseded", report.SupersededObjectives).
		Msg("规划修订已落图")
	evidence := make(map[string][]string, len(revision.CompletedObjectives))
	for _, c := range revision.CompletedObjectives {
		evidence[c.ObjectiveID] = c.Evidence
	}
	for _, id := range report.CompletedObjectives {
		a.logger.Info().Str("objective_id", id).Strs("evidence", evidence[id]).Msg("派生目标已达成")
	}
	for _, id := range report.AddedActions {
		a.eventBus.PublishActionProposed(a.taskID, id)
	}
	return nil
}

// knownProposals 是图上既有提案的字面集合（去首尾空白）：动作指令与目标描述。
// 结果分析的新提案逐条与它比对并登记，空提案与字面复读（含本批内）在落图前
// 拦断——与规划产出动作的指令去重同一口径。
type knownProposals struct {
	instructions map[string]bool
	objectives   map[string]bool
}

// admit 判定提案文本可否落图：非空且未见过则登记并放行。
func admit(seen map[string]bool, text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || seen[text] {
		return text, false
	}
	seen[text] = true
	return text, true
}

// knownProposals 读取图上全部动作指令与目标描述（含根目标与已收敛目标）。
// 读失败降级为空集：本轮跳过去重，不阻断分析结论落图。
func (a *Agent) knownProposals(ctx context.Context) knownProposals {
	known := knownProposals{instructions: map[string]bool{}, objectives: map[string]bool{}}
	if instructions, err := a.knownInstructions(ctx); err == nil {
		known.instructions = instructions
	} else {
		a.logger.Warn().Err(err).Msg("读取既有动作指令失败（延续动作跳过去重）")
	}
	objectives, err := a.graph.ListNodesByKind(ctx, a.taskID, core.KindObjective)
	if err != nil {
		a.logger.Warn().Err(err).Msg("读取既有目标失败（新目标跳过去重）")
		return known
	}
	for _, obj := range objectives {
		var c struct {
			Description string `json:"description"`
		}
		if json.Unmarshal(obj.Content, &c) == nil {
			if desc := strings.TrimSpace(c.Description); desc != "" {
				known.objectives[desc] = true
			}
		}
	}
	return known
}

// planRevision 把分析结论翻译为规划修订（构造新节点，引用原样交给 RevisePlan
// 校验）。新目标与延续动作先过 knownProposals 的字面去重——分析视野里看不到
// 既有动作，目标清单也可能被复述，复读只能在这里结构化拦断。
func (a *Agent) planRevision(analysis *ResultAnalysis, known knownProposals) (explorationgraph.PlanRevision, error) {
	revision := explorationgraph.PlanRevision{CompletedObjectives: analysis.CompletedObjectives}
	for _, obj := range analysis.NewObjectives {
		description, ok := admit(known.objectives, obj.Description)
		if !ok {
			a.logger.Info().Str("description", obj.Description).Msg("新目标为空或与既有目标描述重复，跳过")
			continue
		}
		node, err := a.analysisNode(core.KindObjective, obj.Priority, map[string]interface{}{
			"description": description,
			"reasoning":   obj.Reasoning,
		})
		if err != nil {
			return revision, err
		}
		revision.AddedObjectives = append(revision.AddedObjectives, explorationgraph.ObjectiveDraft{
			Node:        node,
			TriggeredBy: obj.TriggeredBy,
			Supersedes:  obj.Supersedes,
		})
	}
	for _, action := range analysis.ContinuationActions {
		instruction, ok := admit(known.instructions, action.Instruction)
		if !ok {
			a.logger.Info().Str("instruction", action.Instruction).Msg("延续动作为空或与既有动作指令重复，跳过")
			continue
		}
		node, err := a.analysisNode(core.KindAction, action.Priority, map[string]interface{}{
			"instruction": instruction,
			"reasoning":   action.Reasoning,
		})
		if err != nil {
			return revision, err
		}
		revision.AddedActions = append(revision.AddedActions, explorationgraph.ActionDraft{
			Node:        node,
			TriggeredBy: action.TriggeredBy,
		})
	}
	return revision, nil
}

// analysisNode 构造结果分析产出的新节点（open，来源 planner/result-analyzer）。
// objective 与 action 都必须有非空 state（DB 约束 ck_state_by_kind）。
func (a *Agent) analysisNode(kind core.NodeKind, priority core.Priority, content map[string]interface{}) (explorationgraph.Node, error) {
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return explorationgraph.Node{}, fmt.Errorf("序列化 %s 内容: %w", kind, err)
	}
	openState := explorationgraph.StateOpen
	now := time.Now()
	return explorationgraph.Node{
		ID:         uuid.New().String(),
		TaskID:     a.taskID,
		Kind:       kind,
		Content:    contentJSON,
		Priority:   priority,
		State:      &openState,
		SourceType: explorationgraph.SourcePlanner,
		SourceID:   "result-analyzer",
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

// GraphPlanner 规划接口（由 planner.New 提供）
type GraphPlanner interface {
	Plan(ctx context.Context, graph *explorationgraph.Store, taskID string, functionTools []string) ([]explorationgraph.Node, error)
	AnalyzeResults(ctx context.Context, graph *explorationgraph.Store, taskID string, results []explorationgraph.Node) (*ResultAnalysis, error)
}

// ResultAnalysis 是对 Results 的分析结果
