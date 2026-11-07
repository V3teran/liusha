package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/reasoning"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/repro"
)

// ActionData 是从 action.Content 解析出的数据
type ActionData struct {
	Type        string `json:"type"`
	Instruction string `json:"instruction"`
	Complexity  string `json:"complexity"`
	Host        string `json:"host"`
}

// parseActionContent 从 action 节点中解析内容
func (e *Engine) parseActionContent(action explorationgraph.Node) (*ActionData, error) {
	var data ActionData
	if err := json.Unmarshal(action.Content, &data); err != nil {
		return nil, fmt.Errorf("解析 Action 失败: %w", err)
	}
	return &data, nil
}

// prepareReActTools 准备并过滤 ReAct 工具列表
func (e *Engine) prepareReActTools() []registry.Tool {
	var tools []registry.Tool
	for _, t := range e.registry.WrappedTools() {
		if registry.Allows(e.functionTools, t.Name()) {
			tools = append(tools, t)
		}
	}
	return tools
}

// buildReActConfigWithMonitoring 构建带监控回调的 ReAct 配置
func (e *Engine) buildReActConfigWithMonitoring(
	ctx context.Context,
	action explorationgraph.Node,
	actionData *ActionData,
	tools []registry.Tool,
) (*runtime.ReActConfig, error) {
	// 构建执行目标
	objective := fmt.Sprintf("执行以下操作：%s\n\n类型：%s\n目标：%s",
		actionData.Instruction,
		actionData.Type,
		actionData.Host)

	// 构建系统提示
	systemPrompt := e.buildSystemPrompt(actionData.Type, actionData.Complexity, tools)

	// 获取 LLM Provider
	provider, err := e.router.For(ctx, e.complexity)
	if err != nil {
		return nil, fmt.Errorf("获取 LLM provider 失败: %w", err)
	}

	// 构建中间件链：防止工具循环、目标偏离、无进展
	middlewareChain := runtime.NewMiddlewareChain(
		runtime.NewLoopDetectionMiddleware(10, 8),        // 连续 10 次相同工具 / 8 次相似思考
		runtime.NewProgressTrackingMiddleware(15),        // 15 轮无新输出终止（连续失败止损由 runtime 内置，阈值随装配下发）
		runtime.NewGoalAlignmentMiddleware(objective, 6), // 6 次偏离目标
	)

	// 构建配置，包含 monitor kill 检查
	var killed atomic.Bool
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
		MaxTokens:                  4000,
		MessageModifierChain:       runtime.NewCompactionChain(e.contextBudget(ctx), "executor", e.distiller),
		ToolOutputTailBytes:        e.toolOutputTailBytes,
		MiddlewareChain:            middlewareChain,
		// Executor 的结构化输出格式
		OutputFormatPrompt: "你刚才完成了测试任务，现在需要输出结构化的观察结果。",
		OutputFormatSchema: `请输出以下JSON格式：

` + "```json\n" + `{
  "status": "completed",
  "summary": "简要总结你完成的工作",
  "observations": [
    {
      "category": "hypothesis",
      "statement": "主张陈述：什么位置、什么条件、出现什么可复现现象",
      "reasoning": "你为什么得出这个结论",
      "confidence": "low/medium/high",
      "severity": "low/medium/high/critical",
      "impact": "危害：主张成立后会造成什么后果、影响面多大（一段话）",
      "remediation": "处置建议：主张指出的问题如何解决（一段话，面向该领域处理者）",
      "repro": {
        "domain": "web",
        "recipe": {
          "request": {
            "method": "GET",
            "url": "http://example.com/target",
            "headers": {"Cookie": "session=abc"},
            "body": ""
          }
        },
        "assert": {
          "body_contains": ["主张成立的独有特征子串"],
          "status_code": 200
        }
      }
    },
    {
      "category": "entity",
      "statement": "对象陈述：环境中存在什么对象、有什么属性",
      "reasoning": "依据是什么（来自哪个实测输出）",
      "confidence": "low/medium/high"
    }
  ]
}
` + "```\n\n" + `说明：
- 每条 observation 必须带 category，七个信息域（领域中立词汇，选最贴切的一个）：
  * hypothesis：待裁决的判断——断言存在可复现的现象，必须带完整 repro；只有这类会被独立验证并可能成为正式发现
  * entity：环境中存在的单个对象及其属性（某主机/端点/服务/页面/文件/配置项存在，及其关键属性）
  * access：触达与运用对象的途径（入口、认证方式、授权范围、可用凭据、会话规则）
  * structure：对象之间的关系与布局（站点地图、网络拓扑、组件依赖、目录层级）
  * behavior：环境受激后的反应与内在机制（输入如何被处理、状态如何迁移、过滤/转义规律）
  * issue：已观察到的异常与障碍（报错、崩溃、配置缺陷、阻塞推进的坑）——已证实、可直接判定的坏事实
  * note：以上都不合适的杂项（最后手段——先找具体域，写不出归属再用 note）
- entity 与 structure 的分界：单个对象的存在与属性记 entity；对象之间怎么连接、怎么组织记 structure
- issue 与 hypothesis 的分界：看一眼就能判定的坏事实记 issue；需要复现证明才能下结论的判断记 hypothesis（必须带 repro）
- 不要把纯过程性记录（"访问了某页"、"请求返回 200"）写成 observation
- 没有值得记录的内容时，observations 可以是空数组 []
- hypothesis 必须包含完整的 repro 复现步骤；只推测未实测的东西不要标 hypothesis
- 重要：domain="web"时，assert必须包含至少一个机器可验证的字段：
  * body_contains: 响应体必须包含的字符串数组（推荐）
  * status_code: 期望的HTTP状态码（数字）
  * header_contains: 响应头必须包含的字段，格式为对象 {"Header-Name": "substring"}，不是数组
  * body_not_contains: 响应体必须不含的字符串数组（主张成立的反向特征）
- domain="generic"（非 HTTP 复现）时，assert 必填且形如 {"description": "执行 steps 后观察到什么才算坐实"}——判据缺失的主张无法被裁决
- 不要只写description，必须提供具体的断言条件
- hypothesis 坐实后会成为交付物给人看：impact/host/target/remediation 都要写全——
  impact 说清后果与影响面（信息泄露/越权/执行任意代码等，落实到本主张的具体对象）；
  remediation 要定位到根因给出可执行的处理，不写空话`,
		// kill 止血：迭代回调发现 action 已被 monitor kill（aborted）→ 置位，
		// 提前终止条件在下一轮立即结束 ReAct——不再空跑剩余迭代。
		EarlyStopCondition: func(string) bool { return killed.Load() },
		// AbortCheck 把止血粒度从迭代边界压到当前步骤：callLLM 与每个
		// 工具执行前即检查（长生成/长工具期间的 kill 即刻生效）。
		AbortCheck: killed.Load,
		OnIteration: func(iteration int, status runtime.IterationStatus) {
			e.logger.Debug().
				Str("action_id", action.ID).
				Int("iteration", iteration).
				Str("status", string(status)).
				Msg("ReAct 迭代")

			// 每轮迭代检查 action 状态（Monitor kill_action 中断机制）
			if e.graph != nil {
				node, err := e.graph.GetNode(ctx, action.ID)
				if err == nil && node.State != nil && *node.State == explorationgraph.StateAborted {
					reason := "unknown"
					if node.BlockedReason != nil {
						reason = *node.BlockedReason
					}
					e.logger.Warn().
						Str("action_id", action.ID).
						Str("reason", reason).
						Msg("检测到 action 已被 monitor kill，中断执行")
					killed.Store(true)
				}
			}
		},
	}

	return config, nil
}

