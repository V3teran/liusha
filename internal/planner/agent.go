// Package planner 提供规划层（Agent），负责根据探索图节点生成可执行的 Action
package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
)

// Agent 是异步规划 Agent
//
// 职责：
// - 监听 EventVerificationPassed/Refuted 事件
// - 根据探索图生成新的 Action
// - 发布 EventActionProposed 事件
type Agent struct {
	taskID        string
	agentRunID    string // 本轮认知循环的 agent_run.id（LLM 审计归属）
	graph         *explorationgraph.Store
	planner       Planner
	eventBus      bus.Bus
	logger        zerolog.Logger
	functionTools []string      // function_tools 白名单
	pollInterval  time.Duration // 轮询间隔（作为兜底）

	// analyzedResultIDs 是 AnalyzeResults 的水位：已分析过的 Result 节点不再
	// 重复送 LLM（否则任务后半程每轮轮询都全量重分析，烧 token 且灌重复结论）。
	analyzedResultIDs map[string]bool

	stopCh chan struct{}
}

// AgentConfig 配置
type AgentConfig struct {
	TaskID        string
	AgentRunID    string // 本轮认知循环的 agent_run.id（LLM 审计归属）
	Graph         *explorationgraph.Store
	Planner       Planner
	EventBus      bus.Bus
	Logger        zerolog.Logger
	PollInterval  time.Duration // 默认 10s
	FunctionTools []string      // function_tools 白名单（agent 配置；nil=全量，空=空集）
}

// NewAgent NewPlannerAgent 创建 Agent。
func NewAgent(cfg AgentConfig) *Agent {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 10 * time.Second
	}

	return &Agent{
		taskID:            cfg.TaskID,
		agentRunID:        cfg.AgentRunID,
		graph:             cfg.Graph,
		planner:           cfg.Planner,
		eventBus:          cfg.EventBus,
		logger:            cfg.Logger.With().Str("agent", "planner").Logger(),
		functionTools:     cfg.FunctionTools,
		pollInterval:      cfg.PollInterval,
		analyzedResultIDs: make(map[string]bool),
		stopCh:            make(chan struct{}),
	}
}

// Run 启动规划主循环（ctx 取消即停止）。
// 启动 Agent 主循环，监听事件并生成新的 Action
func (a *Agent) Run(ctx context.Context) error {
	// LLM 审计维度：规划调用归 task/本轮 run、角色 planner。
	ctx = llm.WithCallMeta(ctx, llm.CallMeta{TaskID: a.taskID, AgentRunID: a.agentRunID, Role: "planner"})

	a.logger.Info().Str("task_id", a.taskID).Msg("Agent 启动")

	// 订阅事件
	sub := a.eventBus.SubscribeTask(a.taskID)
	defer sub.Cancel()

	// 定期检查（兜底机制）
	ticker := time.NewTicker(a.pollInterval)
	defer ticker.Stop()

	// 启动时规划一次
	if err := a.planActions(ctx); err != nil {
		a.logger.Error().Err(err).Msg("初始规划失败")
	}

	for {
		select {
		case <-ctx.Done():
			a.logger.Info().Str("task_id", a.taskID).Msg("Agent 停止（context done）")
			return ctx.Err()

		case <-a.stopCh:
			a.logger.Info().Str("task_id", a.taskID).Msg("Agent 停止")
			return nil

		case event := <-sub.Events():
			// 处理验证结果事件
			if err := a.handleEvent(ctx, event); err != nil {
				a.logger.Error().
					Err(err).
					Str("event_type", string(event.Type)).
					Msg("事件处理失败")
			}

		case <-ticker.C:
			// 定期兜底：检查是否需要新的规划
			if err := a.planActions(ctx); err != nil {
				a.logger.Error().Err(err).Msg("定期规划失败")
			}
		}
	}
}

