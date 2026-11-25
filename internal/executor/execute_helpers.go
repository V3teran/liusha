package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/allowlist"
	executorcontract "github.com/V3teran/liusha/internal/executor/contract"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/reasoning"
	"github.com/V3teran/liusha/internal/registry"
)

// ActionContent 是从 action.Content 解析出的数据
type ActionContent struct {
	Instruction string `json:"instruction"`
}

// parseActionContent 从 action 节点中解析内容
func (e *Engine) parseActionContent(action explorationgraph.Node) (*ActionContent, error) {
	var data ActionContent
	if err := json.Unmarshal(action.Content, &data); err != nil {
		return nil, fmt.Errorf("解析 Action 失败: %w", err)
	}
	return &data, nil
}

// actionObjective 构建一个动作的执行输入：只陈述事实——指令、来源、复杂度与任务目标
// 原文。怎样对待这些事实（用户指派不扩大范围、按复杂度把握投入）由章程说明。
//
// 任务目标逐字附上（取自图上的根目标，调整目标后即时生效）：执行者曾凭记忆转录目标里的
// 入口地址而打空整轮，逐字原文比任何摘要都可靠。
func actionObjective(action explorationgraph.Node, content *ActionContent, goal string) string {
	var b strings.Builder
	b.WriteString("## 动作指令\n" + content.Instruction + "\n")
	if action.SourceType == explorationgraph.SourceUser {
		b.WriteString("\n来源：用户指派\n")
	}
	if action.Complexity != nil {
		b.WriteString("\n复杂度：" + string(*action.Complexity) + "\n")
	}
	if goal != "" {
		b.WriteString("\n## 任务目标（用户原文）\n" + goal + "\n")
	}
	return b.String()
}

// prepareReActTools 准备并过滤 ReAct 工具列表
func (e *Engine) prepareReActTools() []registry.Tool {
	var tools []registry.Tool
	for _, t := range e.registry.WrappedTools() {
		if allowlist.Allows(e.functionTools, t.Name()) {
			tools = append(tools, t)
		}
	}
	return tools
}

// buildReActConfigWithMonitoring 构建带监控回调的 ReAct 配置
func (e *Engine) buildReActConfigWithMonitoring(
	ctx context.Context,
	action explorationgraph.Node,
	actionContent *ActionContent,
	goal string,
	tools []registry.Tool,
	own *ownership,
) (*runtime.ReActConfig, error) {
	objective := actionObjective(action, actionContent, goal)
	systemPrompt := e.buildSystemPrompt(tools)

	// 获取 LLM Provider
	provider, err := e.router.For(ctx, e.complexity)
	if err != nil {
		return nil, fmt.Errorf("获取 LLM provider 失败: %w", err)
	}

	// 构建中间件链：防止工具循环、目标偏离、无进展
	middlewareChain := runtime.NewMiddlewareChain(
		runtime.NewLoopDetectionMiddleware(10, 8),                        // 连续 10 次相同工具 / 8 次相似思考
		runtime.NewProgressTrackingMiddleware(15),                        // 15 轮无新输出终止（连续失败止损由 runtime 内置，阈值随装配下发）
		runtime.NewGoalAlignmentMiddleware(actionContent.Instruction, 6), // 6 次偏离动作指令
	)

	// 构建配置，包含收回检查（由 watchOwnership 后台侦测置位）
	// 思考流（nil=不流式）：方法值不能从 nil 接口取，先守卫。
	var onThoughtDelta, onThinkingDelta func(string)
	if e.thoughts != nil {
		onThoughtDelta = e.thoughts.Delta
		onThinkingDelta = e.thoughts.ThinkingDelta
	}
	config := &runtime.ReActConfig{
		Objective:                  objective,
		SystemPrompt:               systemPrompt,
		LLMProvider:                provider,
		MaxIterations:              e.maxIt,
		MaxConsecutiveToolFailures: 10, // 连续工具失败止损阈值（单机制：runtime 内置，执行链路容忍中档）
		OnThoughtDelta:             onThoughtDelta,
		OnThinkingDelta:            onThinkingDelta,
		Temperature:                0.7,
		MessageModifierChain:       runtime.NewCompactionChain(e.contextBudget(ctx), "executor", e.distiller, e.compactionTimeouts),
		ToolOutputTailBytes:        e.toolOutputTailBytes,
		MiddlewareChain:            middlewareChain,
		// Executor 的结构化输出格式
		OutputFormatPrompt: "你刚才完成了动作的执行，现在需要按输出契约输出终稿。",
		OutputFormatSchema: executorcontract.Text, // 强制总结时重申输出契约（唯一一份）
		// 收回止血：在途步骤由 action 级 context 取消即刻中断；以下两道闸门让
		// 已排队的后续步骤（下一轮迭代 / 同一轮剩余工具）不再启动。
		EarlyStopCondition: func(string) bool { return own.Lost() },
		AbortCheck:         own.Lost,
		OnIteration: func(iteration int, status runtime.IterationStatus) {
			e.activity.Touch(action.ID) // 一步推理完成即一次进展
			e.logger.Debug().
				Str("action_id", action.ID).
				Int("iteration", iteration).
				Str("status", string(status)).
				Msg("ReAct 迭代")
		},
	}

	return config, nil
}

