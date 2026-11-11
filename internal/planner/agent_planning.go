package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// planActions 规划主流程：消费 monitor 纠偏标记 → 分析新 Result →
// 有可执行动作则跳过 → 收敛去重 → 生成新 Action → 写图。
func (a *Agent) planActions(ctx context.Context) error {
	a.logger.Debug().
		Str("caller", "planActions").
		Msg("▶ planActions 入口")

	defer a.logger.Debug().
		Str("caller", "planActions").
		Msg("◀ planActions 出口")

	a.logger.Debug().Msg("开始规划 Action")

	// 0. 外部纠偏的持久化恢复：monitor 的重规划请求双写在 objective 上，
	//    事件丢失（bus fire-and-forget）时由这里兜底——at-least-once。
	if forced, reason := a.consumeMonitorRequest(ctx); forced {
		return a.forcePlanActions(ctx, reason)
	}

	// 1. 分析新出现的 Results（失败仅记录：分析不是规划的前置硬依赖）
	if err := a.analyzeNewResults(ctx); err != nil {
		a.logger.Warn().Err(err).Msg("分析新 Results 失败（继续规划）")
	}

	// 2. 检查是否已有可执行的 Action
	hasExecutable, err := a.checkExecutableActions(ctx)
	if err != nil {
		return err
	}
	if hasExecutable {
		return nil
	}

	// 2.5 收敛去重：上轮已判定「无新方向」且图终态指纹无变化——验证事件
	//     成串到达的尾段不再重复审议（实测 5 次同结论、~18% LLM 浪费）。
	if a.convergedPending {
		if fp, fpErr := a.graphFingerprint(ctx); fpErr == nil && fp.equal(a.convergedAt) {
			a.logger.Info().Msg("图终态无新变化，跳过重复规划（上轮已收敛）")
			return nil
		}
	}

	// 3. 生成新的 Actions
	actions, err := a.generateNewActions(ctx)
	if err != nil {
		return err
	}

	// 4. 落图；没有实际新增动作即走收敛判定
	return a.applyPlan(ctx, actions, "planning")
}

// applyPlan 把规划提案落图。实际新增为零——没有提案，或提案全被过滤（空指令 / 与
// 既有动作重复）——与「planner 无新方向」是同一局面，走无动作处理（收敛判定）；
// 否则图静止而每次唤醒都再付费规划一遍。有新增才解除收敛挂起。
func (a *Agent) applyPlan(ctx context.Context, actions []explorationgraph.Node, trigger string) error {
	written := 0
	if len(actions) > 0 {
		n, err := a.writeActionsFromPlan(ctx, actions)
		if err != nil {
			return err
		}
		written = n
	}
	if written == 0 {
		a.handleNoActions(ctx, trigger)
		return nil
	}
	a.convergedPending = false
	return nil
}

// rootObjective 返回任务的根目标节点（见 explorationgraph.RootObjective）。
// 规划产出的 Action 以根任务目标为归属。
func (a *Agent) rootObjective(ctx context.Context) (explorationgraph.Node, error) {
	objectives, err := a.graph.ListNodesByKind(ctx, a.taskID, core.KindObjective)
	if err != nil {
		return explorationgraph.Node{}, fmt.Errorf("获取 Objectives 失败: %w", err)
	}
	root, ok := explorationgraph.RootObjective(objectives)
	if !ok {
		return explorationgraph.Node{}, fmt.Errorf("当前无根 Objective 节点")
	}
	return root, nil
}

// writeActionsFromPlan 返回实际新增的动作数。它将 planner 返回的 action 节点连同目标归属边
// （根 objective → action）与发现滋生边（result → action，TRIGGERS）原子写入图。
// 指令级去重（与结果分析的延续动作同一口径，见 admit）：空指令不可执行，与图上
// 既有动作 instruction 字面一致的提案是复读，两者都跳过（语义去重仍靠 LLM 的
// refuted 视野）。
func (a *Agent) writeActionsFromPlan(ctx context.Context, actions []explorationgraph.Node) (int, error) {
	a.logger.Info().
		Int("action_count", len(actions)).
		Msg("准备写入 Actions 到探索图")

	// 既有指令集（含本批已写入的）：字面重复提案在此拦断。
	known, err := a.knownInstructions(ctx)
	if err != nil {
		a.logger.Warn().Err(err).Msg("读取既有动作指令失败（跳过去重，直接写入）")
		known = map[string]bool{}
	}

	// 动作统一归属根目标（与结果分析、人工注入同一口径）：无根目标即无处归属，
	// 整批不写，不落孤立动作。
	root, err := a.rootObjective(ctx)
	if err != nil {
		return 0, fmt.Errorf("写入 Action: %w", err)
	}

	written, failed, skipped := 0, 0, 0
	for i, action := range actions {
		var c struct {
			Instruction string   `json:"instruction"`
			TriggeredBy []string `json:"triggered_by"`
		}
		_ = json.Unmarshal(action.Content, &c)
		if _, ok := admit(known, c.Instruction); !ok {
			skipped++
			a.logger.Info().Str("instruction", c.Instruction).Msg("提案为空或与既有动作指令重复，跳过写入")
			continue
		}

		a.logger.Info().
			Int("index", i).
			Str("action_id", action.ID).
			Msg("准备创建 Action")

		// 边集：目标归属（generates）+ 发现滋生（triggers）——后者是因果链
		// 溯源（成果A 催生本动作）的图上事实，与节点原子落库。
		edges := make([]explorationgraph.Edge, 0, 1+len(c.TriggeredBy))
		edges = append(edges, explorationgraph.Edge{
			TaskID:    a.taskID,
			SrcID:     root.ID,
			Rel:       explorationgraph.RelGenerates,
			DstID:     action.ID,
			CreatedAt: time.Now(),
		})
		for _, resultID := range c.TriggeredBy {
			edges = append(edges, explorationgraph.Edge{
				TaskID:    a.taskID,
				SrcID:     resultID,
				Rel:       explorationgraph.RelTriggers,
				DstID:     action.ID,
				CreatedAt: time.Now(),
			})
		}

		if writeErr := a.graph.CreateNodeWithEdges(ctx, action, edges); writeErr != nil {
			failed++
			a.logger.Error().
				Err(writeErr).
				Str("action_id", action.ID).
				Msg("写入 Action 失败")
			continue
		}

		written++
		a.logger.Info().
			Str("action_id", action.ID).
			Str("priority", string(action.Priority)).
			Msg("新 Action 已生成")

		// 发布 ActionProposed 事件
		a.eventBus.PublishActionProposed(a.taskID, action.ID)
	}

	if skipped > 0 {
		a.logger.Info().Int("skipped", skipped).Msg("已拦断空指令与重复指令提案")
	}
	if failed > 0 {
		return written, fmt.Errorf("写入 Action 失败 %d/%d 条（详见日志）", failed, len(actions))
	}
	return written, nil
}

// knownInstructions 汇总图上全部动作的 instruction（去首尾空白后的字面去重集）。
func (a *Agent) knownInstructions(ctx context.Context) (map[string]bool, error) {
	all, err := a.graph.ListNodesByKind(ctx, a.taskID, core.KindAction)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(all))
	for _, action := range all {
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
