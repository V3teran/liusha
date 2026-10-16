package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/tools"
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
		runtime.NewProgressTrackingMiddleware(15, 10),    // 15 次无新输出 / 10 次连续失败
		runtime.NewGoalAlignmentMiddleware(objective, 6), // 6 次偏离目标
	)

	// 构建配置，包含 monitor kill 检查
	var killed atomic.Bool
	config := &runtime.ReActConfig{
		Objective:            objective,
		SystemPrompt:         systemPrompt,
		LLMProvider:          provider,
		MaxIterations:        e.maxIt,
		Temperature:          0.7,
		MaxTokens:            4000,
		MessageModifierChain: runtime.NewDefaultModifierChain(15),
		MiddlewareChain:      middlewareChain,
		TaskID:               action.TaskID,
		// Executor 的结构化输出格式
		OutputFormatPrompt: "你刚才完成了测试任务，现在需要输出结构化的观察结果。",
		OutputFormatSchema: `请输出以下JSON格式：

` + "```json\n" + `{
  "status": "completed",
  "summary": "简要总结你完成的工作",
  "observations": [
    {
      "statement": "你发现的具体问题或观察（例如：发现XSS漏洞）",
      "reasoning": "你为什么得出这个结论",
      "test_plan": "如何验证这个发现",
      "confidence": "low/medium/high",
      "severity": "low/medium/high/critical",
      "repro": {
        "domain": "web",
        "recipe": {
          "request": {
            "method": "GET",
            "url": "http://example.com/vulnerable",
            "headers": {"Cookie": "session=abc"},
            "body": ""
          }
        },
        "assert": {
          "body_contains": ["<script>alert(1)</script>", "XSS"],
          "status_code": 200
        }
      }
    }
  ]
}
` + "```\n\n" + `说明：
- observations 只放两类内容：(1) 漏洞假设——必须带完整 repro；(2) 关键执行结论（如认证已建立/攻击面确认等对后续动作有决策价值的上下文）——不带 repro 字段
- 不要把纯过程性记录（"访问了某页"、"请求返回 200"）写成 observation
- 如果没有发现问题时，observations 可以是空数组 []
- 漏洞 observation 必须包含完整的repro复现步骤
- ⚠️ 重要：domain="web"时，assert必须包含至少一个机器可验证的字段：
  * body_contains: 响应体必须包含的字符串数组（推荐）
  * status_code: 期望的HTTP状态码（数字）
  * header_contains: 响应头必须包含的字段，格式为对象 {"Header-Name": "substring"}，不是数组
  * body_not_contains: 响应体必须不含的字符串数组（如未跳登录页=认证绕过坐实）
- 不要只写description，必须提供具体的断言条件`,
		// kill 止血：迭代回调发现 action 已被 monitor kill（aborted）→ 置位，
		// 提前终止条件在下一轮立即结束 ReAct——不再空跑剩余迭代。
		EarlyStopCondition: func(string) bool { return killed.Load() },
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
			Msg("❌ 严重错误：没有可用工具！Executor 无法执行任何操作")
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
		Msg("🚀 开始执行 ReAct")

	result, err := reactRuntime.Run(ctx, config)
	if err != nil {
		// ReAct 失败：尝试降级策略——强制提取假设
		e.logger.Warn().
			Err(err).
			Str("action_id", actionID).
			Msg("⚠️  ReAct 执行失败，进入降级模式：强制提取假设")

		fallbackResult, fallbackErr := e.fallbackExtractHypothesis(ctx, config, actionID)
		if fallbackErr != nil {
			// 降级也失败：返回原始错误
			return nil, fmt.Errorf("ReAct 执行失败: %w", err)
		}

		e.logger.Info().
			Str("action_id", actionID).
			Msg("✅ 降级模式成功提取假设")
		return fallbackResult, nil
	}

	e.logger.Info().
		Str("action_id", actionID).
		Int("iterations", result.Iterations).
		Str("status", string(result.Status)).
		Msg("✅ ReAct 执行完成")

	return result, nil
}