// confirmHeld 核对本 run 仍持有该动作；已被收回返回 ErrActionRevoked。读图失败不阻断
// 执行（侦测尽力而为），但必须留痕。
func (e *Engine) confirmHeld(ctx context.Context, actionID string) error {
	if e.graph == nil {
		return nil
	}
	node, err := e.graph.GetNode(ctx, actionID)
	if err != nil {
		e.logger.Warn().Err(err).Str("action_id", actionID).Msg("读取动作状态失败，跳过本次持有核对")
		return nil
	}
	return revocation(node, e.agentRunID)
}

// registerReActTools 注册工具到 ReAct runtime
func (e *Engine) registerReActTools(
	reactRuntime runtime.ReActRuntime,
	tools []registry.Tool,
	actionID string,
) error {
	e.logger.Info().
		Int("tool_count", len(tools)).
		Str("action_id", actionID).
		Msg("🔧 开始注册工具到 ReAct runtime")

	if len(tools) == 0 {
		e.logger.Error().
			Str("action_id", actionID).
			Msg("严重错误：没有可用工具！Executor 无法执行任何操作")
		// 仍然继续执行，但会失败
	}

	for _, tool := range tools {
		e.logger.Debug().
			Str("tool_name", tool.Name()).
			Str("action_id", actionID).
			Msg("注册工具")

		if err := reactRuntime.RegisterTool(tool); err != nil {
			return fmt.Errorf("注册工具 %s 失败: %w", tool.Name(), err)
		}
	}

	return nil
}

// executeReActRuntime 执行 ReAct runtime。失败即如实返回错误，动作按失败结束：没有
// 工具结果就没有可报告的观察，不凭任务目标推测假设充数。
func (e *Engine) executeReActRuntime(
	ctx context.Context,
	reactRuntime runtime.ReActRuntime,
	config *runtime.ReActConfig,
	actionID string,
) (*runtime.ReActResult, error) {
	e.logger.Info().
		Str("action_id", actionID).
		Msg("开始执行 ReAct")

	result, err := reactRuntime.Run(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("ReAct 执行失败: %w", err)
	}
	if e.thoughts != nil {
		// 终态叙述在解析边界完成：执行协议转人读摘要（谁解析谁叙述，原文由
		// 审计面保存）。仅当终稿确为 JSON 且解析成功才叙述——ParseOutput 的
		// 兜底策略永不报错，散文终稿若被兜底摘要覆盖反而是信息损失。
		round := reasoning.RoundOf(result)
		if output, perr := ParseOutput(result); perr == nil && terminalIsJSON(result.FinalAnswer) {
			round.Summary = outputNarrative(output)
		}
		e.thoughts.Done(ctx, round)
	}

	e.logger.Info().
		Str("action_id", actionID).
		Int("iterations", result.Iterations).
		Str("status", string(result.Status)).
		Msg("ReAct 执行完成")

	return result, nil
}

// processOutput 解析 Executor 终稿并把观察落图。假设写入即待验证，评估者从图上取，
// 执行侧不另构造验证载荷。
func (e *Engine) processOutput(
	ctx context.Context,
	action explorationgraph.Node,
	result *runtime.ReActResult,
) error {
	// 解析输出
	output, err := ParseOutput(result)
	if err != nil {
		e.logger.Warn().
			Err(err).
			Str("action_id", action.ID).
			Msg("解析 Executor 输出失败，使用空输出")

		output = &Output{
			Status:       "completed",
			Summary:      "执行完成，但输出格式解析失败",
			Observations: []Observation{},
		}
	}

	e.logger.Info().
		Str("action_id", action.ID).
		Str("status", output.Status).
		Str("summary", output.Summary).
		Int("observations_count", len(output.Observations)).
		Msg("解析 Executor 输出")

	anyObserved := false
	for _, obs := range output.Observations {
		if e.harvestObservation(ctx, action, obs) != nil {
			anyObserved = true
		}
	}

	// 图完整性不变式：动作完结必须给图留下可见输出。终稿没有任何观察落图时
	// （实测 82% 的 completed 动作无任何 observation 节点），
	// 以执行总结落一条 state 观察——「图 = 共享状态面」不因输出路由偏好破。
	if !anyObserved {
		e.ensureSummaryObservation(ctx, action, output)
	}

	return nil
}

