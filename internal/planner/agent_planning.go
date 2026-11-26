package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// planActions 规划主流程：分析新 Result → 有待执行动作则跳过 → 生成新 Action →
// 写图（零新增则走收敛判定）。
func (a *Agent) planActions(ctx context.Context) error {
	return a.untilGoalStable(ctx, "常规规划", a.planRound)
}

// forcePlanActions 强制重规划：不看是否还有待执行动作、也不复用既有结论，带着理由立即
// 规划一轮。
func (a *Agent) forcePlanActions(ctx context.Context, reasons []string) error {
	return a.untilGoalStable(ctx, "强制重规划", func(ctx context.Context) error {
		basis, view, err := a.planningBasis(ctx)
		if err != nil {
			return err
		}
		// 理由进规划提示，模型据此修订策略
		outcome, err := a.planner.Plan(ctx, a.graph, a.taskID, PlanRequest{
			FunctionTools: a.functionTools, ReplanReasons: reasons,
		})
		if err != nil {
			return fmt.Errorf("forced plan: %w", err)
		}
		a.logger.Info().Bool("stop", outcome.Stop).Int("action_count", len(outcome.Actions)).Strs("reasons", reasons).Msg("强制重规划完成")
		return a.applyOutcome(ctx, basis, view, outcome, "forced replan")
	})
}

// untilGoalStable 执行一轮规划；规划期间任务目标被改写（ErrGoalChanged）时，这一轮的
// 结论按旧目标得出、已整批作废，立即按新目标重做——不等下一次唤醒。每次重做都对应
// 一次真实的目标改写，不会空转。
func (a *Agent) untilGoalStable(ctx context.Context, phase string, round func(context.Context) error) error {
	for {
		err := round(ctx)
		if !errors.Is(err, explorationgraph.ErrGoalChanged) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		a.logger.Info().Str("phase", phase).Msg("规划期间任务目标已变化，本轮结论作废，按新目标重新规划")
	}
}

// planRound 是一轮常规规划。
func (a *Agent) planRound(ctx context.Context) error {
	// 1. 分析新出现的 Results。分析结论作废于目标变化时整轮重做；其余失败仅记录
	//    （分析不是规划的前置硬依赖，水位未推进，下一轮重新分析）。
	if err := a.analyzeNewResults(ctx); err != nil {
		if errors.Is(err, explorationgraph.ErrGoalChanged) {
			return err
		}
		a.logger.Warn().Err(err).Msg("分析新 Results 失败（继续规划）")
	}

	// 2. 还有待执行动作则本轮不规划
	hasOpen, err := a.hasOpenActions(ctx)
	if err != nil {
		return err
	}
	if hasOpen {
		return nil
	}

	// 3. 依据与图面在调用规划器之前读取——规划器看到的目标与图面不会比它们旧
	basis, view, err := a.planningBasis(ctx)
	if err != nil {
		return err
	}
	// 图面未变：上一次的结论依然成立，不再求解，只重做收敛判定（未完结工作可能已落定）
	if a.conclusion.holds(view) {
		return a.handleNoActions(ctx, basis, "planning")
	}
	outcome, err := a.generateNewActions(ctx)
	if err != nil {
		return err
	}

	// 4. 落定结论
	return a.applyOutcome(ctx, basis, view, outcome, "planning")
}

// planningBasis 读取规划依据与图面。
func (a *Agent) planningBasis(ctx context.Context) (explorationgraph.GoalBasis, explorationgraph.Surface, error) {
	basis, err := a.graph.GoalBasis(ctx, a.taskID)
	if err != nil {
		return basis, explorationgraph.Surface{}, fmt.Errorf("读取规划依据: %w", err)
	}
	view, err := a.graph.Surface(ctx, a.taskID, basis)
	if err != nil {
		return basis, explorationgraph.Surface{}, err
	}
	return basis, view, nil
}

// applyOutcome 落定一次规划结论（常规规划与强制重规划共用）：
//   - 停止探索：收回规划者排出、还没做完的动作（以 basis 为前置），记下结论，走收敛判定——
//     余下的只有用户指派的动作、待验证假设与待分析结果，落定后即收敛；
//   - 图面有了变化（排了新动作，或判定不追了线索）：落图，此前的结论随之作废，下一轮按
//     新图面重新规划；
//   - 没有变化：记下「等待」结论，走收敛判定。
//
// 目标在规划期间被改写时返回 ErrGoalChanged，什么都不落。
func (a *Agent) applyOutcome(ctx context.Context, basis explorationgraph.GoalBasis, view explorationgraph.Surface, outcome PlanOutcome, trigger string) error {
	if outcome.Stop {
		withdrawn, err := a.graph.StopExploration(ctx, a.taskID, basis, stopReason)
		if err != nil {
			return fmt.Errorf("停止探索: %w", err)
		}
		a.logger.Info().Str("trigger", trigger).Strs("withdrawn", withdrawn).Msg("规划者停止探索，已收回未做完的规划动作")
		a.conclusion = &conclusion{stop: true, view: view}
		return a.handleNoActions(ctx, basis, trigger)
	}
	changed, err := a.applyPlan(ctx, basis, outcome)
	if err != nil {
		return err
	}
	if changed {
		a.conclusion = nil
		return nil
	}
	a.conclusion = &conclusion{view: view}
	return a.handleNoActions(ctx, basis, trigger)
}

// stopReason 是规划者停止探索时收回动作所记的原因。
const stopReason = "规划者已停止探索，收回未做完的规划动作"

