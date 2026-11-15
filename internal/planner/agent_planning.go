package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// planActions 规划主流程：分析新 Result → 有待执行动作则跳过 → 生成新 Action →
// 写图（零新增则走收敛判定）。
func (a *Agent) planActions(ctx context.Context) error {
	return a.untilGoalStable(ctx, "常规规划", a.planRound)
}

// forcePlanActions 强制重规划：不看是否还有待执行动作，带着理由立即规划一轮。
func (a *Agent) forcePlanActions(ctx context.Context, reasons []string) error {
	return a.untilGoalStable(ctx, "强制重规划", func(ctx context.Context) error {
		basis, err := a.graph.GoalBasis(ctx, a.taskID)
		if err != nil {
			return fmt.Errorf("读取规划依据: %w", err)
		}
		// 理由进规划提示，模型据此修订策略
		actions, err := a.planner.Plan(ctx, a.graph, a.taskID, PlanRequest{
			FunctionTools: a.functionTools, ReplanReasons: reasons,
		})
		if err != nil {
			return fmt.Errorf("forced plan: %w", err)
		}
		a.logger.Info().Int("action_count", len(actions)).Strs("reasons", reasons).Msg("强制重规划完成")
		return a.applyPlan(ctx, basis, actions, "forced replan")
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

	// 3. 生成新的 Actions：依据在调用规划器之前读取——规划器看到的目标不会比依据旧
	basis, err := a.graph.GoalBasis(ctx, a.taskID)
	if err != nil {
		return fmt.Errorf("读取规划依据: %w", err)
	}
	actions, err := a.generateNewActions(ctx)
	if err != nil {
		return err
	}

	// 4. 落图；没有实际新增动作即走收敛判定
	return a.applyPlan(ctx, basis, actions, "planning")
}

// applyPlan 把规划提案以 basis 为前置原子落图（与结果分析的修订同一条写入路径）。
// 实际新增为零——没有提案，或提案全被过滤（空指令 / 与既有动作重复）——与「planner
// 无新方向」是同一局面，走无动作处理（收敛判定）；否则图静止而每次唤醒都再付费规划
// 一遍。目标在规划期间被改写时返回 ErrGoalChanged，什么都不落。
func (a *Agent) applyPlan(ctx context.Context, basis explorationgraph.GoalBasis, actions []explorationgraph.ActionDraft, trigger string) error {
	drafts := a.admitActions(ctx, actions)
	if len(drafts) == 0 {
		return a.handleNoActions(ctx, basis, trigger)
	}
	report, err := a.graph.RevisePlan(ctx, a.taskID, explorationgraph.PlanRevision{Basis: basis, AddedActions: drafts})
	for _, r := range report.Rejected {
		a.logger.Warn().Str("field", string(r.Field)).Str("ref", r.ID).Str("reason", r.Reason).Msg("规划提案剔除无效引用")
	}
	if err != nil {
		return fmt.Errorf("写入 Action: %w", err)
	}
	for _, id := range report.AddedActions {
		a.eventBus.PublishActionProposed(a.taskID, id)
	}
	a.logger.Info().Strs("action_ids", report.AddedActions).Msg("新 Action 已落图")
	return nil
}

// admitActions 过滤规划提案：指令级去重（与结果分析的延续动作同一口径，见 admit）——
// 空指令不可执行，与图上既有动作（已中止的除外）或本批更早提案 instruction 字面一致的
// 是复读，两者都跳过（语义去重仍靠 LLM 的 refuted 视野）。动作统一挂在根目标下、溯源
// 边 result → action 随动作落图（见 RevisePlan）。
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
