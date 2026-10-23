package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

func (a *Agent) planActions(ctx context.Context) error {
	a.logger.Debug().
		Str("task_id", a.taskID).
		Str("caller", "planActions").
		Msg("▶ planActions 入口")

	defer a.logger.Debug().
		Str("task_id", a.taskID).
		Str("caller", "planActions").
		Msg("◀ planActions 出口")

	a.logger.Debug().Str("task_id", a.taskID).Msg("开始规划 Action")

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

	// 4. 如果没有生成新 Action，发布收敛事件
	if len(actions) == 0 {
		a.handleNoActions(ctx)
		return nil
	}

	// 产生新方向即解除收敛挂起（下轮空手而归时重新走收敛判定）。
	a.convergedPending = false

	// 5. 将新 Action 写入探索图
	return a.writeActionsFromPlan(ctx, actions)
}

// rootObjective 返回任务的根目标节点（创建序最早——根目标由任务入口创建，
// planner 派生的次级目标晚于它）。规划产出的 Action 以根任务目标为归属。
func (a *Agent) rootObjective(ctx context.Context) (explorationgraph.Node, error) {
	objectives, err := a.graph.ListNodesByKind(ctx, a.taskID, core.KindObjective)
	if err != nil {
		return explorationgraph.Node{}, fmt.Errorf("获取 Objectives 失败: %w", err)
	}
	if len(objectives) == 0 {
		return explorationgraph.Node{}, fmt.Errorf("当前无 Objective 节点")
	}
	return objectives[0], nil
}

// writeActionsFromPlan 将 planner 返回的 action 节点连同目标归属边
// （根 objective → action）与发现滋生边（result → action，TRIGGERS）原子写入图。
// 指令级去重：与图上既有动作 instruction 完全一致的提案跳过（语义去重仍靠
// LLM 的 refuted 视野，字面复读在这里结构化拦断）。
func (a *Agent) writeActionsFromPlan(ctx context.Context, actions []explorationgraph.Node) error {
	a.logger.Info().
		Int("action_count", len(actions)).
		Msg("准备写入 Actions 到探索图")

	// 既有指令集（含本批已写入的）：字面重复提案在此拦断。
	known, err := a.knownInstructions(ctx)
	if err != nil {
		a.logger.Warn().Err(err).Msg("读取既有动作指令失败（跳过去重，直接写入）")
		known = map[string]bool{}
	}

	primaryObjective, err := a.rootObjective(ctx)
	if err != nil {
		a.logger.Error().Err(err).Msg("获取根 Objective 失败")
	}

	failed, duplicated := 0, 0
	for i, action := range actions {
		var c struct {
			Instruction string   `json:"instruction"`
			TriggeredBy []string `json:"triggered_by"`
		}
		_ = json.Unmarshal(action.Content, &c)
		if c.Instruction != "" {
			if known[c.Instruction] {
				duplicated++
				a.logger.Info().Str("instruction", c.Instruction).Msg("提案与既有动作指令重复，跳过写入")
				continue
			}
			known[c.Instruction] = true
		}

		a.logger.Info().
			Int("index", i).
			Str("action_id", action.ID).
			Msg("准备创建 Action")

		// 边集：目标归属（generates）+ 发现滋生（triggers）——后者是攻击链
		// 溯源（漏洞A 催生本动作）的图上事实，与节点原子落库。
		edges := make([]explorationgraph.Edge, 0, 1+len(c.TriggeredBy))
		if primaryObjective.ID != "" {
			edges = append(edges, explorationgraph.Edge{
				TaskID:    a.taskID,
				SrcID:     primaryObjective.ID,
				Rel:       explorationgraph.RelGenerates,
				DstID:     action.ID,
				CreatedAt: time.Now(),
			})
		}
		for _, resultID := range c.TriggeredBy {
			edges = append(edges, explorationgraph.Edge{
				TaskID:    a.taskID,
				SrcID:     resultID,
				Rel:       explorationgraph.RelTriggers,
				DstID:     action.ID,
				CreatedAt: time.Now(),
			})
		}

		var writeErr error
		if len(edges) > 0 {
			writeErr = a.graph.CreateNodeWithEdges(ctx, action, edges)
		} else {
			_, writeErr = a.graph.CreateNode(ctx, action)
		}
		if writeErr != nil {
			failed++
			a.logger.Error().
				Err(writeErr).
				Str("action_id", action.ID).
				Msg("写入 Action 失败")
			continue
		}

		a.logger.Info().
			Str("action_id", action.ID).
			Str("priority", string(action.Priority)).
			Msg("新 Action 已生成")

		// 发布 ActionProposed 事件
		a.eventBus.PublishActionProposed(a.taskID, action.ID)
	}

	if duplicated > 0 {
		a.logger.Info().Int("duplicated", duplicated).Msg("已拦断重复指令提案")
	}
	if failed > 0 {
		return fmt.Errorf("写入 Action 失败 %d/%d 条（详见日志）", failed, len(actions))
	}
	return nil
}

// knownInstructions 汇总图上全部动作的 instruction（字面去重集）。
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
		if json.Unmarshal(action.Content, &c) == nil && c.Instruction != "" {
			set[c.Instruction] = true
		}
	}
	return set, nil
}

// analyzeAndProcessResults 分析增量 Results 并处理（水位由调用方维护）。