// handleEvent 处理事件
func (a *Agent) handleEvent(ctx context.Context, event bus.Event) error {
	switch event.Type {
	case bus.EventVerificationPassed:
		// 验证通过，可能需要新的规划
		a.logger.Info().
			Str("task_id", a.taskID).
			Msg("收到 VerificationPassed 事件，触发新规划")
		return a.planActions(ctx)

	case bus.EventVerificationRefuted:
		// 证伪，需要调整规划
		a.logger.Info().
			Str("task_id", a.taskID).
			Msg("收到 VerificationRefuted 事件，触发调整规划")
		return a.planActions(ctx)

	case bus.EventReplanRequested:
		// ✅ P2：Monitor 请求重规划
		reason := ""
		if r, ok := event.Payload["reason"].(string); ok {
			reason = r
		}
		a.logger.Info().
			Str("task_id", a.taskID).
			Str("reason", reason).
			Msg("收到 Monitor 重规划请求，强制重规划")
		return a.forcePlanActions(ctx, reason)

	default:
		// 忽略其他事件
		return nil
	}
}

// planActions 根据当前探索图生成新的 Action
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

	// 1. 分析新出现的 Results
	if err := a.analyzeNewResults(ctx); err != nil {
		// 继续执行，不中断
	}

	// 2. 检查是否已有可执行的 Action
	hasExecutable, err := a.checkExecutableActions(ctx)
	if err != nil {
		return err
	}
	if hasExecutable {
		return nil
	}

	// 3. 生成新的 Actions
	actions, err := a.generateNewActions(ctx)
	if err != nil {
		return err
	}

	// 4. 如果没有生成新 Action，发布收敛事件
	if len(actions) == 0 {
		a.handleNoActions()
		return nil
	}

	// 5. 将新 Action 写入探索图
	return a.writeActionsFromPlan(ctx, actions)
}

// writeActionsFromPlan 将 planner 返回的 action nodes 写入图（保持原有逻辑）
func (a *Agent) writeActionsFromPlan(ctx context.Context, actions []explorationgraph.Node) error {
	a.logger.Info().
		Int("action_count", len(actions)).
		Msg("准备写入 Actions 到探索图")

	// 获取当前 Objective（用于关联 Actions）
	objectives, err := a.graph.ListNodesByKind(ctx, a.taskID, core.KindObjective)
	if err != nil {
		a.logger.Error().Err(err).Msg("获取 Objectives 失败")
	}

	var primaryObjectiveID string
	if len(objectives) > 0 {
		primaryObjectiveID = objectives[0].ID
	}

	failed := 0
	for i, action := range actions {
		a.logger.Info().
			Int("index", i).
			Str("action_id", action.ID).
			Msg("准备创建 Action")

		// action 已经是完整的 Node，直接写入
		if _, err := a.graph.CreateNode(ctx, action); err != nil {
			failed++
			a.logger.Error().
				Err(err).
				Str("action_id", action.ID).
				Msg("写入 Action 失败")
			continue
		}

		// 创建 Objective → Action 边
		if primaryObjectiveID != "" {
			if err := a.graph.CreateEdge(ctx, &core.GraphEdge{
				From:      primaryObjectiveID,
				To:        action.ID,
				Relation:  string(core.RelationGenerates),
				CreatedAt: time.Now(),
			}); err != nil {
				a.logger.Error().Err(err).Msg("创建 Objective → Action 边失败")
				// 不返回错误，节点已创建
			}
		}

		a.logger.Info().
			Str("action_id", action.ID).
			Str("priority", string(action.Priority)).
			Msg("新 Action 已生成")

		// 发布 ActionProposed 事件
		a.eventBus.PublishActionProposed(a.taskID, action.ID)
	}

	if failed > 0 {
		return fmt.Errorf("写入 Action 失败 %d/%d 条（详见日志）", failed, len(actions))
	}
	return nil
}