// fallbackExtractHypothesis 是 ReAct 失败后的降级策略：
// 通过单轮 LLM 调用，基于 action 描述强制推导可能的漏洞假设。
//
// 业界最佳实践：智能体执行失败时不应完全静默，而要尽可能提取有价值的中间结果。
// 参考：Anthropic Claude Computer Use、LangChain AgentExecutor 的 handle_parsing_errors 机制。
func (e *Engine) fallbackExtractHypothesis(
	ctx context.Context,
	config *runtime.ReActConfig,
	actionID string,
) (*runtime.ReActResult, error) {
	// 1. 构建降级 prompt：引导 LLM 输出结构化假设
	fallbackPrompt := fmt.Sprintf(`
你是渗透测试专家。以下 Action 执行失败，但你仍需基于任务描述推导可能的漏洞假设。

**原始任务**：
%s

**要求**：
1. 分析任务目标，推导 2-3 个最可能存在的漏洞假设
2. 每个假设必须包含：
   - statement: 漏洞假设陈述（简洁明确）
   - repro: 复现步骤（JSON 数组，每步一个字符串）
   - severity: 严重等级（critical/high/medium/low）

**输出格式**（严格 JSON）：
{
  "status": "completed_with_hypothesis",
  "summary": "执行失败但推导出以下假设",
  "observations": [
    {
      "statement": "可能存在 XSS 漏洞",
      "repro": ["访问 http://target/?q=<script>alert(1)</script>", "检查响应是否未转义"],
      "severity": "high"
    }
  ]
}
`, config.SystemPrompt)

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

// processExecutorOutput 处理 Executor 输出，创建 Observation 节点
func (e *Engine) processExecutorOutput(
	ctx context.Context,
	action explorationgraph.Node,
	result *runtime.ReActResult,
) ([]evaluator.Attempt, error) {
	// 解析输出
	output, err := ParseExecutorOutput(result)
	if err != nil {
		e.logger.Warn().
			Err(err).
			Str("action_id", action.ID).
			Msg("解析 Executor 输出失败，使用空输出")

		output = &ExecutorOutput{
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
	// 从 agent_traffic 读取这个 action 产生的所有 HTTP 流量
	var trace ExecutionTrace
	if e.traceCollector != nil {
		trace, err = e.traceCollector.CollectTrace(ctx, action.TaskID, action.ID)
		if err != nil {
			e.logger.Warn().
				Err(err).
				Str("action_id", action.ID).
				Msg("收集 ExecutionTrace 失败，使用空 trace")
			// 使用空 trace，不影响流程
			trace = ExecutionTrace{
				Traffic: []TrafficRecord{},
				Session: SessionState{Cookies: make(map[string]string)},
				Meta:    TraceMeta{},
			}
		} else {
			e.logger.Info().
				Str("action_id", action.ID).
				Int("traffic_count", trace.Meta.TotalCount).
				Msg("成功收集 ExecutionTrace")
		}
	} else {
		e.logger.Warn().
			Str("action_id", action.ID).
			Msg("TraceCollector 未配置，无法收集 trace")
		trace = ExecutionTrace{
			Traffic: []TrafficRecord{},
			Session: SessionState{Cookies: make(map[string]string)},
			Meta:    TraceMeta{},
		}
	}

	// 为每个观察结果创建 observation 节点
	var attempts []evaluator.Attempt

	e.logger.Debug().
		Str("action_id", action.ID).
		Int("total_observations", len(output.Observations)).
		Msg("开始处理 observations")

	for i, obs := range output.Observations {
		e.logger.Debug().
			Str("action_id", action.ID).
			Int("observation_index", i).
			Str("statement", obs.Statement).
			Int("repro_len", len(obs.Repro)).
			Msg("处理单个 observation")

		// 降级模式生成的假设可能没有 repro（推测性假设，非实际执行）
		// 仍需创建 observation 节点供 Evaluator 评估
		if len(obs.Repro) == 0 {
			e.logger.Warn().
				Str("action_id", action.ID).
				Int("observation_index", i).
				Str("statement", obs.Statement).
				Msg("observation 缺少 repro（可能是降级模式生成的假设），仍继续创建")
		}

		// 附加 ExecutionTrace 到 observation：裁决官看到的是完整执行轨迹，
		// 而不是 executor 转述的不可靠结论
		obs.ExecutionTrace = &trace

		e.logger.Debug().
			Str("action_id", action.ID).
			Int("observation_index", i).
			Int("trace_traffic_count", trace.Meta.TotalCount).
			Msg("已附加 ExecutionTrace 到 observation")

		// 配方归一化在循环内做一次、节点与 Attempt 共用同一结果——归一化
		// 若藏在节点构造内部，矫正结果不会传回 obs，事件路径的 Attempt
		// 会携带原始破碎形状（数组信封/标量断言）直通复现门被确定性杀死，
		// 而节点上存的却是修复后的配方：同一观察两条消费路径各说各话
		// （e2e 实测 4 条真漏洞假设因此冤死）。矫正失败保留原始配方，
		// 由门侧以 invalid_recipe 终局——归一化不是万能的，但必须对所有
		// 消费路径一致。
		if normalized, note, nErr := tools.NormalizeReproEnvelope(obs.Repro); nErr == nil {
			obs.Repro = normalized
			if note != "" {
				e.logger.Info().
					Str("action_id", action.ID).
					Int("observation_index", i).
					Msg("repro 形状已矫正：" + note)
			}
		} else {
			e.logger.Warn().
				Str("action_id", action.ID).
				Int("observation_index", i).
				Err(nErr).
				Msg("repro 归一化失败，保留原始配方（门侧将以 invalid_recipe 终局）")
		}

		// 创建 observation 节点
		node, err := e.createObservationNode(ctx, action.TaskID, action.ID, obs)
		if err != nil {
			e.logger.Error().
				Err(err).
				Str("action_id", action.ID).
				Int("observation_index", i).
				Msg("创建 observation 节点失败")
			continue
		}

		e.logger.Info().
			Str("action_id", action.ID).
			Str("observation_id", node.ID).
			Str("statement", obs.Statement).
			Msg("创建 observation 节点成功")

		// 转换为 Attempt
		attempt, err := e.observationToAttempt(action.TaskID, node, obs)
		if err != nil {
			e.logger.Error().
				Err(err).
				Str("observation_id", node.ID).
				Msg("转换 observation 为 Attempt 失败")
			continue
		}

		attempts = append(attempts, attempt)
	}

	e.logger.Info().
		Str("action_id", action.ID).
		Int("attempts_count", len(attempts)).
		Msg("Executor 输出处理完成")

	return attempts, nil
}