// checkActionAborted 检查 action 是否被 monitor kill
func (e *Engine) checkActionAborted(ctx context.Context, actionID string) error {
	if e.graph == nil {
		return nil
	}

	node, err := e.graph.GetNode(ctx, actionID)
	if err != nil {
		return nil // 忽略查询错误
	}

	if node.State != nil && *node.State == explorationgraph.StateAborted {
		reason := "unknown"
		if node.BlockedReason != nil {
			reason = *node.BlockedReason
		}
		e.logger.Warn().
			Str("action_id", actionID).
			Str("reason", reason).
			Msg("action 已被 monitor kill，提前返回（图层 CAS 已保护状态）")
		return fmt.Errorf("action killed by monitor: %s", reason)
	}

	return nil
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

// executeReActRuntime 执行 ReAct runtime，失败时降级提取假设
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
	if err == nil && e.thoughts != nil {
		// 终态叙述在解析边界完成：执行协议转人读摘要（谁解析谁叙述，原文由
		// 审计面保存）。仅当终稿确为 JSON 且解析成功才叙述——ParseOutput 的
		// 兜底策略永不报错，散文终稿若被兜底摘要覆盖反而是信息损失。
		round := reasoning.RoundOf(result)
		if output, perr := ParseOutput(result); perr == nil && terminalIsJSON(result.FinalAnswer) {
			round.Summary = outputNarrative(output)
		}
		e.thoughts.Done(round)
	}
	if err != nil {
		// ReAct 失败：尝试降级策略——强制提取假设
		e.logger.Warn().
			Err(err).
			Str("action_id", actionID).
			Msg(" ReAct 执行失败，进入降级模式：强制提取假设")

		fallbackResult, fallbackErr := e.fallbackExtractHypothesis(ctx, config, actionID)
		if fallbackErr != nil {
			// 降级也失败：返回原始错误
			return nil, fmt.Errorf("ReAct 执行失败: %w", err)
		}

		e.logger.Info().
			Str("action_id", actionID).
			Msg("降级模式成功提取假设")
		if e.thoughts != nil {
			e.thoughts.Done(reasoning.RoundOf(fallbackResult))
		}
		return fallbackResult, nil
	}

	e.logger.Info().
		Str("action_id", actionID).
		Int("iterations", result.Iterations).
		Str("status", string(result.Status)).
		Msg("ReAct 执行完成")

	return result, nil
}