// analyzeAndProcessResults 分析增量 Results 并处理（水位由调用方维护）。
func (a *Agent) analyzeAndProcessResults(ctx context.Context, results []explorationgraph.Node) error {
	a.logger.Info().Int("result_count", len(results)).Msg("开始分析 Results")

	if len(results) == 0 {
		return nil
	}

	// 2. 调用 Planner 分析 Results
	analysis, err := a.planner.AnalyzeResults(ctx, a.graph, a.taskID, results)
	if err != nil {
		return fmt.Errorf("analyze results: %w", err)
	}

	a.logger.Info().
		Bool("completed", analysis.Completed).
		Int("new_objectives", len(analysis.NewObjectives)).
		Int("continuation_actions", len(analysis.ContinuationActions)).
		Str("reasoning", analysis.Reasoning).
		Msg("Result 分析完成")

	// 3. 处理分析结果

	// 情况 A：当前 Objective 已完成
	if analysis.Completed {
		a.logger.Info().
			Int("evidence_count", len(analysis.Evidence)).
			Msg("当前 Objective 已完成")

		// 如果有新 Objectives，创建它们
		if len(analysis.NewObjectives) > 0 {
			return a.createNewObjectives(ctx, analysis.NewObjectives)
		}

		a.logger.Info().Msg("当前 Objective 完成，但没有新方向")
		return nil
	}

	// 情况 B：生成 ContinuationActions（在当前 Objective 下继续）
	if len(analysis.ContinuationActions) > 0 {
		a.logger.Info().Msg("在当前 Objective 下生成新 Actions")
		return a.createContinuationActions(ctx, analysis.ContinuationActions)
	}

	// 情况 C：生成新 Objectives（新方向）
	if len(analysis.NewObjectives) > 0 {
		a.logger.Info().Msg("发现新探索方向，生成新 Objectives")
		return a.createNewObjectives(ctx, analysis.NewObjectives)
	}

	// 情况 D：什么都不做
	a.logger.Info().Msg("分析完成，无需生成新节点")
	return nil
}

// createNewObjectives 创建新的 Objective 节点
func (a *Agent) createNewObjectives(ctx context.Context, objectives []NewObjective) error {
	for _, obj := range objectives {
		objID := uuid.New().String()

		content := map[string]interface{}{
			"description": obj.Description,
			"reasoning":   obj.Reasoning,
		}
		contentJSON, err := json.Marshal(content)
		if err != nil {
			a.logger.Error().Err(err).Msg("序列化 Objective 内容失败")
			continue
		}

		node := explorationgraph.Node{
			ID:         objID,
			TaskID:     a.taskID,
			Kind:       core.KindObjective,
			Content:    contentJSON,
			Priority:   obj.Priority,
			SourceType: explorationgraph.SourcePlanner,
			SourceID:   "result-analyzer",
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}

		if _, err := a.graph.CreateNode(ctx, node); err != nil {
			a.logger.Error().
				Err(err).
				Str("objective_id", objID).
				Msg("创建 Objective 节点失败")
			continue
		}

		// 创建 Result → Objective 的 TRIGGERS 边
		for _, resultID := range obj.TriggeredBy {
			if err := a.graph.CreateEdge(ctx, &core.GraphEdge{
				From:      resultID,
				To:        objID,
				Relation:  string(core.RelationTriggers),
				CreatedAt: time.Now(),
			}); err != nil {
				a.logger.Error().
					Err(err).
					Str("result_id", resultID).
					Str("objective_id", objID).
					Msg("创建 Result → Objective 边失败")
			}
		}

		a.logger.Info().
			Str("objective_id", objID).
			Str("description", obj.Description).
			Msg("新 Objective 已创建")
	}

	return nil
}

