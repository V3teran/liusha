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

// analyzeAndProcessResults 分析增量 Results，并把结论（新方向）连同分析水位作为一次
// 规划修订原子落图：要么全落、要么全不落。分析只追加方向，不排动作。失败返回错误——
// 水位随修订一起未落，下一轮整体重做；
// 因为没有部分生效的写入，重做不会重复落图。分析以调用前读取的目标为依据，分析期间
// 目标被改写则返回 ErrGoalChanged（结论作废，按新目标重新分析）。
func (a *Agent) analyzeAndProcessResults(ctx context.Context, results []explorationgraph.Node) error {
	a.logger.Info().Int("result_count", len(results)).Msg("开始分析 Results")

	if len(results) == 0 {
		return nil
	}

	basis, err := a.graph.GoalBasis(ctx, a.taskID)
	if err != nil {
		return fmt.Errorf("读取分析依据: %w", err)
	}
	analysis, err := a.planner.AnalyzeResults(ctx, a.graph, a.taskID, results)
	if err != nil {
		return fmt.Errorf("analyze results: %w", err)
	}

	a.logger.Info().
		Int("new_objectives", len(analysis.NewObjectives)).
		Str("reasoning", analysis.Reasoning).
		Msg("Result 分析完成")

	revision, err := a.planRevision(analysis, a.knownObjectives(ctx))
	if err != nil {
		return err
	}
	revision.Basis = basis
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

	a.logger.Info().Strs("added_objectives", report.AddedObjectives).Msg("规划修订已落图")
	return nil
}

// admit 判定提案文本可否落图：非空且未见过则登记并放行（规划的动作指令与分析的
// 新目标描述各用一个集合）。
func admit(seen map[string]bool, text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || seen[text] {
		return text, false
	}
	seen[text] = true
	return text, true
}

// knownObjectives 读取图上全部目标描述（去首尾空白，含根目标）。
// 读失败降级为空集：本轮跳过去重，不阻断分析结论落图。
func (a *Agent) knownObjectives(ctx context.Context) map[string]bool {
	known := map[string]bool{}
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
				known[desc] = true
			}
		}
	}
	return known
}

// planRevision 把分析结论翻译为规划修订（构造新节点，引用原样交给 RevisePlan
// 校验）。新目标先按描述字面去重：分析会复述目标清单，复读在这里拦下。
func (a *Agent) planRevision(analysis *ResultAnalysis, knownObjectives map[string]bool) (explorationgraph.PlanRevision, error) {
	revision := explorationgraph.PlanRevision{}
	for _, obj := range analysis.NewObjectives {
		description, ok := admit(knownObjectives, obj.Description)
		if !ok {
			a.logger.Info().Str("description", obj.Description).Msg("新目标为空或与既有目标描述重复，跳过")
			continue
		}
		node, err := a.derivedObjectiveNode(obj.Priority, map[string]interface{}{
			"description": description,
			"reasoning":   obj.Reasoning,
		})
		if err != nil {
			return revision, err
		}
		revision.AddedObjectives = append(revision.AddedObjectives, explorationgraph.ObjectiveDraft{
			Node:        node,
			TriggeredBy: obj.TriggeredBy,
		})
	}
	return revision, nil
}

// derivedObjectiveNode 构造结果分析提出的派生目标节点（open，来源 planner；目标必须有
// 非空 state，DB 约束 ck_state_by_kind）。
func (a *Agent) derivedObjectiveNode(priority core.Priority, content map[string]interface{}) (explorationgraph.Node, error) {
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return explorationgraph.Node{}, fmt.Errorf("序列化派生目标内容: %w", err)
	}
	openState := explorationgraph.StateOpen
	now := time.Now()
	return explorationgraph.Node{
		ID:         uuid.New().String(),
		TaskID:     a.taskID,
		Kind:       core.KindObjective,
		Content:    contentJSON,
		Priority:   priority,
		State:      &openState,
		SourceType: explorationgraph.SourcePlanner,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

// GraphPlanner 规划接口（由 planner.New 提供）
type GraphPlanner interface {
	Plan(ctx context.Context, graph *explorationgraph.Store, taskID string, req PlanRequest) (PlanOutcome, error)
	AnalyzeResults(ctx context.Context, graph *explorationgraph.Store, taskID string, results []explorationgraph.Node) (*ResultAnalysis, error)
}

// ResultAnalysis 是对 Results 的分析结果