// fallbackExtractHypothesis 是 ReAct 失败后的降级策略：
// 通过单轮 LLM 调用，基于任务目标推导仍值得独立验证的假设——执行失败
// 不等于一无所获，能给出复现步骤的推测交给裁决门去实测。角色与领域
// 语义由章程承载（SystemPrompt 已含），这里只教降级任务本身。
func (e *Engine) fallbackExtractHypothesis(
	ctx context.Context,
	config *runtime.ReActConfig,
	_ string,
) (*runtime.ReActResult, error) {
	// 1. 构建降级 prompt：以任务目标为据推导假设（不是 SystemPrompt——
	// 角色章程已在 provider 侧生效，这里喂任务本体）
	fallbackPrompt := fmt.Sprintf(`
以下 Action 执行失败，但任务目标仍可能推导出值得独立验证的假设。

**任务目标**：
%s

**要求**：
1. 分析任务目标，推导 2-3 个最可能成立的待验证假设（必须能给出复现步骤，纯猜测不要）
2. 每个假设必须包含：
   - statement: 主张陈述（简洁明确：什么位置、什么条件、什么现象）
   - repro: 复现步骤（JSON 数组，每步一个字符串）
   - severity: 影响等级（critical/high/medium/low）

**输出格式**（严格 JSON）：
{
  "status": "completed_with_hypothesis",
  "summary": "执行失败但推导出以下假设",
  "observations": [
    {
      "category": "hypothesis",
      "statement": "X 输入在响应中原样出现，未经过任何处理",
      "repro": ["构造带唯一标记的输入访问目标", "检查响应中标记是否原样保留"],
      "severity": "high"
    }
  ]
}
`, config.Objective)

	// 2. 调用 LLM（使用 config 的 LLMProvider）
	req := llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: fallbackPrompt},
		},
	}

	result, err := config.LLMProvider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("降级 LLM 调用失败: %w", err)
	}

	// 3. 构造伪 ReActResult（模拟成功完成）
	return &runtime.ReActResult{
		Status:      runtime.ReActStatusSuccess,
		Iterations:  1, // 降级只有一轮
		FinalAnswer: result.Content,
	}, nil
}