// createContinuationActions 创建新的 Action 节点（在当前 Objective 下）
func (a *Agent) createContinuationActions(ctx context.Context, actions []ContinuationAction) error {
	// 获取当前 Objective
	objectives, err := a.graph.ListNodesByKind(ctx, a.taskID, core.KindObjective)
	if err != nil {
		return fmt.Errorf("获取当前 Objective 失败: %w", err)
	}
	if len(objectives) == 0 {
		return fmt.Errorf("当前无 Objective 节点")
	}

	currentObjective := objectives[0] // 主 Objective（创建序第一个，与 primaryObjectiveID 判定一致）

	for _, action := range actions {
		actionID := uuid.New().String()

		content := map[string]interface{}{
			"instruction": action.Instruction,
			"reasoning":   action.Reasoning,
		}
		contentJSON, err := json.Marshal(content)
		if err != nil {
			a.logger.Error().Err(err).Msg("序列化 Action 内容失败")
			continue
		}

		openState := explorationgraph.StateOpen
		node := explorationgraph.Node{
			ID:         actionID,
			TaskID:     a.taskID,
			Kind:       core.KindAction,
			Content:    contentJSON,
			Priority:   action.Priority,
			State:      &openState,
			SourceType: explorationgraph.SourcePlanner,
			SourceID:   "result-analyzer",
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}

		if _, err := a.graph.CreateNode(ctx, node); err != nil {
			a.logger.Error().
				Err(err).
				Str("action_id", actionID).
				Msg("创建 Action 节点失败")
			continue
		}

		// 创建 Objective → Action 的 GENERATES 边
		if err := a.graph.CreateEdge(ctx, &core.GraphEdge{
			From:      currentObjective.ID,
			To:        actionID,
			Relation:  string(core.RelationGenerates),
			CreatedAt: time.Now(),
		}); err != nil {
			a.logger.Error().
				Err(err).
				Str("objective_id", currentObjective.ID).
				Str("action_id", actionID).
				Msg("创建 Objective → Action 边失败")
		}

		// 创建 Result → Action 的 TRIGGERS 边
		for _, resultID := range action.TriggeredBy {
			if err := a.graph.CreateEdge(ctx, &core.GraphEdge{
				From:      resultID,
				To:        actionID,
				Relation:  string(core.RelationTriggers),
				CreatedAt: time.Now(),
			}); err != nil {
				a.logger.Error().
					Err(err).
					Str("result_id", resultID).
					Str("action_id", actionID).
					Msg("创建 Result → Action 边失败")
			}
		}

		a.logger.Info().
			Str("action_id", actionID).
			Str("instruction", action.Instruction).
			Msg("新 Action 已创建")

		// 发布 ActionProposed 事件
		a.eventBus.PublishActionProposed(a.taskID, actionID)
	}

	return nil
}

// Planner 规划接口（由 planner.New 提供）
type Planner interface {
	Plan(ctx context.Context, graph *explorationgraph.Store, taskID string, functionTools []string) ([]explorationgraph.Node, error)
	AnalyzeResults(ctx context.Context, graph *explorationgraph.Store, taskID string, results []explorationgraph.Node) (*ResultAnalysis, error)
}

// ResultAnalysis 是对 Results 的分析结果
type ResultAnalysis struct {
	Completed           bool                 // 当前 Objective 是否已完成
	Evidence            []string             // 支撑完成判断的证据（Result IDs）
	NewObjectives       []NewObjective       // 发现的新探索方向
	ContinuationActions []ContinuationAction // 当前方向的延续
	Reasoning           string               // 判断理由
}

// NewObjective 表示从 Result 中提取的新探索目标
type NewObjective struct {
	Description string        // 目标描述
	Priority    core.Priority // 优先级
	TriggeredBy []string      // 触发此目标的 Result ID 列表
	Reasoning   string        // 为什么需要这个目标
}

// ContinuationAction 表示在当前 Objective 下继续探索的新动作
type ContinuationAction struct {
	Instruction string        // 动作指令
	Priority    core.Priority // 优先级
	TriggeredBy []string      // 触发此动作的 Result ID 列表
	Reasoning   string        // 为什么需要这个动作
}

// forcePlanActions 强制重规划（跳过 executable action 检查）
// 用于 Monitor 请求重规划的场景（探索停滞、资源耗尽等）
func (a *Agent) forcePlanActions(ctx context.Context, monitorReason string) error {
	a.logger.Info().
		Str("task_id", a.taskID).
		Str("monitor_reason", monitorReason).
		Msg("强制重规划（Monitor 触发）")

	// 直接调用 Plan，不检查 executable actions
	actions, err := a.planner.Plan(ctx, a.graph, a.taskID, a.functionTools)
	if err != nil {
		return fmt.Errorf("forced plan: %w", err)
	}

	a.logger.Info().
		Int("action_count", len(actions)).
		Str("monitor_reason", monitorReason).
		Msg("强制重规划完成")

	if len(actions) == 0 {
		a.logger.Info().Msg("强制重规划未生成新 Action（任务收敛）")
		a.eventBus.PublishTaskConverged(a.taskID, "planner: no more actions after forced replan")
		return nil
	}

	// 写入探索图（复用现有逻辑）
	return a.writeActionsFromPlan(ctx, actions)
}