// applyPlan 把规划提案以 basis 为前置原子落图（与结果分析的修订同一条写入路径），返回
// 图面是否因此有了变化——新增了动作，或判定不追了线索。动作全被过滤且没有不追处置，
// 与「没有提案」是同一局面，由调用方走无动作处理。目标在规划期间被改写时返回
// ErrGoalChanged，什么都不落。
func (a *Agent) applyPlan(ctx context.Context, basis explorationgraph.GoalBasis, outcome PlanOutcome) (bool, error) {
	drafts := a.admitActions(ctx, outcome.Actions)
	if len(drafts) == 0 && len(outcome.DeclinedLeads) == 0 {
		return false, nil
	}
	report, err := a.graph.RevisePlan(ctx, a.taskID, explorationgraph.PlanRevision{
		Basis: basis, AddedActions: drafts, DeclinedLeads: outcome.DeclinedLeads,
	})
	for _, r := range report.Rejected {
		a.logger.Warn().Str("field", string(r.Field)).Str("ref", r.ID).Str("reason", r.Reason).Msg("规划提案剔除无效引用")
	}
	if err != nil {
		return false, fmt.Errorf("写入规划修订: %w", err)
	}
	for _, id := range report.AddedActions {
		a.eventBus.PublishActionProposed(a.taskID, id)
	}
	if len(report.AddedActions) > 0 {
		a.logger.Info().Strs("action_ids", report.AddedActions).Msg("新 Action 已落图")
	}
	if len(report.DeclinedLeads) > 0 {
		a.logger.Info().Strs("lead_ids", report.DeclinedLeads).Msg("线索已判定不追")
	}

	pursued := slices.ContainsFunc(drafts, func(d explorationgraph.ActionDraft) bool { return d.PursuesLead != "" })
	if outcome.OpenLeadCount > 0 && !pursued && len(report.DeclinedLeads) == 0 {
		// 章程要求待深挖的线索非空时本轮至少处理一条。这里只留证据，不替规划者补动作：
		// 是否已构成功能障碍由监察者据 open_leads 判断。
		a.logger.Warn().Int("open_leads", outcome.OpenLeadCount).
			Msg("本轮有待深挖的线索，规划者既未认领也未判定不追")
	}
	return len(report.AddedActions) > 0 || len(report.DeclinedLeads) > 0, nil
}

// admitActions 过滤规划提案：指令级字面去重（见 admit）——空指令不可执行，与图上既有
// 动作（已中止的除外）或本批更早提案 instruction 字面一致的是复读，两者都跳过（语义
// 去重靠规划者看得到的已做、在途动作与待验证、已证伪的假设）。动作统一挂在根目标下、
// 溯源边 result → action 随动作落图（见 RevisePlan）。
func (a *Agent) admitActions(ctx context.Context, actions []explorationgraph.ActionDraft) []explorationgraph.ActionDraft {
	if len(actions) == 0 {
		return nil
	}
	known, err := a.knownInstructions(ctx)
	if err != nil {
		a.logger.Warn().Err(err).Msg("读取既有动作指令失败（跳过去重，直接写入）")
		known = map[string]bool{}
	}

	admitted := make([]explorationgraph.ActionDraft, 0, len(actions))
	for _, draft := range actions {
		var c struct {
			Instruction string `json:"instruction"`
		}
		_ = json.Unmarshal(draft.Node.Content, &c)
		if _, ok := admit(known, c.Instruction); !ok {
			a.logger.Info().Str("instruction", c.Instruction).Msg("提案为空或与既有动作指令重复，跳过")
			continue
		}
		admitted = append(admitted, draft)
	}
	return admitted
}

// knownInstructions 汇总图上动作的 instruction（去首尾空白后的字面去重集），已中止的除外。
func (a *Agent) knownInstructions(ctx context.Context) (map[string]bool, error) {
	all, err := a.graph.ListNodesByKind(ctx, a.taskID, core.KindAction)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(all))
	for _, action := range all {
		// 已中止的动作没做完、也不含对错判断：按当前目标重提同一指令是正当的。
		if action.State != nil && *action.State == explorationgraph.StateAborted {
			continue
		}
		var c struct {
			Instruction string `json:"instruction"`
		}
		if json.Unmarshal(action.Content, &c) == nil {
			if instruction := strings.TrimSpace(c.Instruction); instruction != "" {
				set[instruction] = true
			}
		}
	}
	return set, nil
}

// liveReplanReasons 取出仍然成立的重规划请求的理由。带着所见图面的请求，在此期间目标被
// 改写、出现了新的派生目标或新结果，判断的前提已经变了，作废并记日志——发起方周期运行，
// 问题仍在会再次提出，作废的代价至多一个周期；照旧执行则会按过时的判断推翻规划者此后
// 的结论。新增的动作不使请求作废：团队原地打转时动作照样增长，那正是请求要纠正的局面。
// 读不到当前图面时保守保留全部请求。
func (a *Agent) liveReplanReasons(ctx context.Context, requests []replanRequest) []string {
	if len(requests) == 0 {
		return nil
	}
	_, now, err := a.planningBasis(ctx)
	if err != nil {
		a.logger.Warn().Err(err).Msg("读取图面失败，重规划请求全部保留")
	}
	reasons := make([]string, 0, len(requests))
	for _, r := range requests {
		if err == nil && r.seen != nil && premiseChanged(*r.seen, now) {
			a.logger.Info().Str("reason", r.reason).Msg("重规划请求作废：发起后图面已有实质进展，判断前提已变")
			continue
		}
		reasons = append(reasons, r.reason)
	}
	return reasons
}

// premiseChanged 判断两个图面之间是否有实质进展：目标改写、新的派生目标、新结果。
func premiseChanged(seen, now explorationgraph.Surface) bool {
	return seen.Goal != now.Goal || seen.Objectives != now.Objectives || seen.Results != now.Results
}