// ensureSummaryObservation 为零观察完结的动作补一条执行总结观察（state 类，
// 写入即终态）——规划者在图上至少能看到该动作的结论，而不是只剩指令回显。
func (e *Engine) ensureSummaryObservation(ctx context.Context, action explorationgraph.Node, output *Output) {
	summary := strings.TrimSpace(output.Summary)
	if summary == "" {
		summary = "执行完结（无总结产出）"
	}
	obs := Observation{
		Category:  explorationgraph.CategoryNote,
		Statement: "执行总结：" + summary,
	}
	if e.harvestObservation(ctx, action, obs) != nil {
		e.logger.Info().
			Str("action_id", action.ID).
			Msg("动作零观察完结，已补执行总结观察（图完整性不变式）")
	}
}

// harvestObservation 收割单个观察：类别归一 → 配方归一 → 落 observation 节点，返回落图
// 的节点。失败返回 nil（单条观察失败不中止同 action 的其余观察）。
func (e *Engine) harvestObservation(
	ctx context.Context,
	action explorationgraph.Node,
	obs Observation,
) *explorationgraph.Node {
	// 类别边界归一化：模型输出是外部输入，三种失真三种处置——
	//   漏写（空值）：按形状推断，有配方即主张（配方是主张意图的最强证据），
	//     无配方落 note；观察本体永远比分类标签值钱，不因漏标签丢弃。
	//   越界（契约外字面值）：落 note 兜底并告警——显式乱写不可信，不做
	//     猜测性归类。
	//   自相矛盾（hypothesis 无配方）：降级 note——无从验证的主张退为杂记。
	if obs.Category == "" {
		if len(obs.Repro) > 0 {
			obs.Category = explorationgraph.CategoryHypothesis
		} else {
			obs.Category = explorationgraph.CategoryNote
		}
	}
	if obs.Category == explorationgraph.CategoryHypothesis && len(obs.Repro) == 0 {
		e.logger.Warn().
			Str("action_id", action.ID).
			Str("statement", obs.Statement).
			Msg("hypothesis 缺少 repro，降级为 note 落图（无配方的主张无从验证）")
		obs.Category = explorationgraph.CategoryNote
	}
	switch obs.Category {
	case explorationgraph.CategoryEntity, explorationgraph.CategoryAccess, explorationgraph.CategoryStructure, explorationgraph.CategoryBehavior,
		explorationgraph.CategoryNote, explorationgraph.CategoryHypothesis:
		// 契约内类别，原样进入落图
	default:
		e.logger.Warn().
			Str("action_id", action.ID).
			Str("raw_category", string(obs.Category)).
			Str("statement", obs.Statement).
			Msg("观察类别越界，落 note 兜底")
		obs.Category = explorationgraph.CategoryNote
	}

	// 配方只在落图时归一化这一次：节点存的就是矫正后的信封，评估者只从节点读配方。
	// 矫正失败保留原始配方，由复现门以 invalid_recipe 终局。只有 hypothesis 带配方——
	// 事实即便被模型误填 repro 也不归一化（createObservationNode 会按类别丢弃它）。
	if obs.Category == explorationgraph.CategoryHypothesis && len(obs.Repro) > 0 {
		if normalized, note, nErr := e.repro.Normalize(obs.Repro); nErr == nil {
			obs.Repro = normalized
			if note != "" {
				e.logger.Info().
					Str("action_id", action.ID).
					Msg("repro 形状已矫正：" + note)
			}
		} else {
			e.logger.Warn().
				Str("action_id", action.ID).
				Err(nErr).
				Msg("repro 归一化失败，保留原始配方（门侧将以 invalid_recipe 终局）")
		}
	}

	node, err := e.createObservationNode(ctx, action.TaskID, action.ID, obs)
	if err != nil {
		logx.ErrorUnlessCanceled(e.logger, err).
			Err(err).
			Str("action_id", action.ID).
			Msg("创建 observation 节点失败")
		return nil
	}
	e.logger.Info().
		Str("action_id", action.ID).
		Str("observation_id", node.ID).
		Str("statement", obs.Statement).
		Str("category", string(obs.Category)).
		Msg("创建 observation 节点成功")
	return node
}

// terminalIsJSON 判断 ReAct 终稿是否 JSON 形态（对象/数组起收）——执行协议
// 叙述的守卫：散文终稿不经协议摘要覆盖。模型常把协议包在 markdown 围栏里
// （```json … ```），剥除后再判定。
func terminalIsJSON(text string) bool {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "```") {
		if _, rest, ok := strings.Cut(t, "\n"); ok {
			t = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "```"))
		}
	}
	return (strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}")) ||
		(strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]"))
}

// outputNarrative 把执行协议转成时间线人读叙述：状态 + 观察数 + 总结。
// 协议原文随 Round.Raw 折叠可查——叙述只在此处发生一次，前端不做协议解释。
func outputNarrative(o *Output) string {
	status := "执行完成"
	if o.Status == "failed" {
		status = "执行失败"
	}
	head := fmt.Sprintf("%s · %d 条观察", status, len(o.Observations))
	if o.Summary == "" {
		return head
	}
	return head + "\n\n" + o.Summary
}