// processOutput 处理 Executor 输出，创建 Observation 节点
func (e *Engine) processOutput(
	ctx context.Context,
	action explorationgraph.Node,
	result *runtime.ReActResult,
) ([]evaluator.Attempt, error) {
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

	// 收集 ExecutionTrace（治本方案的核心）
	trace := e.collectTrace(ctx, action)

	// 为每个观察结果创建 observation 节点
	var attempts []evaluator.Attempt

	e.logger.Debug().
		Str("action_id", action.ID).
		Int("total_observations", len(output.Observations)).
		Msg("开始处理 observations")

	anyObserved := false
	for _, obs := range output.Observations {
		attempt, observed := e.harvestObservation(ctx, action, obs, trace)
		if observed {
			anyObserved = true
		}
		if attempt != nil {
			attempts = append(attempts, *attempt)
		}
	}

	// 图完整性不变式：动作完结必须给图留下可见输出。LLM 把事实写进黑板、
	// 或观察列表为空时（实测 82% 的 done 动作无任何 observation 节点），
	// 以执行总结落一条 state 观察——「图 = 共享状态面」不因输出路由偏好破。
	if !anyObserved {
		e.ensureSummaryObservation(ctx, action, output)
	}

	e.logger.Info().
		Str("action_id", action.ID).
		Int("attempts_count", len(attempts)).
		Msg("Executor 输出处理完成")

	return attempts, nil
}

// ensureSummaryObservation 为零观察完结的动作补一条执行总结观察（state 类，
// 写入即终态）——规划者在图上至少能看到该动作的结论，而不是只剩指令回显。
func (e *Engine) ensureSummaryObservation(ctx context.Context, action explorationgraph.Node, output *Output) {
	summary := strings.TrimSpace(output.Summary)
	if summary == "" {
		summary = "执行完结（无总结产出）"
	}
	obs := Observation{
		Category:  CategoryNote,
		Statement: "执行总结：" + summary,
	}
	if _, observed := e.harvestObservation(ctx, action, obs, ExecutionTrace{}); observed {
		e.logger.Info().
			Str("action_id", action.ID).
			Msg("动作零观察完结，已补执行总结观察（图完整性不变式）")
	}
}

// collectTrace 从 agent_traffic 读取该 action 的完整 HTTP 轨迹；未配置收集器
// 或收集失败时返回空 trace（不阻塞观察收割）。
func (e *Engine) collectTrace(ctx context.Context, action explorationgraph.Node) ExecutionTrace {
	empty := ExecutionTrace{
		Traffic: []TrafficRecord{},
		Session: SessionState{Cookies: make(map[string]string)},
		Meta:    TraceMeta{},
	}
	if e.traceCollector == nil {
		e.logger.Warn().
			Str("action_id", action.ID).
			Msg("TraceCollector 未配置，无法收集 trace")
		return empty
	}
	trace, err := e.traceCollector.CollectTrace(ctx, action.TaskID, action.ID)
	if err != nil {
		e.logger.Warn().
			Err(err).
			Str("action_id", action.ID).
			Msg("收集 ExecutionTrace 失败，使用空 trace")
		return empty
	}
	e.logger.Info().
		Str("action_id", action.ID).
		Int("traffic_count", trace.Meta.TotalCount).
		Msg("成功收集 ExecutionTrace")
	return trace
}

// harvestObservation 收割单个观察：归一化配方 → 落 observation 节点 → 转 Attempt。
// 任一步失败返回 nil（单条观察失败不中止同 action 的其余观察）。
func (e *Engine) harvestObservation(
	ctx context.Context,
	action explorationgraph.Node,
	obs Observation,
	trace ExecutionTrace,
) (attempt *evaluator.Attempt, observed bool) {
	// 类别边界归一化：模型输出是外部输入，三种失真三种处置——
	//   漏写（空值）：按形状推断，有配方即主张（配方是主张意图的最强证据），
	//     无配方落 note；观察本体永远比分类标签值钱，不因漏标签丢弃。
	//   越界（契约外字面值）：落 note 兜底并告警——显式乱写不可信，不做
	//     猜测性归类。
	//   自相矛盾（hypothesis 无配方）：降级 note——无从裁决的主张退为杂记。
	if obs.Category == "" {
		if len(obs.Repro) > 0 {
			obs.Category = CategoryHypothesis
		} else {
			obs.Category = CategoryNote
		}
	}
	if obs.Category == CategoryHypothesis && len(obs.Repro) == 0 {
		e.logger.Warn().
			Str("action_id", action.ID).
			Str("statement", obs.Statement).
			Msg("hypothesis 缺少 repro，降级为 note 落图（无配方的主张无从裁决）")
		obs.Category = CategoryNote
	}
	switch obs.Category {
	case CategoryEntity, CategoryAccess, CategoryStructure, CategoryBehavior,
		CategoryNote, CategoryIssue, CategoryHypothesis:
		// 契约内类别，原样进入落图
	default:
		e.logger.Warn().
			Str("action_id", action.ID).
			Str("raw_category", string(obs.Category)).
			Str("statement", obs.Statement).
			Msg("观察类别越界，落 note 兜底")
		obs.Category = CategoryNote
	}

	// 附加 ExecutionTrace：裁决官看到的是完整执行轨迹，而不是 executor
	// 转述的不可靠结论
	obs.ExecutionTrace = &trace

	// 配方归一化在此做一次、节点与 Attempt 共用同一结果——归一化若藏在
	// 节点构造内部，矫正结果不会传回 obs，事件路径的 Attempt 会携带原始
	// 破碎形状（数组信封/标量断言）直通复现门被确定性杀死，而节点上存的
	// 却是修复后的配方：同一观察两条消费路径各说各话（e2e 实测 4 条真漏洞
	// 假设因此冤死）。矫正失败保留原始配方，由门侧以 invalid_recipe 终局
	// ——归一化不是万能的，但必须对所有消费路径一致。
	if normalized, note, nErr := repro.NormalizeReproEnvelope(obs.Repro); nErr == nil {
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

	node, err := e.createObservationNode(ctx, action.TaskID, action.ID, obs)
	if err != nil {
		e.logger.Error().
			Err(err).
			Str("action_id", action.ID).
			Msg("创建 observation 节点失败")
		return nil, false
	}
	observed = true
	e.logger.Info().
		Str("action_id", action.ID).
		Str("observation_id", node.ID).
		Str("statement", obs.Statement).
		Str("category", string(obs.Category)).
		Msg("创建 observation 节点成功")

	// 只有假设进裁决管道（Attempt → 复现门）；事实是终态，无 Attempt、
	// 无事件、无晋升——planner 直接从图上读。
	if obs.Category != CategoryHypothesis {
		return nil, observed
	}

	converted, err := e.observationToAttempt(action.TaskID, node, obs)
	if err != nil {
		e.logger.Error().
			Err(err).
			Str("observation_id", node.ID).
			Msg("转换 observation 为 Attempt 失败")
		return nil, observed
	}
	return &converted, observed
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
